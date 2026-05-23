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

	igdbQuery := fmt.Sprintf(`search "%s"; fields id,name,cover.url,first_release_date,summary; limit 20;`, query)
	
	respBytes, err := h.IGDBClient.PostRequest("games", igdbQuery)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to search igdb", "details": err.Error()})
	}

	var results []map[string]interface{}
	if err := json.Unmarshal(respBytes, &results); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to parse igdb response"})
	}

	return c.JSON(results)
}
