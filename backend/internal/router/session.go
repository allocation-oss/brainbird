package router

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

const sessionCookieName = "brainbird_session"
const sessionContextKey = "dashboardSession"

type DashboardSession = dashboardSession

type dashboardSession struct {
	UserID    int64  `json:"uid"`
	RoleID    int    `json:"rid"`
	Email     string `json:"email"`
	FullName  string `json:"name"`
	ExpiresAt int64  `json:"exp"`
}

func signDashboardSession(session dashboardSession, secret string) (string, error) {
	payload, err := json.Marshal(session)
	if err != nil {
		return "", err
	}
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(encodedPayload))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return encodedPayload + "." + signature, nil
}

func readDashboardSession(token, secret string, now time.Time) (dashboardSession, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 || secret == "" {
		return dashboardSession{}, false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(parts[0]))
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(signature, mac.Sum(nil)) {
		return dashboardSession{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return dashboardSession{}, false
	}
	var session dashboardSession
	if err := json.Unmarshal(payload, &session); err != nil || session.UserID <= 0 || session.ExpiresAt <= now.Unix() {
		return dashboardSession{}, false
	}
	return session, true
}

func requireDashboardSession(secret string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		session, ok := readDashboardSession(c.Cookies(sessionCookieName), secret, time.Now())
		if !ok {
			if strings.HasPrefix(c.Path(), "/api/") {
				return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "authentication required"})
			}
			return c.Redirect("/login", fiber.StatusSeeOther)
		}
		c.Locals("dashboardSession", session)
		return c.Next()
	}
}
