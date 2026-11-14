package handlers

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/checkpoint/server/internal/utils"
	"github.com/checkpoint/server/pkg/igdb"
	"github.com/gofiber/fiber/v2"
)

const newReleasesWindow = 90 * 24 * 60 * 60 // seconds

// listFields is what game cards need: cover, year, rating, genres, and one
// landscape image (artwork, else screenshot) for featured layouts.
const listFields = "id,name,cover.image_id,first_release_date,summary,rating,genres.name,artworks.image_id,screenshots.image_id"

const igdbImageBase = "https://images.igdb.com/igdb/image/upload"

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
// @Param limit query int false "Limit (default 20)"
// @Param offset query int false "Offset (default 0)"
// @Success 200 {array} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /games/search [get]
func (h *GamesHandler) Search(c *fiber.Ctx) error {
	query := c.Query("q")
	if query == "" {
		return utils.BadRequest(c, "search query 'q' is required")
	}

	limit, offset := parseLimitOffset(c)

	igdbQuery := fmt.Sprintf(`search %s; fields %s; limit %d; offset %d;`, igdb.QuoteString(query), listFields, limit, offset)
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
	limit, offset := parseLimitOffset(c)

	igdbQuery := fmt.Sprintf(`fields %s; where total_rating_count > 1000; sort total_rating_count desc; limit %d; offset %d;`, listFields, limit, offset)
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
	limit, offset := parseLimitOffset(c)

	// Sort by hypes for trending
	igdbQuery := fmt.Sprintf(`fields %s; where hypes > 0; sort hypes desc; limit %d; offset %d;`, listFields, limit, offset)
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
	limit, offset := parseLimitOffset(c)

	// Released in the last 90 days, newest first. game_type 0/8/9 = main game,
	// remake, remaster (IGDB replaced `category` with `game_type`); the hype or
	// rating floor drops the flood of obscure daily releases.
	now := time.Now().Unix()
	igdbQuery := fmt.Sprintf(`fields %s; where first_release_date < %d & first_release_date > %d & game_type = (0,8,9) & cover != null & (hypes >= 5 | total_rating_count >= 5); sort first_release_date desc; limit %d; offset %d;`, listFields, now, now-newReleasesWindow, limit, offset)
	return h.fetchAndFormatGames(c, igdbQuery)
}

// parseLimitOffset clamps paging so callers can't request huge IGDB pages.
func parseLimitOffset(c *fiber.Ctx) (limit, offset int) {
	limit = min(max(c.QueryInt("limit", 20), 1), 50)
	offset = min(max(c.QueryInt("offset", 0), 0), 5000)
	return limit, offset
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

	for _, game := range results {
		setCoverURL(game)
		setHeroURL(game)
		// Only the first image is used; drop the rest to keep pages small.
		delete(game, "artworks")
		delete(game, "screenshots")
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
	id, err := c.ParamsInt("id")
	if err != nil || id <= 0 {
		return utils.BadRequest(c, "game ID must be a positive integer")
	}

	igdbQuery := fmt.Sprintf(`fields id,name,cover.image_id,first_release_date,summary,storyline,rating,genres.name,platforms.name,screenshots.image_id,artworks.image_id,involved_companies.developer,involved_companies.company.name; where id = %d; limit 1;`, id)

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

	setCoverURL(game)
	setHeroURL(game)

	var screenshotURLs []string
	for _, imageID := range imageIDs(game["screenshots"]) {
		screenshotURLs = append(screenshotURLs, fmt.Sprintf("%s/t_1080p/%s.jpg", igdbImageBase, imageID))
	}
	game["screenshot_urls"] = screenshotURLs

	var developers []string
	if companies, ok := game["involved_companies"].([]interface{}); ok {
		for _, ic := range companies {
			icObj, ok := ic.(map[string]interface{})
			if !ok || icObj["developer"] != true {
				continue
			}
			if company, ok := icObj["company"].(map[string]interface{}); ok {
				if name, ok := company["name"].(string); ok {
					developers = append(developers, name)
				}
			}
		}
	}
	game["developers"] = developers
	delete(game, "involved_companies")

	return c.JSON(game)
}

func setCoverURL(game map[string]interface{}) {
	if ids := imageIDs([]interface{}{game["cover"]}); len(ids) > 0 {
		game["cover_url"] = fmt.Sprintf("%s/t_cover_big/%s.jpg", igdbImageBase, ids[0])
	}
}

// setHeroURL picks a landscape image, preferring key art over screenshots.
func setHeroURL(game map[string]interface{}) {
	ids := imageIDs(game["artworks"])
	if len(ids) == 0 {
		ids = imageIDs(game["screenshots"])
	}
	if len(ids) > 0 {
		game["hero_url"] = fmt.Sprintf("%s/t_screenshot_huge/%s.jpg", igdbImageBase, ids[0])
	}
}

// imageIDs extracts image_id values from an IGDB image list.
func imageIDs(v interface{}) []string {
	list, _ := v.([]interface{})
	var ids []string
	for _, item := range list {
		if obj, ok := item.(map[string]interface{}); ok {
			if id, ok := obj["image_id"].(string); ok && id != "" {
				ids = append(ids, id)
			}
		}
	}
	return ids
}
