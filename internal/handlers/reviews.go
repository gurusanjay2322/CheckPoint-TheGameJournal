package handlers

import (
	"errors"

	"github.com/checkpoint/server/internal/catalog"
	"github.com/checkpoint/server/internal/db"
	"github.com/checkpoint/server/internal/models"
	"github.com/checkpoint/server/internal/utils"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"gorm.io/gorm"
)

var errAlreadyReviewed = errors.New("already reviewed")

type ReviewsHandler struct {
	asynqClient *asynq.Client
}

func NewReviewsHandler(asynqClient *asynq.Client) *ReviewsHandler {
	return &ReviewsHandler{
		asynqClient: asynqClient,
	}
}

type CreateReviewRequest struct {
	IGDBID          int     `json:"igdb_id" validate:"required"`
	GameTitle       string  `json:"game_title"`
	Rating          float32 `json:"rating" validate:"gte=0,lte=5"`
	Content         string  `json:"content"`
	ContainsSpoiler bool    `json:"contains_spoiler"`
}

// CreateReview godoc
// @Summary Create a Review
// @Description Write a review and rate a game
// @Tags reviews
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body CreateReviewRequest true "Create Review Request"
// @Success 200 {object} models.Review
// @Failure 400 {object} map[string]string
// @Failure 401 {object} map[string]string
// @Failure 409 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /reviews [post]
func (h *ReviewsHandler) CreateReview(c *fiber.Ctx) error {
	userIDStr, ok := c.Locals("user_id").(string)
	if !ok {
		return utils.Unauthorized(c, "unauthorized")
	}
	userID, _ := uuid.Parse(userIDStr)

	var req CreateReviewRequest
	if err := utils.ParseAndValidate(c, &req); err != nil {
		return err
	}

	var review models.Review
	var created bool
	err := db.DB.Transaction(func(tx *gorm.DB) error {
		game, gameCreated, err := catalog.FindOrCreateByIGDB(tx, req.IGDBID, req.GameTitle)
		if err != nil {
			return err
		}
		created = gameCreated

		var existing int64
		if err := tx.Model(&models.Review{}).Where("user_id = ? AND game_id = ?", userID, game.ID).Count(&existing).Error; err != nil {
			return err
		}
		if existing > 0 {
			return errAlreadyReviewed
		}

		review = models.Review{
			UserID:          userID,
			GameID:          game.ID,
			Rating:          req.Rating,
			Content:         req.Content,
			ContainsSpoiler: req.ContainsSpoiler,
		}
		if err := tx.Omit("User", "Game").Create(&review).Error; err != nil {
			if errors.Is(err, gorm.ErrDuplicatedKey) {
				return errAlreadyReviewed
			}
			return err
		}

		return tx.Omit("User").Create(&models.Activity{
			UserID:     userID,
			ActionType: "reviewed",
			TargetID:   review.ID,
			TargetType: "review",
		}).Error
	})
	if errors.Is(err, errAlreadyReviewed) {
		return utils.Conflict(c, "you have already reviewed this game")
	}
	if err != nil {
		return utils.InternalError(c, "failed to create review")
	}

	if created {
		enqueueIGDBEnrichment(h.asynqClient, req.IGDBID)
	}

	db.DB.Preload("User").Preload("Game").First(&review, review.ID)

	return c.JSON(review)
}

type UpdateReviewRequest struct {
	Rating          *float32 `json:"rating,omitempty" validate:"omitempty,gte=0,lte=5"`
	Content         *string  `json:"content,omitempty"`
	ContainsSpoiler *bool    `json:"contains_spoiler,omitempty"`
}

// UpdateReview godoc
// @Summary Update a Review
// @Description Update your review for a game
// @Tags reviews
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Review UUID"
// @Param request body UpdateReviewRequest true "Update Review Request"
// @Success 200 {object} models.Review
// @Failure 400 {object} map[string]string
// @Failure 401 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Router /reviews/{id} [put]
func (h *ReviewsHandler) UpdateReview(c *fiber.Ctx) error {
	userIDStr, ok := c.Locals("user_id").(string)
	if !ok {
		return utils.Unauthorized(c, "unauthorized")
	}
	userID, _ := uuid.Parse(userIDStr)

	reviewID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return utils.BadRequest(c, "invalid review id")
	}

	var req UpdateReviewRequest
	if err := utils.ParseAndValidate(c, &req); err != nil {
		return err
	}

	var review models.Review
	if err := db.DB.First(&review, reviewID).Error; err != nil {
		return utils.NotFound(c, "review not found")
	}

	if review.UserID != userID {
		return utils.Unauthorized(c, "you can only update your own reviews")
	}

	updates := map[string]interface{}{}
	if req.Rating != nil {
		updates["rating"] = *req.Rating
	}
	if req.Content != nil {
		updates["content"] = *req.Content
	}
	if req.ContainsSpoiler != nil {
		updates["contains_spoiler"] = *req.ContainsSpoiler
	}

	if err := db.DB.Model(&review).Updates(updates).Error; err != nil {
		return utils.InternalError(c, "failed to update review")
	}

	db.DB.Preload("User").Preload("Game").First(&review, review.ID)
	return c.JSON(review)
}

