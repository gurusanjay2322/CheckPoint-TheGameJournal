package utils

import (
	"math"

	"github.com/gofiber/fiber/v2"
)

type PaginationMeta struct {
	Page      int   `json:"page"`
	PageSize  int   `json:"page_size"`
	Total     int   `json:"total"`
	TotalPages int  `json:"total_pages"`
	HasMore   bool  `json:"has_more"`
}

func ParsePagination(c *fiber.Ctx) (page int, pageSize int) {
	page = c.QueryInt("page", 1)
	pageSize = c.QueryInt("page_size", 20)
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	return
}

func NewPaginationMeta(total int, page int, pageSize int) PaginationMeta {
	totalPages := 0
	if pageSize > 0 {
		totalPages = int(math.Ceil(float64(total) / float64(pageSize)))
	}
	return PaginationMeta{
		Page:       page,
		PageSize:   pageSize,
		Total:      total,
		TotalPages: totalPages,
		HasMore:    page < totalPages,
	}
}
