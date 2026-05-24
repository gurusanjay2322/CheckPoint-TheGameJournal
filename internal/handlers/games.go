package handlers

import (
	"encoding/json"
	"fmt"

	"github.com/checkpoint/server/pkg/igdb"
	"github.com/gofiber/fiber/v2"
)

type GamesHandler struct {
	IGDBClient *igdb.Client
}

func NewGamesHandler(igdbClient *igdb.Client) *GamesHandler {
	return &GamesHandler{
		IGDBClient: igdbClient,
	}
}

// Search godoc
// @Summary Search Games
// @Description Search for games on IGDB
// @Tags games
// @Accept json
// @Produce json
// @Param q query string true "Search query"
// @Success 200 {array} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /games/search [get]
func (h *GamesHandler) Search(c *fiber.Ctx) error {
	query := c.Query("q")
	if query == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "search query 'q' is required"})
	}

	igdbQuery := fmt.Sprintf(`search "%s"; fields id,name,cover.image_id,first_release_date,summary; limit 20;`, query)
	return h.fetchAndFormatGames(c, igdbQuery)
}

// Discover godoc
// @Summary Discover Games
// @Description Fetch popular mainstream games (All time)
// @Tags games
// @Accept json
// @Produce json
// @Param limit query int false "Limit (default 20)"
// @Param offset query int false "Offset (default 0)"
// @Success 200 {array} map[string]interface{}
// @Failure 500 {object} map[string]string
// @Router /games/discover [get]
func (h *GamesHandler) Discover(c *fiber.Ctx) error {
	limit := c.QueryInt("limit", 20)
	offset := c.QueryInt("offset", 0)

	igdbQuery := fmt.Sprintf(`fields id,name,cover.image_id,first_release_date,summary; where total_rating_count > 1000; sort total_rating_count desc; limit %d; offset %d;`, limit, offset)
	return h.fetchAndFormatGames(c, igdbQuery)
}

// Trending godoc
// @Summary Trending Games
// @Description Fetch currently trending games
// @Tags games
// @Accept json
// @Produce json
// @Param limit query int false "Limit (default 20)"
// @Param offset query int false "Offset (default 0)"
// @Success 200 {array} map[string]interface{}
// @Failure 500 {object} map[string]string
// @Router /games/trending [get]
func (h *GamesHandler) Trending(c *fiber.Ctx) error {
	limit := c.QueryInt("limit", 20)
	offset := c.QueryInt("offset", 0)

	// Sort by hypes for trending
	igdbQuery := fmt.Sprintf(`fields id,name,cover.image_id,first_release_date,summary; where hypes > 0; sort hypes desc; limit %d; offset %d;`, limit, offset)
	return h.fetchAndFormatGames(c, igdbQuery)
}

// NewReleases godoc
// @Summary New Releases
// @Description Fetch newly released games
// @Tags games
// @Accept json
// @Produce json
// @Param limit query int false "Limit (default 20)"
// @Param offset query int false "Offset (default 0)"
// @Success 200 {array} map[string]interface{}
// @Failure 500 {object} map[string]string
// @Router /games/new-releases [get]
func (h *GamesHandler) NewReleases(c *fiber.Ctx) error {
	limit := c.QueryInt("limit", 20)
	offset := c.QueryInt("offset", 0)

	// Sort by first_release_date for newest games that are out now (we'll just use recent past)
	// Alternatively, just sort by first_release_date desc where release is in the past
	igdbQuery := fmt.Sprintf(`fields id,name,cover.image_id,first_release_date,summary; where first_release_date != null & category = 0; sort first_release_date desc; limit %d; offset %d;`, limit, offset)
	return h.fetchAndFormatGames(c, igdbQuery)
}

func (h *GamesHandler) fetchAndFormatGames(c *fiber.Ctx, igdbQuery string) error {
	respBytes, err := h.IGDBClient.PostRequest("games", igdbQuery)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to fetch games from igdb", "details": err.Error()})
	}

	var results []map[string]interface{}
	if err := json.Unmarshal(respBytes, &results); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to parse igdb response"})
	}

	for i := range results {
		if coverObj, ok := results[i]["cover"].(map[string]interface{}); ok {
			if imageID, exists := coverObj["image_id"].(string); exists {
				results[i]["cover_url"] = fmt.Sprintf("https://images.igdb.com/igdb/image/upload/t_cover_big/%s.jpg", imageID)
			}
		}
	}

	return c.JSON(results)
}

// GetGameByID godoc
// @Summary Get Game by ID
// @Description Fetch detailed information for a specific game by its IGDB ID
// @Tags games
// @Accept json
// @Produce json
// @Param id path int true "IGDB Game ID"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /games/{id} [get]
func (h *GamesHandler) GetGameByID(c *fiber.Ctx) error {
	id := c.Params("id")
	if id == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "game ID is required"})
	}

	igdbQuery := fmt.Sprintf(`fields id,name,cover.image_id,first_release_date,summary,storyline,rating,genres.name,platforms.name,screenshots.image_id; where id = %s; limit 1;`, id)
	
	respBytes, err := h.IGDBClient.PostRequest("games", igdbQuery)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to fetch game from igdb", "details": err.Error()})
	}

	var results []map[string]interface{}
	if err := json.Unmarshal(respBytes, &results); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to parse igdb response"})
	}

	if len(results) == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "game not found"})
	}

	game := results[0]

	// Format cover URL
	if coverObj, ok := game["cover"].(map[string]interface{}); ok {
		if imageID, exists := coverObj["image_id"].(string); exists {
			game["cover_url"] = fmt.Sprintf("https://images.igdb.com/igdb/image/upload/t_cover_big/%s.jpg", imageID)
		}
	}

	// Format screenshot URLs
	if screenshots, ok := game["screenshots"].([]interface{}); ok {
		var screenshotURLs []string
		for _, s := range screenshots {
			if sObj, ok := s.(map[string]interface{}); ok {
				if imageID, exists := sObj["image_id"].(string); exists {
					screenshotURLs = append(screenshotURLs, fmt.Sprintf("https://images.igdb.com/igdb/image/upload/t_1080p/%s.jpg", imageID))
				}
			}
		}
		game["screenshot_urls"] = screenshotURLs
	}

	return c.JSON(game)
}
