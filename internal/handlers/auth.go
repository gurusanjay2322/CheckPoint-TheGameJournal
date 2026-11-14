package handlers

import (
	"errors"
	"log"
	"net/url"

	"github.com/checkpoint/server/internal/config"
	"github.com/checkpoint/server/internal/db"
	"github.com/checkpoint/server/internal/models"
	"github.com/checkpoint/server/internal/utils"
	"github.com/checkpoint/server/internal/worker"
	"github.com/checkpoint/server/pkg/steam"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"gorm.io/gorm"
)

const steamCallbackPath = "/api/v1/auth/steam/callback"

type AuthHandler struct {
	Cfg         *config.Config
	SteamClient *steam.Client
	AsynqClient *asynq.Client
}

func NewAuthHandler(cfg *config.Config, steamClient *steam.Client, asynqClient *asynq.Client) *AuthHandler {
	return &AuthHandler{
		Cfg:         cfg,
		SteamClient: steamClient,
		AsynqClient: asynqClient,
	}
}

type SignupRequest struct {
	Email    string `json:"email" validate:"required,email"`
	Username string `json:"username" validate:"required,min=2,max=50"`
	Password string `json:"password" validate:"required,min=6"`
}

// Signup godoc
// @Summary Traditional Signup
// @Description Register a new user with email and password
// @Tags auth
// @Accept json
// @Produce json
// @Param request body SignupRequest true "Signup Request"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 409 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /auth/signup [post]
func (h *AuthHandler) Signup(c *fiber.Ctx) error {
	var req SignupRequest
	if err := utils.ParseAndValidate(c, &req); err != nil {
		return err
	}

	hash, err := utils.HashPassword(req.Password)
	if err != nil {
		return utils.InternalError(c, "failed to process password")
	}

	user := models.User{
		Email:        &req.Email,
		Username:     req.Username,
		PasswordHash: &hash,
	}

	if err := db.DB.Create(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return utils.Conflict(c, "email is already in use")
		}
		return utils.InternalError(c, "failed to create user")
	}

	token, err := utils.GenerateJWT(user.ID.String(), h.Cfg.JWTSecret)
	if err != nil {
		return utils.InternalError(c, "failed to generate token")
	}

	return c.JSON(fiber.Map{
		"token": token,
		"user":  user,
	})
}

type EmailLoginRequest struct {
	Email    string `json:"email" validate:"required,email"`
	Password string `json:"password" validate:"required"`
}

// EmailLogin godoc
// @Summary Traditional Login
// @Description Authenticates a user using email and password
// @Tags auth
// @Accept json
// @Produce json
// @Param request body EmailLoginRequest true "Login Request"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 401 {object} map[string]string
// @Router /auth/login [post]
func (h *AuthHandler) EmailLogin(c *fiber.Ctx) error {
	var req EmailLoginRequest
	if err := utils.ParseAndValidate(c, &req); err != nil {
		return err
	}

	var user models.User
	result := db.DB.Where("email = ?", req.Email).First(&user)
	if result.Error != nil || user.PasswordHash == nil {
		return utils.Unauthorized(c, "invalid email or password")
	}

	if !utils.CheckPasswordHash(req.Password, *user.PasswordHash) {
		return utils.Unauthorized(c, "invalid email or password")
	}

	token, err := utils.GenerateJWT(user.ID.String(), h.Cfg.JWTSecret)
	if err != nil {
		return utils.InternalError(c, "failed to generate token")
	}

	return c.JSON(fiber.Map{
		"token": token,
		"user":  user,
	})
}

func (h *AuthHandler) steamCallbackURL() string {
	return h.Cfg.PublicURL + steamCallbackPath
}

func (h *AuthHandler) steamLoginURL(state string) string {
	returnTo := h.steamCallbackURL()
	if state != "" {
		returnTo += "?" + url.Values{"state": {state}}.Encode()
	}
	return steam.OpenIDLoginURL(returnTo, h.Cfg.PublicURL)
}

// redirectToApp sends the browser back to the app's deep link with params.
func (h *AuthHandler) redirectToApp(c *fiber.Ctx, params url.Values) error {
	target, err := url.Parse(h.Cfg.AppRedirectURL)
	if err != nil {
		return utils.InternalError(c, "invalid APP_REDIRECT_URL")
	}
	q := target.Query()
	for k, v := range params {
		q[k] = v
	}
	target.RawQuery = q.Encode()
	return c.Redirect(target.String(), fiber.StatusFound)
}

