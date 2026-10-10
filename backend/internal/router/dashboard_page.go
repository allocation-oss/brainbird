package router

import (
	_ "embed"
	"strings"

	"github.com/gofiber/fiber/v2"
)

//go:embed templates/dashboard.html
var dashboardPageHTML []byte

//go:embed templates/partials/sidebar.html
var dashboardSidebarPartial []byte

//go:embed templates/partials/header.html
var dashboardHeaderPartial []byte

//go:embed templates/partials/footer.html
var dashboardFooterPartial []byte

func RenderDashboardPage(c *fiber.Ctx) error {
	html := string(dashboardPageHTML)
	html = strings.ReplaceAll(html, "{{SIDEBAR_PARTIAL}}", string(dashboardSidebarPartial))
	html = strings.ReplaceAll(html, "{{HEADER_PARTIAL}}", string(dashboardHeaderPartial))
	html = strings.ReplaceAll(html, "{{FOOTER_PARTIAL}}", string(dashboardFooterPartial))
	c.Type("html", "utf-8")
	return c.SendString(html)
}
