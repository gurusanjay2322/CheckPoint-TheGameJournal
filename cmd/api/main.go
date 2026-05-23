package main

import (
	"log"

	"github.com/checkpoint/server/internal/config"
	"github.com/checkpoint/server/internal/db"
	"github.com/checkpoint/server/internal/handlers"
	"github.com/checkpoint/server/internal/middleware"
	"github.com/checkpoint/server/internal/models"
	"github.com/checkpoint/server/pkg/igdb"
	"github.com/checkpoint/server/pkg/steam"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/swagger"
	"github.com/hibiken/asynq"

	_ "github.com/checkpoint/server/docs"
)

// @title Checkpoint MVP API
// @version 1.0
// @description The Game Journal (Letterboxd for games) API
// @host localhost:3000
// @BasePath /api/v1
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @description Type "Bearer" followed by a space and JWT token.
func main() {
	// Load config
	cfg := config.Load()

	// Initialize database
	db.Init(cfg)

	// Auto-migrate models
	err := db.DB.AutoMigrate(
		&models.User{},
		&models.Game{},
		&models.UserGame{},
		&models.Review{},
		&models.Follow{},
		&models.Activity{},
	)
	if err != nil {
		log.Fatalf("Failed to auto-migrate database: %v", err)
	}

	log.Println("Database migration completed!")

	// Initialize Fiber app
	app := fiber.New(fiber.Config{
		AppName: "Checkpoint MVP API",
	})

	// Middleware
	app.Use(logger.New())
	app.Use(cors.New())

	// Setup dependencies
	steamClient := steam.NewClient(cfg.SteamAPIKey)
	igdbClient := igdb.NewClient(cfg.IGDBClientID, cfg.IGDBSecret)

	redisOpt, _ := asynq.ParseRedisURI(cfg.RedisURL)
	asynqClient := asynq.NewClient(redisOpt)
	defer asynqClient.Close()

	authHandler := handlers.NewAuthHandler(cfg, steamClient, asynqClient)
	gamesHandler := handlers.NewGamesHandler(igdbClient)
	libraryHandler := handlers.NewLibraryHandler()
	reviewsHandler := handlers.NewReviewsHandler()
	socialHandler := handlers.NewSocialHandler()

	// Setup routes
	api := app.Group("/api/v1")

	// Swagger route
	app.Get("/swagger/*", swagger.HandlerDefault)

	api.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok", "message": "Checkpoint API is running"})
	})

	auth := api.Group("/auth")
	auth.Post("/signup", authHandler.Signup)
	auth.Post("/login", authHandler.EmailLogin)
	auth.Post("/steam", authHandler.SteamLogin)

	games := api.Group("/games")
	games.Get("/search", gamesHandler.Search)

	library := api.Group("/library")
	library.Get("/:user_id", libraryHandler.GetUserLibrary)

	reviews := api.Group("/reviews")
	reviews.Get("/game/:igdb_id", reviewsHandler.GetGameReviews)

	social := api.Group("/social")
	social.Get("/profile/:user_id", socialHandler.GetProfile)

	// Protected routes
	api.Put("/auth/email", middleware.Protected(cfg), authHandler.UpdateEmail)
	api.Post("/auth/steam/link", middleware.Protected(cfg), authHandler.LinkSteam)
	api.Post("/library/update", middleware.Protected(cfg), libraryHandler.UpdateStatus)
	api.Post("/reviews", middleware.Protected(cfg), reviewsHandler.CreateReview)
	api.Post("/social/follow", middleware.Protected(cfg), socialHandler.FollowUser)
	api.Get("/social/feed", middleware.Protected(cfg), socialHandler.GetFeed)

	// Start server
	log.Printf("Starting server on port %s", cfg.Port)
	if err := app.Listen(":" + cfg.Port); err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}
}
