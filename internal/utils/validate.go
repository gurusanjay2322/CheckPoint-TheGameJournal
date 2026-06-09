package utils

import (
	"fmt"
	"strings"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
)

var validate = validator.New()

func ParseAndValidate(c *fiber.Ctx, req interface{}) error {
	if err := c.BodyParser(req); err != nil {
		return BadRequest(c, "invalid request body")
	}

	if err := validate.Struct(req); err != nil {
		details := make(map[string]string)
		for _, err := range err.(validator.ValidationErrors) {
			field := strings.ToLower(err.Field())
			details[field] = fmt.Sprintf("%s", err.Tag())
		}
		return ValidationError(c, details)
	}

	return nil
}
