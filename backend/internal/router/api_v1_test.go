package router

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"brainbird-backend/internal/platform/database"
	"brainbird-backend/pkg/auth"

	"github.com/gofiber/fiber/v2"
)

func TestRootServesLoginPage(t *testing.T) {
	app := fiber.New()
	RegisterV1(app, nil)

	response, err := app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	if err != nil {
		t.Fatalf("request root page: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("expected root page status 200, got %d", response.StatusCode)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read root page: %v", err)
	}
	page := string(body)
	if !strings.Contains(page, "Sign in | Brain Bird") || !strings.Contains(page, `href="/assets/branding/logo-192.png"`) || !strings.Contains(page, `src="/assets/branding/logo.png"`) {
		t.Fatal("root page should render the branded login page")
	}
}

func TestLoginReturnsJSONWhenDatabaseUnavailable(t *testing.T) {
	app := fiber.New()
	RegisterV1(app, nil)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/login", strings.NewReader(`{"email":"user@example.com","password":"secret"}`))
	request.Header.Set("Content-Type", "application/json")

	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("request login: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d", response.StatusCode)
	}
	if contentType := response.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("expected JSON content type, got %q", contentType)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read login response: %v", err)
	}
	if !strings.Contains(string(body), "authentication database is unavailable") {
		t.Fatalf("expected database-unavailable error, got %s", body)
	}
}

func TestAuthenticateUser(t *testing.T) {
	hash, err := auth.HashPassword("correct-password")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if !authenticateUser(hash, "correct-password") {
		t.Fatal("expected matching password hash to authenticate")
	}
	if !authenticateUser("$2y$"+hash[4:], "correct-password") {
		t.Fatal("expected PHP bcrypt password hash to authenticate")
	}

	if authenticateUser(hash, "wrong-password") {
		t.Fatal("password should not authenticate against a different hash")
	}
	if authenticateUser("", "correct-password") {
		t.Fatal("empty password hash should not authenticate")
	}
}

func TestCaseRemarkVisibilityRoles(t *testing.T) {
	for _, test := range []struct {
		roleID  int
		allowed bool
	}{{1, true}, {2, true}, {3, false}, {4, false}, {5, false}, {6, false}, {7, false}} {
		if allowed := canViewAllCaseRemarks(test.roleID); allowed != test.allowed {
			t.Errorf("role %d: expected canViewAllCaseRemarks=%t, got %t", test.roleID, test.allowed, allowed)
		}
	}
}

func TestNormalizeDirectCloseMode(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
		valid bool
	}{{"", "close", true}, {"  ", "close", true}, {"withdraw", "withdraw", true}, {"denial", "denial", true}, {"unknown", "unknown", false}} {
		got, valid := normalizeDirectCloseMode(test.input)
		if got != test.want || valid != test.valid {
			t.Errorf("normalizeDirectCloseMode(%q) = (%q, %t), want (%q, %t)", test.input, got, valid, test.want, test.valid)
		}
	}
}

func TestBuildDenialRegistryEntries(t *testing.T) {
	entries, err := buildDenialRegistryEntries(
		[]string{"hospital", "patient"},
		[]deniedPartRegistrationInput{{Part: "hospital", UniqueID: " 000123 "}, {Part: "patient", UniqueID: "AAD-45"}},
	)
	if err != nil {
		t.Fatalf("build denial registry entries: %v", err)
	}
	if len(entries) != 2 || entries[0].EntityType != "Hospital" || entries[0].UniqueID != "000123" || entries[1].EntityType != "Patient" || entries[1].UniqueID != "AAD-45" {
		t.Fatalf("unexpected registry entries: %#v", entries)
	}
	if _, err := buildDenialRegistryEntries([]string{"doctor"}, nil); err == nil {
		t.Fatal("expected missing registration ID to be rejected")
	}
	if _, err := buildDenialRegistryEntries([]string{"doctor"}, []deniedPartRegistrationInput{{Part: "hospital", UniqueID: "H-1"}}); err == nil {
		t.Fatal("expected registration for an unselected part to be rejected")
	}
}

