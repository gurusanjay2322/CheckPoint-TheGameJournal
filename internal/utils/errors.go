package utils

import (
	"github.com/gofiber/fiber/v2"
)

type ErrorResponse struct {
	Error   string            `json:"error"`
	Code    string            `json:"code,omitempty"`
	Details map[string]string `json:"details,omitempty"`
}

func BadRequest(c *fiber.Ctx, msg string) error {
	return c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{Error: msg, Code: "BAD_REQUEST"})
}

func Unauthorized(c *fiber.Ctx, msg string) error {
	return c.Status(fiber.StatusUnauthorized).JSON(ErrorResponse{Error: msg, Code: "UNAUTHORIZED"})
}

func NotFound(c *fiber.Ctx, msg string) error {
	return c.Status(fiber.StatusNotFound).JSON(ErrorResponse{Error: msg, Code: "NOT_FOUND"})
}

func Conflict(c *fiber.Ctx, msg string) error {
	return c.Status(fiber.StatusConflict).JSON(ErrorResponse{Error: msg, Code: "CONFLICT"})
}

func InternalError(c *fiber.Ctx, msg string) error {
	return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{Error: msg, Code: "INTERNAL_ERROR"})
}

func ValidationError(c *fiber.Ctx, details map[string]string) error {
	return c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{
		Error:   "validation failed",
		Code:    "VALIDATION_ERROR",
		Details: details,
	})
}

func TooManyRequests(c *fiber.Ctx, msg string) error {
	return c.Status(fiber.StatusTooManyRequests).JSON(ErrorResponse{Error: msg, Code: "RATE_LIMITED"})
}