func (h *AuthHandler) enqueueSteamSync(userID uuid.UUID, steamID string) {
	task, err := worker.NewSyncSteamLibraryTask(userID.String(), steamID)
	if err == nil {
		_, err = h.AsynqClient.Enqueue(task)
	}
	if err != nil {
		log.Printf("Failed to enqueue steam sync for user %s: %v", userID, err)
	}
}

// SteamLogin godoc
// @Summary Login/Signup with Steam
// @Description Redirects the browser to Steam sign-in. After Steam authenticates the user, /auth/steam/callback redirects to APP_REDIRECT_URL with `token` (or `error`).
// @Tags auth
// @Success 302
// @Router /auth/steam/login [get]
func (h *AuthHandler) SteamLogin(c *fiber.Ctx) error {
	return c.Redirect(h.steamLoginURL(""), fiber.StatusFound)
}

// LinkSteam godoc
// @Summary Start linking a Steam account
// @Description Returns a Steam sign-in URL for the current user. Open it in a browser; after sign-in, /auth/steam/callback links the account, starts a library sync and redirects to APP_REDIRECT_URL with `linked=true` (or `error`).
// @Tags auth
// @Security BearerAuth
// @Produce json
// @Success 200 {object} map[string]string
// @Failure 401 {object} map[string]string
// @Router /auth/steam/link [post]
func (h *AuthHandler) LinkSteam(c *fiber.Ctx) error {
	userIDStr, ok := c.Locals("user_id").(string)
	if !ok {
		return utils.Unauthorized(c, "unauthorized")
	}

	state, err := utils.GenerateSteamLinkState(userIDStr, h.Cfg.JWTSecret)
	if err != nil {
		return utils.InternalError(c, "failed to start steam link")
	}

	return c.JSON(fiber.Map{"url": h.steamLoginURL(state)})
}

// SteamCallback godoc
// @Summary Steam OpenID callback
// @Description Called by Steam after sign-in. Verifies the assertion with Steam, then logs in or links the account and redirects to APP_REDIRECT_URL.
// @Tags auth
// @Success 302
// @Router /auth/steam/callback [get]
func (h *AuthHandler) SteamCallback(c *fiber.Ctx) error {
	params, err := url.ParseQuery(string(c.Request().URI().QueryString()))
	if err != nil {
		return h.redirectToApp(c, url.Values{"error": {"invalid_callback"}})
	}

	steamID, err := h.SteamClient.VerifyOpenID(params, h.steamCallbackURL())
	if err != nil {
		log.Printf("Steam OpenID verification failed: %v", err)
		return h.redirectToApp(c, url.Values{"error": {"steam_verification_failed"}})
	}

	// Read state from the Steam-signed return_to, not the outer query string.
	signedReturnTo, _ := url.Parse(params.Get("openid.return_to"))
	if state := signedReturnTo.Query().Get("state"); state != "" {
		return h.completeSteamLink(c, state, steamID)
	}
	return h.completeSteamLogin(c, steamID)
}

func (h *AuthHandler) completeSteamLogin(c *fiber.Ctx, steamID string) error {
	summaries, err := h.SteamClient.GetPlayerSummaries([]string{steamID})
	if err != nil || len(summaries) == 0 {
		log.Printf("Steam profile lookup failed for %s (check STEAM_API_KEY): err=%v, results=%d", steamID, err, len(summaries))
		return h.redirectToApp(c, url.Values{"error": {"steam_profile_unavailable"}})
	}
	player := summaries[0]

	var user models.User
	result := db.DB.Where("steam_id = ?", steamID).First(&user)

	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		user = models.User{
			SteamID:   &steamID,
			Username:  player.PersonaName,
			AvatarURL: player.AvatarFull,
		}
		if err := db.DB.Create(&user).Error; err != nil {
			return h.redirectToApp(c, url.Values{"error": {"account_creation_failed"}})
		}
	} else if result.Error != nil {
		return h.redirectToApp(c, url.Values{"error": {"server_error"}})
	} else if user.AvatarURL != player.AvatarFull {
		db.DB.Model(&user).Update("avatar_url", player.AvatarFull)
	}

	h.enqueueSteamSync(user.ID, steamID)

	token, err := utils.GenerateJWT(user.ID.String(), h.Cfg.JWTSecret)
	if err != nil {
		return h.redirectToApp(c, url.Values{"error": {"server_error"}})
	}

	return h.redirectToApp(c, url.Values{"token": {token}})
}

