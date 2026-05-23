package handlers

import (
	"github.com/checkpoint/server/internal/db"
	"github.com/checkpoint/server/internal/models"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

type ReviewsHandler struct{}

func NewReviewsHandler() *ReviewsHandler {
	return &ReviewsHandler{}
}

type CreateReviewRequest struct {
	IGDBID          int    `json:"igdb_id"`
	Title           string `json:"title"`
	Rating          int    `json:"rating"` // 1-10
	Content         string `json:"content"`
	ContainsSpoiler bool   `json:"contains_spoiler"`
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
// @Failure 500 {object} map[string]string
// @Router /reviews [post]
func (h *ReviewsHandler) CreateReview(c *fiber.Ctx) error {
	userIDStr, ok := c.Locals("user_id").(string)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
	}
	userID, _ := uuid.Parse(userIDStr)

	var req CreateReviewRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	if req.Rating < 1 || req.Rating > 10 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "rating must be between 1 and 10"})
	}

	// Ensure game exists locally
	var game models.Game
	result := db.DB.Where("igdb_id = ?", req.IGDBID).First(&game)
	if result.Error != nil {
		game = models.Game{
			IGDBID: &req.IGDBID,
			Title:  req.Title,
		}
		if err := db.DB.Create(&game).Error; err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to create game record"})
		}
	}

	// Create review
	review := models.Review{
		UserID:          userID,
		GameID:          game.ID,
		Rating:          req.Rating,
		Content:         req.Content,
		ContainsSpoiler: req.ContainsSpoiler,
	}

	if err := db.DB.Create(&review).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to create review"})
	}

	// Create activity
	activity := models.Activity{
		UserID:     userID,
		ActionType: "reviewed",
		TargetID:   review.ID,
		TargetType: "review",
	}
	db.DB.Create(&activity)

	return c.JSON(review)
}

// GetGameReviews godoc
// @Summary Get Game Reviews
// @Description Get all reviews for a specific game by IGDB ID
// @Tags reviews
// @Accept json
// @Produce json
// @Param igdb_id path int true "IGDB ID"
// @Success 200 {array} models.Review
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /reviews/game/{igdb_id} [get]
func (h *ReviewsHandler) GetGameReviews(c *fiber.Ctx) error {
	igdbID, err := c.ParamsInt("igdb_id")
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid igdb_id"})
	}

	var game models.Game
	if err := db.DB.Where("igdb_id = ?", igdbID).First(&game).Error; err != nil {
		return c.JSON([]models.Review{}) // Return empty if game isn't tracked yet
	}

	var reviews []models.Review
	if err := db.DB.Preload("User").Where("game_id = ?", game.ID).Order("created_at desc").Find(&reviews).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to fetch reviews"})
	}

	return c.JSON(reviews)
}
