package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/checkpoint/server/internal/db"
	"github.com/checkpoint/server/internal/models"
	"github.com/checkpoint/server/pkg/steam"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	TypeSyncSteamLibrary = "sync:steam_library"
)

// Keeps each IN (...) list and batch insert well under Postgres' 65535
// bind-parameter limit.
const syncChunkSize = 1000

// Achievements need one Steam call per game, so only the most recently
// played games are refreshed on each sync.
const (
	achievementGamesPerSync = 40
	achievementConcurrency  = 4
)

// storeLookupChunk bounds the number of app IDs per Steam store request.
const storeLookupChunk = 100

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
		userUUID, err := uuid.Parse(p.UserID)
		if err != nil {
			return fmt.Errorf("invalid user id %q: %w", p.UserID, asynq.SkipRetry)
		}

		log.Printf("Syncing Steam library for User %s (SteamID: %s)", p.UserID, p.SteamID)

		// The wishlist needs no API key, so sync it even if owned games fail.
		ownedErr := syncOwnedGames(steamClient, userUUID, p.SteamID)
		wishlistErr := syncWishlist(steamClient, userUUID, p.SteamID)
		if wishlistErr != nil {
			log.Printf("Failed to sync Steam wishlist for User %s: %v", p.UserID, wishlistErr)
		}

		// Queue enrichment task to fetch IGDB metadata for the new games
		if enrichTask, err := NewEnrichSteamGamesTask(); err == nil {
			if _, err := asynqClient.Enqueue(enrichTask); err != nil {
				log.Printf("Failed to enqueue enrichment: %v", err)
			}
		}

		return errors.Join(ownedErr, wishlistErr)
	}
}

func syncOwnedGames(steamClient *steam.Client, userID uuid.UUID, steamID string) error {
	games, err := steamClient.GetOwnedGames(steamID)
	if err != nil {
		return fmt.Errorf("failed to fetch games from steam: %v", err)
	}

	for start := 0; start < len(games); start += syncChunkSize {
		end := min(start+syncChunkSize, len(games))
		if err := syncChunk(userID, games[start:end]); err != nil {
			return fmt.Errorf("failed to sync games %d-%d: %w", start, end, err)
		}
	}
	log.Printf("Successfully synced %d games for User %s", len(games), userID)

	syncAchievements(steamClient, userID, steamID, games)
	return nil
}

// gameIDsBySteamApp maps app IDs to local game IDs, creating rows (titled by
// titleFor) for apps not yet in the catalog. Apps titleFor can't name are skipped.
func gameIDsBySteamApp(tx *gorm.DB, appIDs []int, titleFor func(int) string) (map[int]uuid.UUID, error) {
	var existing []models.Game
	if err := tx.Where("steam_app_id IN ?", appIDs).Find(&existing).Error; err != nil {
		return nil, err
	}
	ids := make(map[int]uuid.UUID, len(appIDs))
	for _, g := range existing {
		ids[*g.SteamAppID] = g.ID
	}

	var newGames []models.Game
	for _, appID := range appIDs {
		if _, ok := ids[appID]; ok {
			continue
		}
		if title := titleFor(appID); title != "" {
			id := appID
			newGames = append(newGames, models.Game{SteamAppID: &id, Title: title})
		}
	}
	if len(newGames) > 0 {
		if err := tx.Create(&newGames).Error; err != nil {
			return nil, err
		}
		for _, g := range newGames {
			ids[*g.SteamAppID] = g.ID
		}
	}
	return ids, nil
}

// syncChunk upserts the games and the user's library entries for one batch.
// Existing entries keep their status (except wishlist, which becomes backlog
// once owned); playtime and last-played are refreshed.
func syncChunk(userID uuid.UUID, owned []steam.OwnedGame) error {
	return db.DB.Transaction(func(tx *gorm.DB) error {
		appIDs := make([]int, len(owned))
		names := make(map[int]string, len(owned))
		for i, g := range owned {
			appIDs[i] = g.AppID
			names[g.AppID] = g.Name
		}

		gameIDByApp, err := gameIDsBySteamApp(tx, appIDs, func(id int) string { return names[id] })
		if err != nil {
			return err
		}

		entries := make([]models.UserGame, 0, len(owned))
		seen := make(map[uuid.UUID]bool, len(owned))
		for _, g := range owned {
			gameID, ok := gameIDByApp[g.AppID]
			if !ok || seen[gameID] {
				continue
			}
			seen[gameID] = true
			entry := models.UserGame{
				UserID:                userID,
				GameID:                gameID,
				Status:                "backlog",
				PlaytimeMinutes:       g.PlaytimeForever,
				Playtime2WeeksMinutes: g.Playtime2Weeks,
			}
			if g.RTimeLastPlayed > 0 {
				t := time.Unix(g.RTimeLastPlayed, 0).UTC()
				entry.LastPlayedAt = &t
			}
			entries = append(entries, entry)
		}
		if len(entries) == 0 {
			return nil
		}

		return tx.Omit("User", "Game").Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "user_id"}, {Name: "game_id"}},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"playtime_minutes":        gorm.Expr("excluded.playtime_minutes"),
				"playtime_2weeks_minutes": gorm.Expr("excluded.playtime_2weeks_minutes"),
				"last_played_at":          gorm.Expr("COALESCE(excluded.last_played_at, user_games.last_played_at)"),
				"status":                  gorm.Expr("CASE WHEN user_games.status = 'wishlist' THEN 'backlog' ELSE user_games.status END"),
				"updated_at":              gorm.Expr("excluded.updated_at"),
			}),
		}).Create(&entries).Error
	})
}

