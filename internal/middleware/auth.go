package middleware

import (
	"strings"

	"github.com/checkpoint/server/internal/config"
	"github.com/checkpoint/server/internal/utils"
	"github.com/gofiber/fiber/v2"
)

func Protected(cfg *config.Config) fiber.Handler {
	return func(c *fiber.Ctx) error {
		authHeader := c.Get("Authorization")
		if authHeader == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Missing authorization header"})
		}

		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || parts[0] != "Bearer" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Invalid authorization header format"})
		}

		token := parts[1]
		claims, err := utils.ParseJWT(token, cfg.JWTSecret)
		if err != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Invalid or expired token"})
		}

		c.Locals("user_id", claims.UserID)

		return c.Next()
	}
}
