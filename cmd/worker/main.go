package main

import (
	"log"

	"github.com/checkpoint/server/internal/config"
	"github.com/checkpoint/server/internal/db"
	"github.com/checkpoint/server/internal/worker"
	"github.com/checkpoint/server/pkg/igdb"
	"github.com/checkpoint/server/pkg/steam"
	"github.com/hibiken/asynq"
)

func main() {
	// Load config
	cfg := config.Load()

	// Initialize database
	db.Init(cfg)

	// Setup dependencies
	steamClient := steam.NewClient(cfg.SteamAPIKey)
	igdbClient := igdb.NewClient(cfg.IGDBClientID, cfg.IGDBSecret)

	// Start Asynq worker server
	redisOpt, err := asynq.ParseRedisURI(cfg.RedisURL)
	if err != nil {
		log.Fatalf("Invalid Redis URL: %v", err)
	}

	srv := asynq.NewServer(
		redisOpt,
		asynq.Config{
			Concurrency: 10,
			Queues: map[string]int{
				"default": 10,
			},
		},
	)

	// Need an asynq client to queue follow-up tasks from within tasks
	asynqClient := asynq.NewClient(redisOpt)
	defer asynqClient.Close()

	mux := asynq.NewServeMux()
	mux.HandleFunc(worker.TypeSyncSteamLibrary, worker.HandleSyncSteamLibraryTask(steamClient, asynqClient))
	mux.HandleFunc(worker.TypeEnrichSteamGames, worker.HandleEnrichSteamGamesTask(igdbClient))
	mux.HandleFunc(worker.TypeEnrichIGDBGame, worker.HandleEnrichIGDBGameTask(igdbClient))

	log.Println("Starting Asynq worker server...")
	if err := srv.Run(mux); err != nil {
		log.Fatalf("Could not start worker server: %v", err)
	}
}