// syncWishlist adds Steam wishlist games as "wishlist" entries. Games already
// in the library keep their status.
func syncWishlist(steamClient *steam.Client, userID uuid.UUID, steamID string) error {
	items, err := steamClient.GetWishlist(steamID)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return nil
	}

	for start := 0; start < len(items); start += storeLookupChunk {
		chunk := items[start:min(start+storeLookupChunk, len(items))]
		appIDs := make([]int, len(chunk))
		for i, it := range chunk {
			appIDs[i] = it.AppID
		}

		// Wishlist items only carry app IDs; fetch names for unknown games.
		var missing []int
		var known []models.Game
		if err := db.DB.Where("steam_app_id IN ?", appIDs).Find(&known).Error; err != nil {
			return err
		}
		have := make(map[int]bool, len(known))
		for _, g := range known {
			have[*g.SteamAppID] = true
		}
		for _, id := range appIDs {
			if !have[id] {
				missing = append(missing, id)
			}
		}
		storeItems, err := steamClient.GetStoreItems(missing, "US")
		if err != nil {
			return fmt.Errorf("failed to look up wishlist games: %w", err)
		}

		err = db.DB.Transaction(func(tx *gorm.DB) error {
			ids, err := gameIDsBySteamApp(tx, appIDs, func(id int) string { return storeItems[id].Name })
			if err != nil {
				return err
			}
			entries := make([]models.UserGame, 0, len(ids))
			for _, it := range chunk {
				if gameID, ok := ids[it.AppID]; ok {
					entry := models.UserGame{UserID: userID, GameID: gameID, Status: "wishlist"}
					if it.DateAdded > 0 {
						entry.CreatedAt = time.Unix(it.DateAdded, 0).UTC()
					}
					entries = append(entries, entry)
				}
			}
			if len(entries) == 0 {
				return nil
			}
			return tx.Omit("User", "Game").Clauses(clause.OnConflict{DoNothing: true}).Create(&entries).Error
		})
		if err != nil {
			return err
		}
	}

	log.Printf("Synced %d wishlist games for User %s", len(items), userID)
	return nil
}

func syncAchievements(steamClient *steam.Client, userID uuid.UUID, steamID string, owned []steam.OwnedGame) {
	played := make([]steam.OwnedGame, 0, len(owned))
	for _, g := range owned {
		if g.PlaytimeForever > 0 {
			played = append(played, g)
		}
	}
	sort.Slice(played, func(i, j int) bool { return played[i].RTimeLastPlayed > played[j].RTimeLastPlayed })
	played = played[:min(len(played), achievementGamesPerSync)]
	if len(played) == 0 {
		return
	}

	appIDs := make([]int, len(played))
	for i, g := range played {
		appIDs[i] = g.AppID
	}
	var games []models.Game
	if err := db.DB.Select("id", "steam_app_id").Where("steam_app_id IN ?", appIDs).Find(&games).Error; err != nil {
		log.Printf("Failed to load games for achievements: %v", err)
		return
	}
	gameIDByApp := make(map[int]uuid.UUID, len(games))
	for _, g := range games {
		gameIDByApp[*g.SteamAppID] = g.ID
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, achievementConcurrency)
	var updated int
	var mu sync.Mutex
	for _, g := range played {
		gameID, ok := gameIDByApp[g.AppID]
		if !ok {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			unlocked, total, ok, err := steamClient.GetPlayerAchievements(steamID, g.AppID)
			if err != nil || !ok {
				return
			}
			err = db.DB.Model(&models.UserGame{}).
				Where("user_id = ? AND game_id = ?", userID, gameID).
				Updates(map[string]interface{}{"achievements_unlocked": unlocked, "achievements_total": total}).Error
			if err != nil {
				log.Printf("Failed to save achievements for app %d: %v", g.AppID, err)
				return
			}
			mu.Lock()
			updated++
			mu.Unlock()
		}()
	}
	wg.Wait()
	log.Printf("Updated achievements for %d games for User %s", updated, userID)
}
