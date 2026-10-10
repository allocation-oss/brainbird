package router

import (
	"database/sql"
	"strings"
	"time"

	"brainbird-backend/pkg/auth"
	"github.com/gofiber/fiber/v2"
)

type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Remember bool   `json:"remember"`
}

func authenticateUser(storedHash, password string) bool {
	if strings.HasPrefix(storedHash, "$2y$") {
		storedHash = "$2a$" + storedHash[4:]
	}
	return storedHash != "" && auth.ComparePassword(storedHash, password) == nil
}

func RegisterLoginAPI(api fiber.Router, pool *sql.DB, secret string, requireSession fiber.Handler) {
	api.Post("/login", func(c *fiber.Ctx) error {
		var req LoginRequest
		if err := c.BodyParser(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request payload"})
		}
		email := strings.TrimSpace(strings.ToLower(req.Email))
		if email == "" || req.Password == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "email and password are required"})
		}
		if pool == nil {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "authentication database is unavailable"})
		}
		var userID int64
		var roleID int
		var storedEmail, storedHash, fullName string
		err := pool.QueryRow("SELECT id, role_id, email, password, fullname FROM users WHERE LOWER(email) = ? AND status = 'active' AND deleted_at IS NULL LIMIT 1", email).Scan(&userID, &roleID, &storedEmail, &storedHash, &fullName)
		if err == sql.ErrNoRows || (err == nil && !authenticateUser(storedHash, req.Password)) {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "invalid credentials"})
		}
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "could not authenticate user"})
		}
		expiresAt := time.Now().Add(12 * time.Hour)
		maxAge := int((12 * time.Hour).Seconds())
		if req.Remember {
			expiresAt = time.Now().Add(30 * 24 * time.Hour)
			maxAge = int((30 * 24 * time.Hour).Seconds())
		}
		token, err := signDashboardSession(dashboardSession{UserID: userID, RoleID: roleID, Email: storedEmail, FullName: fullName, ExpiresAt: expiresAt.Unix()}, secret)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "could not create login session"})
		}
		c.Cookie(&fiber.Cookie{Name: sessionCookieName, Value: token, Path: "/", HTTPOnly: true, Secure: c.Secure(), SameSite: "Lax", Expires: expiresAt, MaxAge: maxAge})
		_, _ = pool.Exec("UPDATE users SET last_login = NOW() WHERE id = ?", userID)
		return c.JSON(fiber.Map{"success": true, "user": fiber.Map{"email": storedEmail, "name": fullName, "roleId": roleID}, "redirect": "/dashboard"})
	})
	api.Post("/logout", requireSession, func(c *fiber.Ctx) error {
		c.Cookie(&fiber.Cookie{Name: sessionCookieName, Value: "", Path: "/", HTTPOnly: true, Secure: c.Secure(), SameSite: "Lax", MaxAge: -1})
		return c.JSON(fiber.Map{"success": true, "redirect": "/login"})
	})
}
