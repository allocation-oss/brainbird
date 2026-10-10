package router

import (
	_ "embed"

	"github.com/gofiber/fiber/v2"
)

//go:embed templates/login.html
var loginPageHTML []byte

func RenderLoginPage(c *fiber.Ctx) error {
	c.Type("html", "utf-8")
	return c.Send(loginPageHTML)
}
