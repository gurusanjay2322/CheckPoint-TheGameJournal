package handlers

import (
	"github.com/checkpoint/server/internal/config"
	"github.com/checkpoint/server/internal/db"
	"github.com/checkpoint/server/internal/models"
	"github.com/checkpoint/server/internal/utils"
	"github.com/checkpoint/server/internal/worker"
	"github.com/checkpoint/server/pkg/steam"
	"github.com/gofiber/fiber/v2"
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

type LoginRequest struct {
	SteamID string `json:"steam_id"`
}

// Login godoc
// @Summary Login with Steam
// @Description Authenticates a user using their Steam ID and returns a JWT token
// @Tags auth
// @Accept json
// @Produce json
// @Param request body LoginRequest true "Login Request"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 401 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /auth/login [post]
func (h *AuthHandler) Login(c *fiber.Ctx) error {
	var req LoginRequest
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
		user = models.User{
			SteamID:   req.SteamID,
			Username:  player.PersonaName,
			AvatarURL: player.AvatarFull,
		}
		if err := db.DB.Create(&user).Error; err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to create user"})
		}
		
		// Trigger asynchronous Steam game sync job
		task, err := worker.NewSyncSteamLibraryTask(user.ID.String(), req.SteamID)
		if err == nil {
			h.AsynqClient.Enqueue(task)
		}
	} else {
		if user.Username != player.PersonaName || user.AvatarURL != player.AvatarFull {
			user.Username = player.PersonaName
			user.AvatarURL = player.AvatarFull
			db.DB.Save(&user)
		}
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