func (h *AuthHandler) completeSteamLink(c *fiber.Ctx, state string, steamID string) error {
	claims, err := utils.ParseSteamLinkState(state, h.Cfg.JWTSecret)
	if err != nil {
		return h.redirectToApp(c, url.Values{"error": {"link_expired"}})
	}
	userID, err := uuid.Parse(claims.UserID)
	if err != nil {
		return h.redirectToApp(c, url.Values{"error": {"link_expired"}})
	}

	var user models.User
	if err := db.DB.First(&user, userID).Error; err != nil {
		return h.redirectToApp(c, url.Values{"error": {"user_not_found"}})
	}

	updates := map[string]interface{}{"steam_id": steamID}
	if user.AvatarURL == "" {
		if summaries, err := h.SteamClient.GetPlayerSummaries([]string{steamID}); err == nil && len(summaries) > 0 {
			updates["avatar_url"] = summaries[0].AvatarFull
		}
	}

	if err := db.DB.Model(&user).Updates(updates).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return h.redirectToApp(c, url.Values{"error": {"steam_already_linked"}})
		}
		return h.redirectToApp(c, url.Values{"error": {"server_error"}})
	}

	h.enqueueSteamSync(user.ID, steamID)

	return h.redirectToApp(c, url.Values{"linked": {"true"}})
}

type UpdateEmailRequest struct {
	Email string `json:"email" validate:"required,email"`
}

// GetMe godoc
// @Summary Get Current User
// @Description Get the currently authenticated user's profile
// @Tags auth
// @Security BearerAuth
// @Accept json
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Failure 401 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Router /auth/me [get]
func (h *AuthHandler) GetMe(c *fiber.Ctx) error {
	userIDStr, ok := c.Locals("user_id").(string)
	if !ok {
		return utils.Unauthorized(c, "unauthorized")
	}

	var user models.User
	if err := db.DB.First(&user, "id = ?", userIDStr).Error; err != nil {
		return utils.NotFound(c, "user not found")
	}

	return c.JSON(fiber.Map{"user": user})
}

type UpdateProfileRequest struct {
	Username  string `json:"username,omitempty" validate:"omitempty,min=2,max=50"`
	Bio       string `json:"bio,omitempty"`
	AvatarURL string `json:"avatar_url,omitempty"`
}

// UpdateProfile godoc
// @Summary Update Profile
// @Description Update the current user's profile (username, bio, avatar)
// @Tags auth
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body UpdateProfileRequest true "Update Profile Request"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 401 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /auth/profile [put]
func (h *AuthHandler) UpdateProfile(c *fiber.Ctx) error {
	userIDStr, ok := c.Locals("user_id").(string)
	if !ok {
		return utils.Unauthorized(c, "unauthorized")
	}
	userID, _ := uuid.Parse(userIDStr)

	var req UpdateProfileRequest
	if err := utils.ParseAndValidate(c, &req); err != nil {
		return err
	}

	var user models.User
	if err := db.DB.First(&user, userID).Error; err != nil {
		return utils.NotFound(c, "user not found")
	}

	updates := map[string]interface{}{}
	if req.Username != "" {
		updates["username"] = req.Username
	}
	if req.Bio != "" {
		updates["bio"] = req.Bio
	}
	if req.AvatarURL != "" {
		updates["avatar_url"] = req.AvatarURL
	}

	if len(updates) == 0 {
		return utils.BadRequest(c, "no fields to update")
	}

	if err := db.DB.Model(&user).Updates(updates).Error; err != nil {
		return utils.InternalError(c, "failed to update profile")
	}

	db.DB.First(&user, userID)
	return c.JSON(fiber.Map{"user": user})
}

// UpdateEmail godoc
// @Summary Update Email
// @Description Update the user's email address
// @Tags auth
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body UpdateEmailRequest true "Update Email Request"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 401 {object} map[string]string
// @Failure 409 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /auth/email [put]
func (h *AuthHandler) UpdateEmail(c *fiber.Ctx) error {
	userIDStr, ok := c.Locals("user_id").(string)
	if !ok {
		return utils.Unauthorized(c, "unauthorized")
	}
	userID, _ := uuid.Parse(userIDStr)

	var req UpdateEmailRequest
	if err := utils.ParseAndValidate(c, &req); err != nil {
		return err
	}

	var user models.User
	if err := db.DB.First(&user, userID).Error; err != nil {
		return utils.NotFound(c, "user not found")
	}

	if err := db.DB.Model(&user).Update("email", req.Email).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return utils.Conflict(c, "email is already in use")
		}
		return utils.InternalError(c, "failed to update email")
	}

	return c.JSON(fiber.Map{
		"message": "Email updated successfully",
		"user":    user,
	})
}
