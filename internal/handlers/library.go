package handlers

import (
	"github.com/checkpoint/server/internal/db"
	"github.com/checkpoint/server/internal/models"
	"github.com/checkpoint/server/internal/worker"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

type LibraryHandler struct {
	asynqClient *asynq.Client
}

func NewLibraryHandler(asynqClient *asynq.Client) *LibraryHandler {
	return &LibraryHandler{
		asynqClient: asynqClient,
	}
}

// GetUserLibrary godoc
// @Summary Get User Library
// @Description Fetch all games in a user's library
// @Tags library
// @Accept json
// @Produce json
// @Param user_id path string true "User UUID"
// @Success 200 {array} models.UserGame
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /library/{user_id} [get]
func (h *LibraryHandler) GetUserLibrary(c *fiber.Ctx) error {
	userIDStr := c.Params("user_id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid user id"})
	}

	var userGames []models.UserGame
	result := db.DB.Preload("Game").Where("user_id = ?", userID).Find(&userGames)
	if result.Error != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to fetch library"})
	}

	return c.JSON(userGames)
}

type UpdateLibraryRequest struct {
	IGDBID    int    `json:"igdb_id"`
	GameTitle string `json:"game_title"`
	Status    string `json:"status"` // playing, completed, backlog, dropped, wishlist
}

// UpdateStatus godoc
// @Summary Update Game Status
// @Description Add a game to library or update its status
// @Tags library
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body UpdateLibraryRequest true "Update Status Request"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 401 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /library/update [post]
func (h *LibraryHandler) UpdateStatus(c *fiber.Ctx) error {
	userIDStr, ok := c.Locals("user_id").(string)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
	}

	userID, _ := uuid.Parse(userIDStr)

	var req UpdateLibraryRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	var game models.Game
	result := db.DB.Where("igdb_id = ?", req.IGDBID).First(&game)
	if result.Error != nil {
		game = models.Game{
			IGDBID: &req.IGDBID,
			Title:  req.GameTitle,
		}
		if err := db.DB.Create(&game).Error; err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to create local game record"})
		}

		// Enqueue background enrichment task to overwrite title with official IGDB data
		if task, err := worker.NewEnrichIGDBGameTask(req.IGDBID); err == nil {
			h.asynqClient.Enqueue(task)
		}
	}

	var userGame models.UserGame
	db.DB.Where("user_id = ? AND game_id = ?", userID, game.ID).FirstOrCreate(&userGame, models.UserGame{
		UserID: userID,
		GameID: game.ID,
		Status: req.Status,
	})

	userGame.Status = req.Status
	db.DB.Save(&userGame)

	activity := models.Activity{
		UserID:     userID,
		ActionType: "status_" + req.Status,
		TargetID:   game.ID,
		TargetType: "game",
	}
	db.DB.Create(&activity)

	return c.JSON(fiber.Map{"message": "library updated", "game": game, "status": req.Status})
}
