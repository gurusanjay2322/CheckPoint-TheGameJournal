package handlers

import (
	"github.com/checkpoint/server/internal/db"
	"github.com/checkpoint/server/internal/models"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

type SocialHandler struct{}

func NewSocialHandler() *SocialHandler {
	return &SocialHandler{}
}

// FollowUser godoc
// @Summary Follow User
// @Description Follow another user
// @Tags social
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param following_id query string true "User UUID to follow"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]string
// @Failure 401 {object} map[string]string
// @Router /social/follow [post]
func (h *SocialHandler) FollowUser(c *fiber.Ctx) error {
	followerIDStr, ok := c.Locals("user_id").(string)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
	}
	followerID, _ := uuid.Parse(followerIDStr)

	followingIDStr := c.Query("following_id")
	followingID, err := uuid.Parse(followingIDStr)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid following_id"})
	}

	if followerID == followingID {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "cannot follow yourself"})
	}

	follow := models.Follow{
		FollowerID:  followerID,
		FollowingID: followingID,
	}

	if err := db.DB.FirstOrCreate(&follow, follow).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to follow user"})
	}

	return c.JSON(fiber.Map{"message": "successfully followed user"})
}

// GetFeed godoc
// @Summary Get Activity Feed
// @Description Get activity feed of followed users
// @Tags social
// @Security BearerAuth
// @Accept json
// @Produce json
// @Success 200 {array} models.Activity
// @Failure 401 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /social/feed [get]
func (h *SocialHandler) GetFeed(c *fiber.Ctx) error {
	userIDStr, ok := c.Locals("user_id").(string)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
	}

	var follows []models.Follow
	db.DB.Where("follower_id = ?", userIDStr).Find(&follows)

	var followingIDs []uuid.UUID
	for _, f := range follows {
		followingIDs = append(followingIDs, f.FollowingID)
	}

	var activities []models.Activity
	if len(followingIDs) > 0 {
		db.DB.Preload("User").
			Where("user_id IN ?", followingIDs).
			Order("created_at desc").
			Limit(50).
			Find(&activities)
	}

	return c.JSON(activities)
}

// GetProfile godoc
// @Summary Get User Profile
// @Description Fetch public user profile and basic stats
// @Tags social
// @Accept json
// @Produce json
// @Param user_id path string true "User UUID"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Router /social/profile/{user_id} [get]
func (h *SocialHandler) GetProfile(c *fiber.Ctx) error {
	userIDStr := c.Params("user_id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid user id"})
	}

	var user models.User
	if err := db.DB.First(&user, userID).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "user not found"})
	}

	var gameCount int64
	db.DB.Model(&models.UserGame{}).Where("user_id = ?", userID).Count(&gameCount)

	var reviewCount int64
	db.DB.Model(&models.Review{}).Where("user_id = ?", userID).Count(&reviewCount)

	return c.JSON(fiber.Map{
		"user":         user,
		"total_games":  gameCount,
		"total_reviews": reviewCount,
	})
}

// GetUserActivity godoc
// @Summary Get User Activity
// @Description Get the activity feed for a specific user
// @Tags social
// @Accept json
// @Produce json
// @Param user_id path string true "User UUID"
// @Success 200 {array} models.Activity
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /social/activity/{user_id} [get]
func (h *SocialHandler) GetUserActivity(c *fiber.Ctx) error {
	userIDStr := c.Params("user_id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid user id"})
	}

	var activities []models.Activity
	if err := db.DB.Preload("User").
		Where("user_id = ?", userID).
		Order("created_at desc").
		Limit(50).
		Find(&activities).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to fetch user activity"})
	}

	return c.JSON(activities)
}