func TestDashboardSessionSignatureAndExpiry(t *testing.T) {
	now := time.Now()
	session := dashboardSession{UserID: 42, RoleID: 1, Email: "user@example.com", ExpiresAt: now.Add(time.Hour).Unix()}
	token, err := signDashboardSession(session, "test-secret")
	if err != nil {
		t.Fatalf("sign dashboard session: %v", err)
	}
	if _, ok := readDashboardSession(token, "wrong-secret", now); ok {
		t.Fatal("session signed with a different secret must be rejected")
	}
	if _, ok := readDashboardSession(token, "test-secret", now.Add(2*time.Hour)); ok {
		t.Fatal("expired session must be rejected")
	}
	verified, ok := readDashboardSession(token, "test-secret", now)
	if !ok || verified.UserID != session.UserID || verified.RoleID != session.RoleID {
		t.Fatal("valid signed session should retain its user and role")
	}
}

func TestDashboardAPIReturnsUnauthorizedWithoutSession(t *testing.T) {
	app := fiber.New()
	app.Get("/api/dashboard", requireDashboardSession("test-secret"), func(c *fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) })
	response, err := app.Test(httptest.NewRequest(http.MethodGet, "/api/dashboard", nil))
	if err != nil {
		t.Fatalf("request protected dashboard API: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("expected unauthenticated dashboard request to return 401, got %d", response.StatusCode)
	}
}

func TestDashboardReadsImportedSchemaWhenConfigured(t *testing.T) {
	databaseURL := os.Getenv("BRAINBIRD_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set BRAINBIRD_TEST_DATABASE_URL to run against an imported MariaDB database")
	}

	pool, err := database.NewPool(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("connect to dashboard test database: %v", err)
	}
	defer pool.Close()

	adminSession := dashboardSession{UserID: 1, RoleID: 1}
	payload, err := buildDashboardResponseFromDB(pool, adminSession)
	if err != nil {
		t.Fatalf("load dashboard data: %v", err)
	}
	if payload.Summary.TotalCases == 0 {
		t.Fatal("expected dashboard total to come from imported cases")
	}
	if len(payload.Segments) == 0 || len(payload.Cases) == 0 {
		t.Fatal("expected dashboard statuses and latest cases from the imported schema")
	}
	targets, err := buildDashboardTargets(pool, adminSession)
	if err != nil {
		t.Fatalf("load live dashboard targets: %v", err)
	}
	if targets.Allocation.Current < 0 || targets.Closure.Current < 0 {
		t.Fatal("dashboard targets should return non-negative live counts")
	}
	filterOptions, err := buildDashboardFilterOptions(pool, adminSession, "")
	if err != nil {
		t.Fatalf("load live dashboard filter options: %v", err)
	}
	if len(filterOptions.Insurances) == 0 || len(filterOptions.Types) == 0 || len(filterOptions.States) == 0 {
		t.Fatal("expected insurance, type, and state options from imported records")
	}

	layout, err := buildDashboardLayoutFromDB(pool, adminSession)
	if err != nil {
		t.Fatalf("load dashboard layout: %v", err)
	}
	if len(layout["sidebarModules"].([]*dashboardMenu)) == 0 {
		t.Fatal("expected active sidebar modules from the imported schema")
	}
	if len(layout["appSettings"].(map[string]string)) == 0 {
		t.Fatal("expected app settings from the imported schema")
	}

	app := fiber.New()
	RegisterV1(app, pool, "integration-secret")
	token, err := signDashboardSession(dashboardSession{UserID: 1, RoleID: 1, Email: "integration@test.invalid", ExpiresAt: time.Now().Add(time.Hour).Unix()}, "integration-secret")
	if err != nil {
		t.Fatalf("sign integration session: %v", err)
	}
	for _, route := range []string{"/api/v1/dashboard", "/api/v1/dashboard/case-status", "/api/v1/dashboard/layout", "/api/v1/dashboard/targets", "/api/v1/cases?tab_status=open&limit=3", "/api/v1/cases?tab_status=total&limit=3", "/api/v1/cases?tab_status=completed&limit=3"} {
		request := httptest.NewRequest(http.MethodGet, route, nil)
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
		response, err := app.Test(request)
		if err != nil {
			t.Fatalf("request %s: %v", route, err)
		}
		body, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		if readErr != nil {
			t.Fatalf("read %s response: %v", route, readErr)
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("expected %s to return 200, got %d: %s", route, response.StatusCode, body)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("decode %s response: %v", route, err)
		}
		if route == "/api/v1/dashboard" && payload["summary"] == nil {
			t.Fatal("dashboard route response should contain its live summary")
		}
		if route == "/api/v1/dashboard/layout" && payload["sidebarModules"] == nil {
			t.Fatal("layout route response should contain permitted sidebar modules")
		}
		if route == "/api/v1/dashboard/targets" && payload["allocation"] == nil {
			t.Fatal("dashboard target route response should contain allocation and closure totals")
		}
		if strings.HasPrefix(route, "/api/v1/cases?") && payload["total"] == nil {
			t.Fatal("cases route response should contain the live filtered total")
		}
		if strings.HasPrefix(route, "/api/v1/cases?") && payload["tabCounts"] == nil {
			t.Fatal("cases route response should contain counts for the active filters")
		}
	}
}

