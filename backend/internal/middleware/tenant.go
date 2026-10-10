package middleware

import (
	"brainbird-backend/internal/ctxutil"
	"context"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

func Tenant() fiber.Handler {
	return func(c *fiber.Ctx) error {
		id, err := uuid.Parse(c.Get("X-Tenant-ID"))
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "valid X-Tenant-ID is required")
		}
		c.SetUserContext(context.WithValue(ctxutil.WithTenantID(c.UserContext(), id), "tenant_present", true))
		return c.Next()
	}
}
