package handlers

import (
	"github.com/checkpoint/server/internal/db"
	"github.com/checkpoint/server/internal/models"
	"github.com/checkpoint/server/internal/utils"
	"github.com/checkpoint/server/internal/worker"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

var validStatuses = map[string]bool{
	"playing":   true,
	"completed": true,
	"backlog":   true,
	"dropped":   true,
	"wishlist":  true,
}

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
// @Param status query string false "Filter by status (playing, completed, backlog, dropped, wishlist)"
// @Param page query int false "Page number (default 1)"
// @Param page_size query int false "Page size (default 20, max 100)"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /library/{user_id} [get]
func (h *LibraryHandler) GetUserLibrary(c *fiber.Ctx) error {
	userID, err := uuid.Parse(c.Params("user_id"))
	if err != nil {
		return utils.BadRequest(c, "invalid user id")
	}

	page, pageSize := utils.ParsePagination(c)
	status := c.Query("status", "")

	query := db.DB.Preload("Game").Where("user_id = ?", userID)

	if status != "" {
		if !validStatuses[status] {
			return utils.BadRequest(c, "invalid status; must be one of: playing, completed, backlog, dropped, wishlist")
		}
		query = query.Where("status = ?", status)
	}

	var total int64
	query.Model(&models.UserGame{}).Count(&total)

	var userGames []models.UserGame
	if err := query.Offset((page-1)*pageSize).Limit(pageSize).Find(&userGames).Error; err != nil {
		return utils.InternalError(c, "failed to fetch library")
	}

	return c.JSON(fiber.Map{"data": userGames, "meta": utils.NewPaginationMeta(int(total), page, pageSize)})
}

type UpdateLibraryRequest struct {
	IGDBID    int    `json:"igdb_id" validate:"required"`
	GameTitle string `json:"game_title"`
	Status    string `json:"status" validate:"required"`
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
		return utils.Unauthorized(c, "unauthorized")
	}
	userID, _ := uuid.Parse(userIDStr)

	var req UpdateLibraryRequest
	if err := utils.ParseAndValidate(c, &req); err != nil {
		return err
	}

	if !validStatuses[req.Status] {
		return utils.BadRequest(c, "invalid status; must be one of: playing, completed, backlog, dropped, wishlist")
	}

	var game models.Game
	result := db.DB.Where("igdb_id = ?", req.IGDBID).First(&game)
	if result.Error != nil {
		game = models.Game{
			IGDBID: &req.IGDBID,
			Title:  req.GameTitle,
		}
		if err := db.DB.Create(&game).Error; err != nil {
			return utils.InternalError(c, "failed to create local game record")
		}

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

// RemoveFromLibrary godoc
// @Summary Remove Game from Library
// @Description Remove a game from the user's library
// @Tags library
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param game_id path string true "Game UUID"
// @Success 204 {object} nil
// @Failure 400 {object} map[string]string
// @Failure 401 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Router /library/{game_id} [delete]
func (h *LibraryHandler) RemoveFromLibrary(c *fiber.Ctx) error {
	userIDStr, ok := c.Locals("user_id").(string)
	if !ok {
		return utils.Unauthorized(c, "unauthorized")
	}
	userID, _ := uuid.Parse(userIDStr)

	gameID, err := uuid.Parse(c.Params("game_id"))
	if err != nil {
		return utils.BadRequest(c, "invalid game id")
	}

	result := db.DB.Where("user_id = ? AND game_id = ?", userID, gameID).Delete(&models.UserGame{})
	if result.RowsAffected == 0 {
		return utils.NotFound(c, "game not found in library")
	}

	db.DB.Where("user_id = ? AND target_id = ? AND target_type = ?", userID, gameID, "game").Delete(&models.Activity{})

	return c.SendStatus(fiber.StatusNoContent)
}