func TestBuildDashboardResponse(t *testing.T) {
	payload := buildDashboardResponse()

	if payload.Summary.TotalCases != 0 {
		t.Fatalf("expected zero live data without database, got %d", payload.Summary.TotalCases)
	}

	if payload.Summary.HealthScore != 0 {
		t.Fatalf("expected zero health score without database, got %v", payload.Summary.HealthScore)
	}

	if len(payload.Cases) != 0 {
		t.Fatal("expected no mock case rows when database data is unavailable")
	}

	if len(payload.Trend) != 0 {
		t.Fatal("expected no mock trend data when database data is unavailable")
	}
}

func TestMailAreaManagerValidation(t *testing.T) {
	app := fiber.New()
	RegisterV1(app, nil, "test-secret")
	token, err := signDashboardSession(dashboardSession{UserID: 1, RoleID: 1, Email: "admin@test.invalid", ExpiresAt: time.Now().Add(time.Hour).Unix()}, "test-secret")
	if err != nil {
		t.Fatalf("sign test session: %v", err)
	}

	// 1. Missing am_id should return 400
	req := httptest.NewRequest(http.MethodPost, "/api/v1/cases/mail-am", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request mail-am: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected status 400 for missing am_id, got %d", resp.StatusCode)
	}
	// 2. Test email template renderer
	emailHTML := renderAreaManagerHTMLEmail(areaManagerEmailPayload{
		RecipientName: "John Doe",
		RecipientMail: "john@example.com",
		GeneratedAt:   "30 Sep 2026",
		TotalCases:    10,
		OverdueCases:  2,
		OpenCases:     8,
		ClosedCases:   0,
		DashboardURL:  "http://localhost:8081/dashboard",
		Cases: []emailCaseItem{
			{
				ClaimNumber: "CLM-9999",
				Patient:     "Jane Smith",
				Insurance:   "Star Health",
				CaseType:    "Reimbursement",
				Hospital:    "Apollo Hospital",
				IsOverdue:   true,
				Status:      "open",
			},
		},
	})
	if !strings.Contains(emailHTML, "John Doe") || !strings.Contains(emailHTML, "CLM-9999") || !strings.Contains(emailHTML, "OUT OF TAT") {
		t.Fatal("expected email template to contain recipient name, claim number and out of tat indicator")
	}
}


