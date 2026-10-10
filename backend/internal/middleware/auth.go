package middleware

import (
	"brainbird-backend/internal/ctxutil"
	"brainbird-backend/pkg/auth"
	"context"
	"github.com/gofiber/fiber/v2"
	"strings"
)

func Auth(secret string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		parts := strings.Fields(c.Get("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			return fiber.ErrUnauthorized
		}
		claims, err := auth.Verify(secret, parts[1])
		if err != nil {
			return fiber.ErrUnauthorized
		}
		c.SetUserContext(context.WithValue(ctxutil.WithUserID(ctxutil.WithTenantID(c.UserContext(), claims.TenantID), claims.UserID), "auth_present", true))
		return c.Next()
	}
}
