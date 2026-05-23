package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/checkpoint/server/internal/db"
	"github.com/checkpoint/server/internal/models"
	"github.com/checkpoint/server/pkg/steam"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

const (
	TypeSyncSteamLibrary = "sync:steam_library"
)

type SyncSteamLibraryPayload struct {
	UserID  string `json:"user_id"`
	SteamID string `json:"steam_id"`
}

func NewSyncSteamLibraryTask(userID, steamID string) (*asynq.Task, error) {
	payload, err := json.Marshal(SyncSteamLibraryPayload{UserID: userID, SteamID: steamID})
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(TypeSyncSteamLibrary, payload), nil
}

func HandleSyncSteamLibraryTask(steamClient *steam.Client, asynqClient *asynq.Client) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, t *asynq.Task) error {
		var p SyncSteamLibraryPayload
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			return fmt.Errorf("json.Unmarshal failed: %v: %w", err, asynq.SkipRetry)
		}

		log.Printf("Syncing Steam library for User %s (SteamID: %s)", p.UserID, p.SteamID)

		games, err := steamClient.GetOwnedGames(p.SteamID)
		if err != nil {
			return fmt.Errorf("failed to fetch games from steam: %v", err)
		}

		userUUID, _ := uuid.Parse(p.UserID)

		for _, g := range games {
			var game models.Game
			result := db.DB.Where("steam_app_id = ?", g.AppID).First(&game)
			
			if result.Error != nil {
				game = models.Game{
					SteamAppID: g.AppID,
					Title:      g.Name,
				}
				db.DB.Create(&game)
			}

			var userGame models.UserGame
			db.DB.Where("user_id = ? AND game_id = ?", userUUID, game.ID).FirstOrCreate(&userGame, models.UserGame{
				UserID:          userUUID,
				GameID:          game.ID,
				Status:          "backlog",
				PlaytimeMinutes: g.PlaytimeForever,
			})

			if userGame.PlaytimeMinutes != g.PlaytimeForever {
				userGame.PlaytimeMinutes = g.PlaytimeForever
				db.DB.Save(&userGame)
			}
		}

		log.Printf("Successfully synced %d games for User %s", len(games), p.UserID)

		// Queue enrichment task to fetch IGDB metadata for the new games
		enrichTask, err := NewEnrichSteamGamesTask()
		if err == nil {
			asynqClient.Enqueue(enrichTask)
		}

		return nil
	}
}
