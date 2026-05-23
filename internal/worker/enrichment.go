package worker

import (
	"context"
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
)

func NewEnrichSteamGamesTask() (*asynq.Task, error) {
	return asynq.NewTask(TypeEnrichSteamGames, nil), nil
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

func normalizeTitle(s string) string {
	var sb strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			sb.WriteRune(unicode.ToLower(r))
		}
	}
	return sb.String()
}
