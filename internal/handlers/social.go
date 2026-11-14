package handlers

import (
	"github.com/checkpoint/server/internal/db"
	"github.com/checkpoint/server/internal/models"
	"github.com/checkpoint/server/internal/utils"
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
		return utils.Unauthorized(c, "unauthorized")
	}
	followerID, _ := uuid.Parse(followerIDStr)

	followingID, err := uuid.Parse(c.Query("following_id"))
	if err != nil {
		return utils.BadRequest(c, "invalid following_id")
	}

	if followerID == followingID {
		return utils.BadRequest(c, "cannot follow yourself")
	}

	follow := models.Follow{
		FollowerID:  followerID,
		FollowingID: followingID,
	}

	if err := db.DB.FirstOrCreate(&follow, follow).Error; err != nil {
		return utils.InternalError(c, "failed to follow user")
	}

	return c.JSON(fiber.Map{"message": "successfully followed user"})
}

// UnfollowUser godoc
// @Summary Unfollow User
// @Description Unfollow a user you currently follow
// @Tags social
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param following_id query string true "User UUID to unfollow"
// @Success 204 {object} nil
// @Failure 400 {object} map[string]string
// @Failure 401 {object} map[string]string
// @Router /social/follow [delete]
func (h *SocialHandler) UnfollowUser(c *fiber.Ctx) error {
	followerIDStr, ok := c.Locals("user_id").(string)
	if !ok {
		return utils.Unauthorized(c, "unauthorized")
	}
	followerID, _ := uuid.Parse(followerIDStr)

	followingID, err := uuid.Parse(c.Query("following_id"))
	if err != nil {
		return utils.BadRequest(c, "invalid following_id")
	}

	result := db.DB.Where("follower_id = ? AND following_id = ?", followerID, followingID).Delete(&models.Follow{})
	if result.RowsAffected == 0 {
		return utils.NotFound(c, "follow relationship not found")
	}

	return c.SendStatus(fiber.StatusNoContent)
}

// GetFeed godoc
// @Summary Get Activity Feed
// @Description Get activity feed of followed users
// @Tags social
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param page query int false "Page number (default 1)"
// @Param page_size query int false "Page size (default 20, max 100)"
// @Success 200 {object} map[string]interface{}
// @Failure 401 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /social/feed [get]
func (h *SocialHandler) GetFeed(c *fiber.Ctx) error {
	userIDStr, ok := c.Locals("user_id").(string)
	if !ok {
		return utils.Unauthorized(c, "unauthorized")
	}

	page, pageSize := utils.ParsePagination(c)

	var follows []models.Follow
	db.DB.Where("follower_id = ?", userIDStr).Find(&follows)

	var followingIDs []uuid.UUID
	for _, f := range follows {
		followingIDs = append(followingIDs, f.FollowingID)
	}

	activities := []models.Activity{}
	var total int64

	if len(followingIDs) > 0 {
		baseQuery := db.DB.Preload("User").Where("user_id IN ?", followingIDs)

		baseQuery.Model(&models.Activity{}).Count(&total)

		baseQuery.
			Order("created_at desc").
			Offset((page - 1) * pageSize).
			Limit(pageSize).
			Find(&activities)
	}

	attachActivityTargets(activities)
	return c.JSON(fiber.Map{"data": activities, "meta": utils.NewPaginationMeta(int(total), page, pageSize)})
}

// attachActivityTargets loads each activity's game (and review, for review
// activities) in two queries.
func attachActivityTargets(activities []models.Activity) {
	var reviewIDs, gameIDs []uuid.UUID
	for _, a := range activities {
		if a.TargetType == "review" {
			reviewIDs = append(reviewIDs, a.TargetID)
		} else {
			gameIDs = append(gameIDs, a.TargetID)
		}
	}

	reviews := map[uuid.UUID]models.Review{}
	if len(reviewIDs) > 0 {
		var rows []models.Review
		db.DB.Preload("Game").Where("id IN ?", reviewIDs).Find(&rows)
		for _, r := range rows {
			reviews[r.ID] = r
		}
	}
	games := map[uuid.UUID]models.Game{}
	if len(gameIDs) > 0 {
		var rows []models.Game
		db.DB.Where("id IN ?", gameIDs).Find(&rows)
		for _, g := range rows {
			games[g.ID] = g
		}
	}

	for i := range activities {
		a := &activities[i]
		if r, ok := reviews[a.TargetID]; ok && a.TargetType == "review" {
			game := r.Game
			a.Game = &game
			a.Review = &models.ActivityReview{Rating: r.Rating, Content: r.Content, ContainsSpoiler: r.ContainsSpoiler}
		} else if g, ok := games[a.TargetID]; ok {
			a.Game = &g
		}
	}
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
	userID, err := uuid.Parse(c.Params("user_id"))
	if err != nil {
		return utils.BadRequest(c, "invalid user id")
	}

	var user models.User
	if err := db.DB.First(&user, userID).Error; err != nil {
		return utils.NotFound(c, "user not found")
	}

	var gameCount int64
	db.DB.Model(&models.UserGame{}).Where("user_id = ?", userID).Count(&gameCount)

	var reviewCount int64
	db.DB.Model(&models.Review{}).Where("user_id = ?", userID).Count(&reviewCount)

	return c.JSON(fiber.Map{
		"user":          user,
		"total_games":   gameCount,
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
// @Param page query int false "Page number (default 1)"
// @Param page_size query int false "Page size (default 20, max 100)"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /social/activity/{user_id} [get]
func (h *SocialHandler) GetUserActivity(c *fiber.Ctx) error {
	userID, err := uuid.Parse(c.Params("user_id"))
	if err != nil {
		return utils.BadRequest(c, "invalid user id")
	}

	page, pageSize := utils.ParsePagination(c)

	var total int64
	db.DB.Model(&models.Activity{}).Where("user_id = ?", userID).Count(&total)

	var activities []models.Activity
	if err := db.DB.Preload("User").
		Where("user_id = ?", userID).
		Order("created_at desc").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&activities).Error; err != nil {
		return utils.InternalError(c, "failed to fetch user activity")
	}

	attachActivityTargets(activities)
	return c.JSON(fiber.Map{"data": activities, "meta": utils.NewPaginationMeta(int(total), page, pageSize)})
}

// SearchUsers godoc
// @Summary Search Users
// @Description Search users by username
// @Tags social
// @Accept json
// @Produce json
// @Param q query string true "Search query"
// @Param page query int false "Page number (default 1)"
// @Param page_size query int false "Page size (default 20, max 100)"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Router /users/search [get]
func (h *SocialHandler) SearchUsers(c *fiber.Ctx) error {
	query := c.Query("q", "")
	if query == "" {
		return utils.BadRequest(c, "search query 'q' is required")
	}

	page, pageSize := utils.ParsePagination(c)

	var total int64
	db.DB.Model(&models.User{}).Where("username ILIKE ?", "%"+query+"%").Count(&total)

	var users []models.User
	if err := db.DB.Select("id, username, avatar_url, bio, created_at").
		Where("username ILIKE ?", "%"+query+"%").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&users).Error; err != nil {
		return utils.InternalError(c, "failed to search users")
	}

	return c.JSON(fiber.Map{"data": users, "meta": utils.NewPaginationMeta(int(total), page, pageSize)})
}