// DeleteReview godoc
// @Summary Delete a Review
// @Description Delete your review for a game
// @Tags reviews
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path string true "Review UUID"
// @Success 204 {object} nil
// @Failure 400 {object} map[string]string
// @Failure 401 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Router /reviews/{id} [delete]
func (h *ReviewsHandler) DeleteReview(c *fiber.Ctx) error {
	userIDStr, ok := c.Locals("user_id").(string)
	if !ok {
		return utils.Unauthorized(c, "unauthorized")
	}
	userID, _ := uuid.Parse(userIDStr)

	reviewID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return utils.BadRequest(c, "invalid review id")
	}

	var review models.Review
	if err := db.DB.First(&review, reviewID).Error; err != nil {
		return utils.NotFound(c, "review not found")
	}

	if review.UserID != userID {
		return utils.Unauthorized(c, "you can only delete your own reviews")
	}

	db.DB.Where("target_id = ? AND target_type = ?", reviewID, "review").Delete(&models.Activity{})

	if err := db.DB.Delete(&review).Error; err != nil {
		return utils.InternalError(c, "failed to delete review")
	}

	return c.SendStatus(fiber.StatusNoContent)
}

// GetGameReviews godoc
// @Summary Get Game Reviews
// @Description Get all reviews for a specific game by IGDB ID
// @Tags reviews
// @Accept json
// @Produce json
// @Param igdb_id path int true "IGDB ID"
// @Param page query int false "Page number (default 1)"
// @Param page_size query int false "Page size (default 20, max 100)"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /reviews/game/{igdb_id} [get]
func (h *ReviewsHandler) GetGameReviews(c *fiber.Ctx) error {
	igdbID, err := c.ParamsInt("igdb_id")
	if err != nil {
		return utils.BadRequest(c, "invalid igdb_id")
	}

	page, pageSize := utils.ParsePagination(c)

	var game models.Game
	if err := db.DB.Where("igdb_id = ?", igdbID).First(&game).Error; err != nil {
		return c.JSON(fiber.Map{"data": []models.Review{}, "meta": utils.NewPaginationMeta(0, page, pageSize)})
	}

	var total int64
	db.DB.Model(&models.Review{}).Where("game_id = ?", game.ID).Count(&total)

	var reviews []models.Review
	if err := db.DB.Preload("User").Preload("Game").
		Where("game_id = ?", game.ID).
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Order("created_at desc").
		Find(&reviews).Error; err != nil {
		return utils.InternalError(c, "failed to fetch reviews")
	}

	return c.JSON(fiber.Map{"data": reviews, "meta": utils.NewPaginationMeta(int(total), page, pageSize)})
}

// GetUserReviews godoc
// @Summary Get User Reviews
// @Description Get all reviews made by a specific user
// @Tags reviews
// @Accept json
// @Produce json
// @Param user_id path string true "User UUID"
// @Param page query int false "Page number (default 1)"
// @Param page_size query int false "Page size (default 20, max 100)"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /reviews/user/{user_id} [get]
func (h *ReviewsHandler) GetUserReviews(c *fiber.Ctx) error {
	userID, err := uuid.Parse(c.Params("user_id"))
	if err != nil {
		return utils.BadRequest(c, "invalid user_id")
	}

	page, pageSize := utils.ParsePagination(c)

	var total int64
	db.DB.Model(&models.Review{}).Where("user_id = ?", userID).Count(&total)

	var reviews []models.Review
	if err := db.DB.Preload("User").Preload("Game").
		Where("user_id = ?", userID).
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Order("created_at desc").
		Find(&reviews).Error; err != nil {
		return utils.InternalError(c, "failed to fetch user reviews")
	}

	return c.JSON(fiber.Map{"data": reviews, "meta": utils.NewPaginationMeta(int(total), page, pageSize)})
}
