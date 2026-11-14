package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/checkpoint/server/internal/catalog"
	"github.com/checkpoint/server/internal/db"
	"github.com/checkpoint/server/internal/models"
	"github.com/checkpoint/server/pkg/igdb"
	"github.com/hibiken/asynq"
	"gorm.io/gorm"
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

// applyMetadata copies IGDB details onto a local game. The title is only
// replaced when overwriteTitle is set, because Steam titles are already clean.
func applyMetadata(g *models.Game, src *igdb.IGDBGame, overwriteTitle bool) {
	if overwriteTitle && src.Name != "" {
		g.Title = src.Name
	}
	if src.Summary != "" {
		g.Summary = src.Summary
	}
	if src.FirstReleaseDate > 0 {
		t := time.Unix(src.FirstReleaseDate, 0).UTC()
		g.ReleaseDate = &t
	}
	if cover := src.CoverURL(); cover != "" {
		g.CoverURL = cover
	}
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

		chunkSize := 50
		for i := 0; i < len(games); i += chunkSize {
			end := min(i+chunkSize, len(games))
			chunk := games[i:end]

			steamIDs := make([]int, len(chunk))
			gameMap := make(map[int]*models.Game, len(chunk))
			for j := range chunk {
				steamIDs[j] = *chunk[j].SteamAppID
				gameMap[*chunk[j].SteamAppID] = &chunk[j]
			}

			igdbGames, err := igdbClient.GetGamesBySteamIDs(steamIDs)
			if err != nil {
				log.Printf("Failed to fetch chunk from IGDB: %v", err)
				continue
			}

			for k := range igdbGames {
				igdbGame := &igdbGames[k]

				// IGDB's category data is incomplete, so match on UID and
				// confirm with the title to avoid cross-platform UID collisions.
				var localGame *models.Game
				for _, ext := range igdbGame.ExternalGames {
					parsedID, err := strconv.Atoi(ext.UID)
					if err != nil {
						continue
					}
					if candidate, ok := gameMap[parsedID]; ok && catalog.TitlesMatch(igdbGame.Name, candidate.Title) {
						localGame = candidate
						break
					}
				}
				if localGame == nil || localGame.IGDBID != nil {
					continue
				}

				err := catalog.LinkSteamGameToIGDB(db.DB, localGame, igdbGame.ID, func(g *models.Game) {
					applyMetadata(g, igdbGame, false)
				})
				if err != nil {
					log.Printf("Failed to link steam app %d to IGDB %d: %v", *localGame.SteamAppID, igdbGame.ID, err)
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

		igdbGame, err := igdbClient.GetGame(p.IGDBID)
		if err != nil {
			return fmt.Errorf("failed to fetch game %d from IGDB: %v", p.IGDBID, err)
		}
		if igdbGame == nil {
			log.Printf("IGDB returned no results for game ID: %d", p.IGDBID)
			return nil // The ID might be invalid, don't retry
		}

		// Overwrite the user-provided title with the official IGDB title
		applyMetadata(&localGame, igdbGame, true)

		var steamAppIDs []int
		for _, ext := range igdbGame.ExternalGames {
			if id, err := strconv.Atoi(ext.UID); err == nil {
				steamAppIDs = append(steamAppIDs, id)
			}
		}

		err = db.DB.Transaction(func(tx *gorm.DB) error {
			if len(steamAppIDs) > 0 {
				var steamOnly []models.Game
				if err := tx.Where("steam_app_id IN ? AND igdb_id IS NULL", steamAppIDs).Find(&steamOnly).Error; err != nil {
					return err
				}
				for _, dup := range steamOnly {
					if !catalog.TitlesMatch(igdbGame.Name, dup.Title) {
						continue
					}
					log.Printf("Merging steam app %d into IGDB game %d", *dup.SteamAppID, p.IGDBID)
					if localGame.SteamAppID == nil {
						localGame.SteamAppID = dup.SteamAppID
					}
					if err := catalog.Merge(tx, localGame.ID, dup.ID); err != nil {
						return err
					}
				}
			}
			return tx.Save(&localGame).Error
		})
		if err != nil {
			return fmt.Errorf("failed to save enriched local game %d: %v", p.IGDBID, err)
		}

		log.Printf("Successfully enriched and overwritten local game: %s (IGDB ID: %d)", localGame.Title, p.IGDBID)
		return nil
	}
}
