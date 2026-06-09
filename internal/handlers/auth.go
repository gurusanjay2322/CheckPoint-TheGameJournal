package handlers

import (
	"github.com/checkpoint/server/internal/config"
	"github.com/checkpoint/server/internal/db"
	"github.com/checkpoint/server/internal/models"
	"github.com/checkpoint/server/internal/utils"
	"github.com/checkpoint/server/internal/worker"
	"github.com/checkpoint/server/pkg/steam"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

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
		return utils.BadRequest(c, "email may already be in use")
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

type SteamLoginRequest struct {
	SteamID string `json:"steam_id" validate:"required"`
}

// SteamLogin godoc
// @Summary Login/Signup with Steam
// @Description Authenticates or creates a user using their Steam ID and syncs library
// @Tags auth
// @Accept json
// @Produce json
// @Param request body SteamLoginRequest true "Steam Login Request"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 401 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /auth/steam [post]
func (h *AuthHandler) SteamLogin(c *fiber.Ctx) error {
	var req SteamLoginRequest
	if err := utils.ParseAndValidate(c, &req); err != nil {
		return err
	}

	summaries, err := h.SteamClient.GetPlayerSummaries([]string{req.SteamID})
	if err != nil || len(summaries) == 0 {
		return utils.Unauthorized(c, "invalid steam_id or failed to fetch profile")
	}

	player := summaries[0]

	var user models.User
	result := db.DB.Where("steam_id = ?", req.SteamID).First(&user)

	if result.Error != nil {
		user = models.User{
			SteamID:   &req.SteamID,
			Username:  player.PersonaName,
			AvatarURL: player.AvatarFull,
		}
		if err := db.DB.Create(&user).Error; err != nil {
			return utils.InternalError(c, "failed to create user")
		}
	} else {
		if user.Username != player.PersonaName || user.AvatarURL != player.AvatarFull {
			user.Username = player.PersonaName
			user.AvatarURL = player.AvatarFull
			db.DB.Save(&user)
		}
	}

	task, err := worker.NewSyncSteamLibraryTask(user.ID.String(), req.SteamID)
	if err == nil {
		h.AsynqClient.Enqueue(task)
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

// LinkSteam godoc
// @Summary Link Steam Account
// @Description Link a steam account to an existing user and sync library
// @Tags auth
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body SteamLoginRequest true "Steam Link Request"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 401 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /auth/steam/link [post]
func (h *AuthHandler) LinkSteam(c *fiber.Ctx) error {
	userIDStr, ok := c.Locals("user_id").(string)
	if !ok {
		return utils.Unauthorized(c, "unauthorized")
	}
	userID, _ := uuid.Parse(userIDStr)

	var req SteamLoginRequest
	if err := utils.ParseAndValidate(c, &req); err != nil {
		return err
	}

	summaries, err := h.SteamClient.GetPlayerSummaries([]string{req.SteamID})
	if err != nil || len(summaries) == 0 {
		return utils.BadRequest(c, "invalid steam_id or failed to fetch profile")
	}
	player := summaries[0]

	var user models.User
	if err := db.DB.First(&user, userID).Error; err != nil {
		return utils.NotFound(c, "user not found")
	}

	user.SteamID = &req.SteamID
	if user.AvatarURL == "" {
		user.AvatarURL = player.AvatarFull
	}

	if err := db.DB.Save(&user).Error; err != nil {
		return utils.BadRequest(c, "steam account may already be linked to another account")
	}

	task, err := worker.NewSyncSteamLibraryTask(userIDStr, req.SteamID)
	if err == nil {
		h.AsynqClient.Enqueue(task)
	}

	return c.JSON(fiber.Map{
		"message": "Steam account linked successfully. Library syncing in background.",
		"user":    user,
	})
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

	user.Email = &req.Email
	if err := db.DB.Save(&user).Error; err != nil {
		return utils.InternalError(c, "failed to update email. it may already be in use.")
	}

	return c.JSON(fiber.Map{
		"message": "Email updated successfully",
		"user":    user,
	})
}
