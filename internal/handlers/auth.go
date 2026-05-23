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
	Email    string `json:"email"`
	Username string `json:"username"`
	Password string `json:"password"`
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
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
	}

	if req.Email == "" || req.Password == "" || req.Username == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Email, username, and password are required"})
	}

	hash, err := utils.HashPassword(req.Password)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to hash password"})
	}

	user := models.User{
		Email:        &req.Email,
		Username:     req.Username,
		PasswordHash: &hash,
	}

	if err := db.DB.Create(&user).Error; err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Email may already be in use"})
	}

	token, err := utils.GenerateJWT(user.ID.String(), h.Cfg.JWTSecret)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to generate token"})
	}

	return c.JSON(fiber.Map{
		"token": token,
		"user":  user,
	})
}

type EmailLoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
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
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
	}

	var user models.User
	result := db.DB.Where("email = ?", req.Email).First(&user)
	if result.Error != nil || user.PasswordHash == nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Invalid email or password"})
	}

	if !utils.CheckPasswordHash(req.Password, *user.PasswordHash) {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Invalid email or password"})
	}

	token, err := utils.GenerateJWT(user.ID.String(), h.Cfg.JWTSecret)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to generate token"})
	}

	return c.JSON(fiber.Map{
		"token": token,
		"user":  user,
	})
}

type SteamLoginRequest struct {
	SteamID string `json:"steam_id"`
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
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
	}

	if req.SteamID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "steam_id is required"})
	}

	summaries, err := h.SteamClient.GetPlayerSummaries([]string{req.SteamID})
	if err != nil || len(summaries) == 0 {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Invalid steam_id or failed to fetch profile"})
	}
	
	player := summaries[0]

	var user models.User
	result := db.DB.Where("steam_id = ?", req.SteamID).First(&user)
	
	if result.Error != nil {
		// New User via Steam
		user = models.User{
			SteamID:   &req.SteamID,
			Username:  player.PersonaName,
			AvatarURL: player.AvatarFull,
		}
		if err := db.DB.Create(&user).Error; err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to create user"})
		}
	} else {
		if user.Username != player.PersonaName || user.AvatarURL != player.AvatarFull {
			user.Username = player.PersonaName
			user.AvatarURL = player.AvatarFull
			db.DB.Save(&user)
		}
	}

	// Always queue a background sync when logging in via Steam 
	// to ensure their library is up to date with new purchases
	task, err := worker.NewSyncSteamLibraryTask(user.ID.String(), req.SteamID)
	if err == nil {
		h.AsynqClient.Enqueue(task)
	}

	token, err := utils.GenerateJWT(user.ID.String(), h.Cfg.JWTSecret)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to generate token"})
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
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
	}
	userID, _ := uuid.Parse(userIDStr)

	var req SteamLoginRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
	}

	if req.SteamID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "steam_id is required"})
	}

	summaries, err := h.SteamClient.GetPlayerSummaries([]string{req.SteamID})
	if err != nil || len(summaries) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid steam_id or failed to fetch profile"})
	}
	player := summaries[0]

	var user models.User
	if err := db.DB.First(&user, userID).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "User not found"})
	}

	user.SteamID = &req.SteamID
	// Optionally update avatar if they don't have one
	if user.AvatarURL == "" {
		user.AvatarURL = player.AvatarFull
	}

	if err := db.DB.Save(&user).Error; err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Steam account may already be linked to another account"})
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
	Email string `json:"email"`
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
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
	}
	userID, _ := uuid.Parse(userIDStr)

	var req UpdateEmailRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
	}

	if req.Email == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Email cannot be empty"})
	}

	var user models.User
	if err := db.DB.First(&user, userID).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "User not found"})
	}

	user.Email = &req.Email
	if err := db.DB.Save(&user).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to update email. It may already be in use."})
	}

	return c.JSON(fiber.Map{
		"message": "Email updated successfully",
		"user":    user,
	})
}
