package config

import (
	"log"
	"os"
	"strings"

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

	// PublicURL is the externally reachable base URL of the API (no trailing
	// slash). Steam OpenID redirects back to it.
	PublicURL string
	// AppRedirectURL is where the Steam callback sends the user afterwards,
	// e.g. the app's deep link "checkpoint://auth".
	AppRedirectURL string

	// ProxyHeader is the header carrying the real client IP when running
	// behind a load balancer (e.g. "X-Forwarded-For", "Fly-Client-IP").
	ProxyHeader string
	// TrustedProxies restricts which peers may set ProxyHeader.
	TrustedProxies []string

	// SteamStoreCountry is the default store region for prices (ISO 3166-1
	// alpha-2), used when a request doesn't pass `cc`.
	SteamStoreCountry string
}

func Load() *Config {
	err := godotenv.Load()
	if err != nil {
		log.Printf("Warning: error loading .env file: %v\n", err)
	}

	port := getEnv("PORT", "3000")

	return &Config{
		DatabaseURL:    os.Getenv("DATABASE_URL"),
		RedisURL:       os.Getenv("REDIS_URL"),
		Port:           port,
		SteamAPIKey:    os.Getenv("STEAM_API_KEY"),
		IGDBClientID:   os.Getenv("IGDB_CLIENT_ID"),
		IGDBSecret:     os.Getenv("IGDB_SECRET"),
		JWTSecret:      os.Getenv("JWT_SECRET"),
		PublicURL:      strings.TrimRight(getEnv("PUBLIC_URL", "http://localhost:"+port), "/"),
		AppRedirectURL: getEnv("APP_REDIRECT_URL", "checkpoint://auth"),
		ProxyHeader:    os.Getenv("PROXY_HEADER"),
		TrustedProxies: splitList(os.Getenv("TRUSTED_PROXIES")),

		SteamStoreCountry: strings.ToUpper(getEnv("STEAM_STORE_COUNTRY", "IN")),
	}
}

// MustValidateAPI exits if settings required by the HTTP API are unsafe.
func (c *Config) MustValidateAPI() {
	if len(c.JWTSecret) < 32 {
		log.Fatal("JWT_SECRET must be set to at least 32 characters (e.g. `openssl rand -hex 32`)")
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}
