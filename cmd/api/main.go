package main

import (
	"log"
	"time"

	"github.com/checkpoint/server/internal/config"
	"github.com/checkpoint/server/internal/db"
	"github.com/checkpoint/server/internal/handlers"
	"github.com/checkpoint/server/internal/middleware"
	"github.com/checkpoint/server/internal/models"
	"github.com/checkpoint/server/pkg/igdb"
	"github.com/checkpoint/server/pkg/steam"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/limiter"
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
	cfg.MustValidateAPI()

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

	// steam_app_id used to be a non-null int, so IGDB-only games stored 0.
	if err := db.DB.Exec(`UPDATE games SET steam_app_id = NULL WHERE steam_app_id = 0`).Error; err != nil {
		log.Fatalf("Failed to backfill steam_app_id: %v", err)
	}

	log.Println("Database migration completed!")

	// Initialize Fiber app
	app := fiber.New(fiber.Config{
		AppName:                 "Checkpoint MVP API",
		ProxyHeader:             cfg.ProxyHeader,
		EnableTrustedProxyCheck: len(cfg.TrustedProxies) > 0,
		TrustedProxies:          cfg.TrustedProxies,
	})

	// Middleware
	app.Use(logger.New())
	app.Use(cors.New())
	app.Use(limiter.New(limiter.Config{
		Max:        100,
		Expiration: 1 * time.Minute,
		KeyGenerator: func(c *fiber.Ctx) string {
			return c.IP()
		},
		LimitReached: func(c *fiber.Ctx) error {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{"error": "rate limit exceeded"})
		},
	}))

	// Setup dependencies
	steamClient := steam.NewClient(cfg.SteamAPIKey)
	igdbClient := igdb.NewClient(cfg.IGDBClientID, cfg.IGDBSecret)

	redisOpt, _ := asynq.ParseRedisURI(cfg.RedisURL)
	asynqClient := asynq.NewClient(redisOpt)
	defer asynqClient.Close()

	authHandler := handlers.NewAuthHandler(cfg, steamClient, asynqClient)
	gamesHandler := handlers.NewGamesHandler(igdbClient)
	libraryHandler := handlers.NewLibraryHandler(asynqClient)
	reviewsHandler := handlers.NewReviewsHandler(asynqClient)
	socialHandler := handlers.NewSocialHandler()
	steamHandler := handlers.NewSteamHandler(cfg, steamClient, asynqClient)

	// Setup routes
	api := app.Group("/api/v1")

	// Swagger route
	app.Get("/swagger/*", swagger.HandlerDefault)

	api.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok", "message": "Checkpoint API is running"})
	})

	// Auth routes
	auth := api.Group("/auth")
	auth.Post("/signup", authHandler.Signup)
	auth.Post("/login", authHandler.EmailLogin)
	auth.Get("/steam/login", authHandler.SteamLogin)
	auth.Get("/steam/callback", authHandler.SteamCallback)

	// Game routes
	games := api.Group("/games")
	games.Get("/search", gamesHandler.Search)
	games.Get("/discover", gamesHandler.Discover)
	games.Get("/trending", gamesHandler.Trending)
	games.Get("/new-releases", gamesHandler.NewReleases)
	games.Get("/:id", gamesHandler.GetGameByID)

	// Library routes
	library := api.Group("/library")
	library.Get("/:user_id", libraryHandler.GetUserLibrary)

	// Review routes
	reviews := api.Group("/reviews")
	reviews.Get("/game/:igdb_id", reviewsHandler.GetGameReviews)
	reviews.Get("/user/:user_id", reviewsHandler.GetUserReviews)

	// Social routes
	social := api.Group("/social")
	social.Get("/profile/:user_id", socialHandler.GetProfile)
	social.Get("/activity/:user_id", socialHandler.GetUserActivity)

	// User search (public)
	api.Get("/users/search", socialHandler.SearchUsers)

	// Steam store and presence (public)
	api.Get("/steam/deals", steamHandler.GetDeals)
	api.Get("/steam/status/:user_id", steamHandler.GetStatus)

	// Protected routes
	api.Get("/auth/me", middleware.Protected(cfg), authHandler.GetMe)
	api.Put("/auth/profile", middleware.Protected(cfg), authHandler.UpdateProfile)
	api.Put("/auth/email", middleware.Protected(cfg), authHandler.UpdateEmail)
	api.Post("/auth/steam/link", middleware.Protected(cfg), authHandler.LinkSteam)
	api.Post("/library/update", middleware.Protected(cfg), libraryHandler.UpdateStatus)
	api.Delete("/library/:game_id", middleware.Protected(cfg), libraryHandler.RemoveFromLibrary)
	api.Post("/reviews", middleware.Protected(cfg), reviewsHandler.CreateReview)
	api.Put("/reviews/:id", middleware.Protected(cfg), reviewsHandler.UpdateReview)
	api.Delete("/reviews/:id", middleware.Protected(cfg), reviewsHandler.DeleteReview)
	api.Post("/social/follow", middleware.Protected(cfg), socialHandler.FollowUser)
	api.Delete("/social/follow", middleware.Protected(cfg), socialHandler.UnfollowUser)
	api.Get("/social/feed", middleware.Protected(cfg), socialHandler.GetFeed)
	api.Get("/steam/wishlist", middleware.Protected(cfg), steamHandler.GetWishlistPrices)
	api.Get("/steam/friends", middleware.Protected(cfg), steamHandler.GetFriends)
	api.Post("/steam/sync", middleware.Protected(cfg), steamHandler.SyncNow)

	// Start server
	log.Printf("Starting server on port %s", cfg.Port)
	if err := app.Listen(":" + cfg.Port); err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}
}
