package config

import (
	"log"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	DatabaseURL  string
	RedisURL     string
	Port         string
	SteamAPIKey  string
	IGDBClientID string
	IGDBSecret   string
	JWTSecret    string
}

func Load() *Config {
	err := godotenv.Load()
	if err != nil {
		log.Printf("Warning: error loading .env file: %v\n", err)
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}

	return &Config{
		DatabaseURL:  os.Getenv("DATABASE_URL"),
		RedisURL:     os.Getenv("REDIS_URL"),
		Port:         port,
		SteamAPIKey:  os.Getenv("STEAM_API_KEY"),
		IGDBClientID: os.Getenv("IGDB_CLIENT_ID"),
		IGDBSecret:   os.Getenv("IGDB_SECRET"),
		JWTSecret:    os.Getenv("JWT_SECRET"),
	}
}
