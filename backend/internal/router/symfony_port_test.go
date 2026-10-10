package router

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
)

func TestSymfonyPortedPagesAndAPIs(t *testing.T) {
	app := fiber.New()
	secret := "test-secret"
	RegisterV1(app, nil, secret)

	sess := dashboardSession{
		UserID:    1,
		RoleID:    1,
		Email:     "admin@brainbird.com",
		FullName:  "Super Admin",
		ExpiresAt: time.Now().Add(24 * time.Hour).Unix(),
	}
	cookieToken, err := signDashboardSession(sess, secret)
	if err != nil {
		t.Fatalf("sign session: %v", err)
	}

	testPages := []struct {
		url          string
		expectedCode int
		contains     string
	}{
		{"/users/manage", 200, "Users"},
		{"/users/allocation_manager", 200, "Allocation"},
		{"/users/points_setup", 200, "Points"},
		{"/staff_mapping", 200, "Staff Mapping"},
		{"/dispatch", 200, "Dispatch"},
		{"/attendance/manual_matrix", 200, "Attendance"},
		{"/settings/audit_logs", 200, "Audit Logs"},
		{"/menu_manager", 200, "Menu Manager"},
		{"/permission_manager", 200, "Permission"},
		{"/analytics", 200, "Analytics"},
		{"/app_forensic_analytics", 200, "Forensic"},
		{"/reports/employee_performance", 200, "Performance"},
		{"/payout/verification", 200, "Payout"},
		{"/letters-manager/manage", 200, "Letters"},
		{"/letters-manager/generate", 200, "Generate"},
		{"/masters/locations", 200, "Location Master"},
		{"/case/edit", 200, "Edit"},
		{"/master_edit", 200, "Edit"},
	}

	for _, tc := range testPages {
		req := httptest.NewRequest(http.MethodGet, tc.url, nil)
		req.AddCookie(&http.Cookie{
			Name:  sessionCookieName,
			Value: cookieToken,
		})
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("GET %s error: %v", tc.url, err)
		}
		if resp.StatusCode != tc.expectedCode {
			t.Errorf("GET %s status: got %d, want %d", tc.url, resp.StatusCode, tc.expectedCode)
		}
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		body := string(bodyBytes)
		resp.Body.Close()
		if !strings.Contains(body, tc.contains) {
			t.Errorf("GET %s body missing %q", tc.url, tc.contains)
		}
	}

	// Test key JSON APIs
	testAPIs := []struct {
		method   string
		url      string
		body     string
		contains string
	}{
		{"GET", "/api/get_report_makers", "", `"status":"success"`},
		{"GET", "/api/case/report_makers", "", `"status":"success"`},
		{"GET", "/api/intel-chat/get-users", "", `"status":"success"`},
		{"POST", "/api/intel-chat/get-messages", "partner_id=1&is_group=0", `"status":"success"`},
		{"POST", "/api/intel-chat/ai-tools", "ai_action=compose", `"status":"success"`},
		{"POST", "/api/permissions/fetch", "target_type=role&target_id=1", `"status":"success"`},
		{"POST", "/api/attendance/data", "year=2026&month=10", `"status":"success"`},
		{"GET", "/api/payout/history", "", `"status":"success"`},
		{"GET", "/api/payout/rate-masters", "", `"status":"success"`},
		{"GET", "/api/payout/rates", "", `"status":"success"`},
	}

	for _, tc := range testAPIs {
		var req *http.Request
		if tc.method == "POST" {
			req = httptest.NewRequest(http.MethodPost, tc.url, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		} else {
			req = httptest.NewRequest(http.MethodGet, tc.url, nil)
		}
		req.AddCookie(&http.Cookie{
			Name:  sessionCookieName,
			Value: cookieToken,
		})
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("%s %s error: %v", tc.method, tc.url, err)
		}
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		bStr := string(bodyBytes)
		resp.Body.Close()
		if !strings.Contains(bStr, tc.contains) {
			t.Errorf("%s %s body missing %q, got: %s", tc.method, tc.url, tc.contains, bStr)
		}
	}
}
