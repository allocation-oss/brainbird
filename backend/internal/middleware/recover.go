package middleware

import "github.com/gofiber/fiber/v2"

func Recover() fiber.Handler {
	return func(c *fiber.Ctx) error { defer func() { _ = recover() }(); return c.Next() }
}
