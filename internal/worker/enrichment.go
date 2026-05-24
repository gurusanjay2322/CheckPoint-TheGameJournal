package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/checkpoint/server/internal/db"
	"github.com/checkpoint/server/internal/models"
	"github.com/checkpoint/server/pkg/igdb"
	"github.com/hibiken/asynq"
)

const (
	TypeEnrichSteamGames = "enrich:steam_games"
	TypeEnrichIGDBGame   = "enrich:igdb_game"
)

func NewEnrichSteamGamesTask() (*asynq.Task, error) {
	return asynq.NewTask(TypeEnrichSteamGames, nil), nil
}

type EnrichIGDBGamePayload struct {
	IGDBID int `json:"igdb_id"`
}

func NewEnrichIGDBGameTask(igdbID int) (*asynq.Task, error) {
	payload, err := json.Marshal(EnrichIGDBGamePayload{IGDBID: igdbID})
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(TypeEnrichIGDBGame, payload), nil
}

func HandleEnrichSteamGamesTask(igdbClient *igdb.Client) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, t *asynq.Task) error {
		log.Println("Starting background enrichment for Steam games...")

		// Find games that have a steam_app_id but no igdb_id
		var games []models.Game
		if err := db.DB.Where("steam_app_id IS NOT NULL AND igdb_id IS NULL").Find(&games).Error; err != nil {
			return fmt.Errorf("failed to fetch games to enrich: %v", err)
		}

		if len(games) == 0 {
			log.Println("No games need enrichment.")
			return nil
		}

		log.Printf("Found %d games to enrich. Processing in chunks...", len(games))

		// Process in chunks of 50
		chunkSize := 50
		for i := 0; i < len(games); i += chunkSize {
			end := i + chunkSize
			if end > len(games) {
				end = len(games)
			}

			chunk := games[i:end]
			var steamIDs []int
			gameMap := make(map[int]*models.Game)

			for j := range chunk {
				steamIDs = append(steamIDs, chunk[j].SteamAppID)
				gameMap[chunk[j].SteamAppID] = &chunk[j]
			}

			igdbGames, err := igdbClient.GetGamesBySteamIDs(steamIDs)
			if err != nil {
				log.Printf("Failed to fetch chunk from IGDB: %v", err)
				continue
			}

			for _, igdbGame := range igdbGames {
				// We can't rely on ext.Category == 1 because IGDB's API is missing category data for many games.
				// Instead, we check if the UID matches AND the titles are somewhat similar to avoid collisions.
				var matchedSteamID int
				for _, ext := range igdbGame.ExternalGames {
					parsedID, err := strconv.Atoi(ext.UID)
					if err != nil {
						continue
					}

					localGame, exists := gameMap[parsedID]
					if exists {
						// Disambiguate collisions (e.g. UID "730" on Twitch vs Steam) by checking if titles are reasonably similar
						igdbTitle := normalizeTitle(igdbGame.Name)
						localTitle := normalizeTitle(localGame.Title)

						if strings.Contains(igdbTitle, localTitle) || strings.Contains(localTitle, igdbTitle) || igdbTitle == localTitle {
							matchedSteamID = parsedID
							break
						}
					}
				}

				if matchedSteamID == 0 {
					continue
				}

				// Update the local game record
				if localGame, exists := gameMap[matchedSteamID]; exists {
					localGame.IGDBID = &igdbGame.ID
					localGame.Summary = igdbGame.Summary

					if igdbGame.FirstReleaseDate > 0 {
						t := time.Unix(igdbGame.FirstReleaseDate, 0)
						localGame.ReleaseDate = &t
					}

					if igdbGame.Cover.ImageID != "" {
						localGame.CoverURL = fmt.Sprintf("https://images.igdb.com/igdb/image/upload/t_cover_big/%s.jpg", igdbGame.Cover.ImageID)
					}

					db.DB.Save(localGame)
				}
			}

			log.Printf("Processed chunk %d to %d...", i, end)
		}

		log.Println("Enrichment task completed successfully.")
		return nil
	}
}

func HandleEnrichIGDBGameTask(igdbClient *igdb.Client) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, t *asynq.Task) error {
		var p EnrichIGDBGamePayload
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			return fmt.Errorf("json.Unmarshal failed: %v: %w", err, asynq.SkipRetry)
		}

		log.Printf("Starting IGDB enrichment for game ID: %d", p.IGDBID)

		var localGame models.Game
		if err := db.DB.Where("igdb_id = ?", p.IGDBID).First(&localGame).Error; err != nil {
			return fmt.Errorf("local game not found for IGDB ID %d: %v", p.IGDBID, err)
		}

		igdbQuery := fmt.Sprintf(`fields id,name,cover.image_id,first_release_date,summary; where id = %d; limit 1;`, p.IGDBID)
		respBytes, err := igdbClient.PostRequest("games", igdbQuery)
		if err != nil {
			return fmt.Errorf("failed to fetch game %d from IGDB: %v", p.IGDBID, err)
		}

		var results []map[string]interface{}
		if err := json.Unmarshal(respBytes, &results); err != nil {
			return fmt.Errorf("failed to parse IGDB response for %d: %v", p.IGDBID, err)
		}

		if len(results) == 0 {
			log.Printf("IGDB returned no results for game ID: %d", p.IGDBID)
			return nil // The ID might be invalid, don't retry
		}

		gameData := results[0]

		// Overwrite the user-provided title with the official IGDB title
		if name, ok := gameData["name"].(string); ok {
			localGame.Title = name
		}

		if summary, ok := gameData["summary"].(string); ok {
			localGame.Summary = summary
		}

		if frd, ok := gameData["first_release_date"].(float64); ok {
			t := time.Unix(int64(frd), 0)
			localGame.ReleaseDate = &t
		}

		if coverObj, ok := gameData["cover"].(map[string]interface{}); ok {
			if imageID, exists := coverObj["image_id"].(string); exists {
				localGame.CoverURL = fmt.Sprintf("https://images.igdb.com/igdb/image/upload/t_cover_big/%s.jpg", imageID)
			}
		}

		if err := db.DB.Save(&localGame).Error; err != nil {
			return fmt.Errorf("failed to save enriched local game %d: %v", p.IGDBID, err)
		}

		log.Printf("Successfully enriched and overwritten local game: %s (IGDB ID: %d)", localGame.Title, p.IGDBID)
		return nil
	}
}

func normalizeTitle(s string) string {
	var sb strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			sb.WriteRune(unicode.ToLower(r))
		}
	}
	return sb.String()
}
