package router

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

type OpenCaseEntity struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	OpenCases      int    `json:"openCases"`
	EmploymentType string `json:"employmentType,omitempty"`
}

type DashboardSummary struct {
	TotalCases         int     `json:"totalCases"`
	ResolvedToday      int     `json:"resolvedToday"`
	RiskCases          int     `json:"riskCases"`
	OpenCases          int     `json:"openCases"`
	CompletedCases     int     `json:"completedCases"`
	ReimbursementCases int     `json:"reimbursementCases"`
	CashlessCases      int     `json:"cashlessCases"`
	OtherCases         int     `json:"otherCases"`
	HealthScore        float64 `json:"healthScore"`
}

type CaseRow struct {
	ID             string       `json:"id"`
	CaseID         int64        `json:"caseId"`
	ClaimNumber    string       `json:"claimNumber"`
	Patient        string       `json:"patient"`
	Insurance      string       `json:"insurance"`
	CaseType       string       `json:"caseType"`
	SubType        string       `json:"subType"`
	Hospital       string       `json:"hospital"`
	City           string       `json:"city"`
	State          string       `json:"state"`
	AllocatedAt      string       `json:"allocatedAt"`
	ClosedAt         string       `json:"closedAt"`
	ElapsedSeconds   int64        `json:"elapsedSeconds"`
	TATHours         int          `json:"tatHours"`
	AreaManager      string       `json:"areaManager"`
	AreaManagerEmail string       `json:"areaManagerEmail"`
	CaseManager    string       `json:"caseManager"`
	AllocationMgr  string       `json:"allocationManager"`
	FieldOfficer   string       `json:"fieldOfficer"`
	ReportMaker    string       `json:"reportMaker"`
	Remarks        []caseRemark `json:"remarks"`
	OwnRemark      string       `json:"ownRemark"`
	RiskReason     string       `json:"riskReason"`
	Source         string       `json:"source"`
	Owner          string       `json:"owner"`
	Status         string       `json:"status"`
	Verdict        string       `json:"verdict"`
	FinalRemark    string       `json:"finalRemark"`
	Priority       string       `json:"priority"`
	RiskScore      float64      `json:"riskScore"`
}

type Segment struct {
	Name  string `json:"name"`
	Value int    `json:"value"`
	Color string `json:"color"`
}

type DashboardResponse struct {
	Summary          DashboardSummary `json:"summary"`
	Cases            []CaseRow        `json:"cases"`
	Trend            []int            `json:"trend"`
	TrendLabels      []string         `json:"trendLabels"`
	Segments         []Segment        `json:"segments"`
	CompanyOpenCases []OpenCaseEntity `json:"companyOpenCases"`
	StateOpenCases   []OpenCaseEntity `json:"stateOpenCases"`
	FOOpenCases      []OpenCaseEntity `json:"foOpenCases"`
}

func buildDashboardResponse() DashboardResponse {
	return DashboardResponse{
		Summary:          DashboardSummary{},
		Cases:            []CaseRow{},
		Trend:            []int{},
		TrendLabels:      []string{},
		Segments:         []Segment{},
		CompanyOpenCases: []OpenCaseEntity{},
		StateOpenCases:   []OpenCaseEntity{},
		FOOpenCases:      []OpenCaseEntity{},
	}
}

func dashboardCaseScope(pool *sql.DB, session dashboardSession) (string, []any, error) {
	where := "c.deleted_at IS NULL AND c.claim_number IS NOT NULL AND TRIM(c.claim_number) != ''"
	args := make([]any, 0, 5)
	if session.RoleID == 1 {
		return where, args, nil
	}
	mapCount := 0
	if err := pool.QueryRow("SELECT COUNT(*) FROM rm_insurance_map WHERE rm_id = ?", session.UserID).Scan(&mapCount); err != nil {
		return "", nil, err
	}
	if mapCount > 0 {
		where += " AND c.insurance_company_id IN (SELECT insurance_company_id FROM rm_insurance_map WHERE rm_id = ?)"
		args = append(args, session.UserID)
	} else if session.RoleID == 3 || session.RoleID == 4 || session.RoleID == 5 || session.RoleID == 7 {
		where += " AND c.insurance_company_id IN (-1)"
	}
	switch session.RoleID {
	case 2:
		where += " AND c.cm_id = ?"
		args = append(args, session.UserID)
	case 7:
		where += " AND c.allocation_manager_id = ?"
		args = append(args, session.UserID)
	case 3, 4, 5:
	default:
		where += " AND (c.cm_id = ? OR c.allocation_manager_id = ? OR c.fo_id = ? OR c.icm_id = ?)"
		args = append(args, session.UserID, session.UserID, session.UserID, session.UserID)
	}
	return where, args, nil
}

func buildDashboardResponseFromDB(pool *sql.DB, session dashboardSession) (DashboardResponse, error) {
	if pool == nil {
		return DashboardResponse{}, fmt.Errorf("database not configured")
	}
	where, args, err := dashboardCaseScope(pool, session)
	if err != nil {
		return DashboardResponse{}, err
	}

	var summary DashboardSummary
	if err := pool.QueryRow(`SELECT COUNT(*),
    COALESCE(SUM(c.is_completed = 1 AND DATE(c.report_submission_date) = CURDATE()), 0),
		COALESCE(SUM(c.is_completed = 0 AND TIMESTAMPDIFF(HOUR, COALESCE(c.allocation_date, c.created_at), NOW()) > COALESCE(NULLIF(t.tat_hours, 0), 48)), 0),
		COALESCE(SUM(c.is_completed = 0 AND (c.status = 'open' OR c.status = '' OR c.status IS NULL) AND (c.fo_id IS NULL OR c.fo_id = 0)), 0),
		COALESCE(SUM(c.is_completed = 1), 0),
		COALESCE(SUM(LOWER(t.type_name) LIKE '%reimbursement%'), 0),
		COALESCE(SUM(LOWER(t.type_name) LIKE '%cashless%'), 0),
		COALESCE(SUM(LOWER(t.type_name) NOT LIKE '%cashless%' AND LOWER(t.type_name) NOT LIKE '%reimbursement%'), 0)
    FROM cases c LEFT JOIN master_case_types t ON c.case_type_id = t.id WHERE `+where, args...).
		Scan(&summary.TotalCases, &summary.ResolvedToday, &summary.RiskCases, &summary.OpenCases, &summary.CompletedCases, &summary.ReimbursementCases, &summary.CashlessCases, &summary.OtherCases); err != nil {
		return DashboardResponse{}, err
	}

	trendCounts := make(map[string]int)
	trendRows, err := pool.Query(`SELECT DATE_FORMAT(COALESCE(c.allocation_date, c.created_at), '%Y-%m'), COUNT(*)
    FROM cases c WHERE `+where+` AND COALESCE(c.allocation_date, c.created_at) >= DATE_SUB(DATE_FORMAT(CURDATE(), '%Y-%m-01'), INTERVAL 11 MONTH)
    GROUP BY 1 ORDER BY 1`, args...)
	if err != nil {
		return DashboardResponse{}, err
	}
	for trendRows.Next() {
		var month string
		var count int
		if err := trendRows.Scan(&month, &count); err != nil {
			trendRows.Close()
			return DashboardResponse{}, err
		}
		trendCounts[month] = count
	}
	if err := trendRows.Err(); err != nil {
		trendRows.Close()
		return DashboardResponse{}, err
	}
	if err := trendRows.Close(); err != nil {
		return DashboardResponse{}, err
	}
	trend := make([]int, 0, 12)
	trendLabels := make([]string, 0, 12)
	month := time.Now().UTC().AddDate(0, -11, 0)
	month = time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 12; i++ {
		trend = append(trend, trendCounts[month.Format("2006-01")])
		trendLabels = append(trendLabels, month.Format("Jan"))
		month = month.AddDate(0, 1, 0)
	}

	statusRows, err := pool.Query(`SELECT LOWER(COALESCE(NULLIF(c.status, ''), 'open')), COUNT(*)
    FROM cases c WHERE `+where+` GROUP BY 1 ORDER BY COUNT(*) DESC`, args...)
	if err != nil {
		return DashboardResponse{}, err
	}
	segments := make([]Segment, 0)
	colors := map[string]string{"open": "#2563eb", "payable": "#14b8a6", "rejected": "#ef4444", "query": "#f59e0b", "inconclusive": "#64748b", "withdraw": "#94a3b8", "rework": "#8b5cf6"}
	for statusRows.Next() {
		var status string
		var count int
		if err := statusRows.Scan(&status, &count); err != nil {
			statusRows.Close()
			return DashboardResponse{}, err
		}
		color := colors[status]
		if color == "" {
			color = "#64748b"
		}
		segments = append(segments, Segment{Name: strings.ToUpper(status[:1]) + status[1:], Value: count, Color: color})
	}
	if err := statusRows.Err(); err != nil {
		statusRows.Close()
		return DashboardResponse{}, err
	}
	if err := statusRows.Close(); err != nil {
		return DashboardResponse{}, err
	}

	rows, err := pool.Query(`SELECT c.id, COALESCE(c.claim_number, ''), COALESCE(c.patient_name, ''),
		COALESCE(ic.company_name, ''), COALESCE(c.hospital_name, ''), COALESCE(t.type_name, ''),
		DATE_FORMAT(COALESCE(c.allocation_date, c.created_at), '%Y-%m-%d %H:%i:%s'),
    COALESCE(am.fullname, 'Unassigned'), LOWER(COALESCE(NULLIF(c.status, ''), 'open')),
    COALESCE(c.ai_risk_score, 0), c.is_completed, COALESCE(c.fo_id, 0)
    FROM cases c LEFT JOIN master_insurance_companies ic ON c.insurance_company_id = ic.id
		LEFT JOIN master_case_types t ON c.case_type_id = t.id
    LEFT JOIN users am ON c.cm_id = am.id WHERE `+where+`
    ORDER BY COALESCE(c.allocation_date, c.created_at) DESC, c.id DESC LIMIT 6`, args...)
	if err != nil {
		return DashboardResponse{}, err
	}
	defer rows.Close()
	cases := make([]CaseRow, 0, 6)
	for rows.Next() {
		var id int64
		var claimNumber, patient, insurance, hospital, caseType, allocatedAt, owner, status string
		var riskScore float64
		var completed bool
		var foID int64
		if err := rows.Scan(&id, &claimNumber, &patient, &insurance, &hospital, &caseType, &allocatedAt, &owner, &status, &riskScore, &completed, &foID); err != nil {
			return DashboardResponse{}, err
		}
		if claimNumber == "" {
			claimNumber = fmt.Sprintf("%d", id)
		}
		statusClass := "waiting"
		if completed {
			statusClass = "resolved"
		} else if status == "open" && foID == 0 {
			statusClass = "open"
		} else if status == "open" {
			statusClass = "progress"
		}
		cases = append(cases, CaseRow{ID: claimNumber, ClaimNumber: claimNumber, Patient: patient, Insurance: insurance, Hospital: hospital, CaseType: caseType, AllocatedAt: allocatedAt, Source: insurance, Owner: owner, Status: statusClass, Priority: fmt.Sprintf("%.0f%%", riskScore*100), RiskScore: riskScore})
	}
	if err := rows.Err(); err != nil {
		return DashboardResponse{}, err
	}

	companyOpenCases := make([]OpenCaseEntity, 0)
	var unassignedCompanyCount int
	_ = pool.QueryRow("SELECT COUNT(*) FROM cases c WHERE "+where+" AND (c.is_completed = 0 OR LOWER(c.status) = 'open') AND (c.insurance_company_id IS NULL OR c.insurance_company_id = 0 OR c.insurance_company_id NOT IN (SELECT id FROM master_insurance_companies))", args...).Scan(&unassignedCompanyCount)
	if unassignedCompanyCount > 0 {
		companyOpenCases = append(companyOpenCases, OpenCaseEntity{ID: 0, Name: "Other / Direct", OpenCases: unassignedCompanyCount})
	}

	companyRows, err := pool.Query(`SELECT ic.id,
		COALESCE(NULLIF(TRIM(ic.company_name), ''), 'Other / Direct') AS name,
		COUNT(c.id) AS open_count
		FROM master_insurance_companies ic
		LEFT JOIN cases c ON c.insurance_company_id = ic.id AND `+where+` AND (c.is_completed = 0 OR LOWER(c.status) = 'open')
		GROUP BY ic.id, name
		ORDER BY open_count DESC, name ASC`, args...)
	if err == nil {
		defer companyRows.Close()
		for companyRows.Next() {
			var ent OpenCaseEntity
			if err := companyRows.Scan(&ent.ID, &ent.Name, &ent.OpenCases); err == nil {
				companyOpenCases = append(companyOpenCases, ent)
			}
		}
	}
	sort.SliceStable(companyOpenCases, func(i, j int) bool {
		if companyOpenCases[i].OpenCases != companyOpenCases[j].OpenCases {
			return companyOpenCases[i].OpenCases > companyOpenCases[j].OpenCases
		}
		return companyOpenCases[i].Name < companyOpenCases[j].Name
	})

	stateOpenCases := make([]OpenCaseEntity, 0)
	var unassignedStateCount int
	_ = pool.QueryRow("SELECT COUNT(*) FROM cases c WHERE "+where+" AND (c.is_completed = 0 OR LOWER(c.status) = 'open') AND (c.state_id IS NULL OR c.state_id = 0 OR c.state_id NOT IN (SELECT id FROM master_states))", args...).Scan(&unassignedStateCount)
	if unassignedStateCount > 0 {
		stateOpenCases = append(stateOpenCases, OpenCaseEntity{ID: 0, Name: "Other / Unassigned State", OpenCases: unassignedStateCount})
	}

	stateRows, err := pool.Query(`SELECT s.id,
		COALESCE(NULLIF(TRIM(s.state_name), ''), 'Other / Unassigned State') AS name,
		COUNT(c.id) AS open_count
		FROM master_states s
		LEFT JOIN cases c ON c.state_id = s.id AND `+where+` AND (c.is_completed = 0 OR LOWER(c.status) = 'open')
		GROUP BY s.id, name
		ORDER BY open_count DESC, name ASC`, args...)
	if err == nil {
		defer stateRows.Close()
		for stateRows.Next() {
			var ent OpenCaseEntity
			if err := stateRows.Scan(&ent.ID, &ent.Name, &ent.OpenCases); err == nil {
				stateOpenCases = append(stateOpenCases, ent)
			}
		}
	}
	sort.SliceStable(stateOpenCases, func(i, j int) bool {
		if stateOpenCases[i].OpenCases != stateOpenCases[j].OpenCases {
			return stateOpenCases[i].OpenCases > stateOpenCases[j].OpenCases
		}
		return stateOpenCases[i].Name < stateOpenCases[j].Name
	})

	return DashboardResponse{
		Summary:          summary,
		Cases:            cases,
		Trend:            trend,
		TrendLabels:      trendLabels,
		Segments:         segments,
		CompanyOpenCases: companyOpenCases,
		StateOpenCases:   stateOpenCases,
		FOOpenCases:      stateOpenCases,
	}, nil
}

type dashboardMenu struct {
	ID         int              `json:"id"`
	ParentID   *int             `json:"parent_id"`
	ModuleName string           `json:"module_name"`
	ModuleSlug string           `json:"module_slug"`
	Icon       string           `json:"icon"`
	Submenus   []*dashboardMenu `json:"submenus"`
}

type dashboardPermissions struct {
	CanView   bool `json:"can_view"`
	CanAdd    bool `json:"can_add"`
	CanEdit   bool `json:"can_edit"`
	CanDelete bool `json:"can_delete"`
}

func buildDashboardLayoutFromDB(pool *sql.DB, session dashboardSession) (fiber.Map, error) {
	settings := make(map[string]string)
	settingRows, err := pool.Query("SELECT setting_key, setting_value FROM app_settings")
	if err != nil {
		return nil, err
	}
	for settingRows.Next() {
		var key string
		var value sql.NullString
		if err := settingRows.Scan(&key, &value); err != nil {
			settingRows.Close()
			return nil, err
		}
		if value.Valid {
			settings[key] = value.String
		}
	}
	if err := settingRows.Err(); err != nil {
		settingRows.Close()
		return nil, err
	}
	if err := settingRows.Close(); err != nil {
		return nil, err
	}

	menuRows, err := pool.Query(`SELECT DISTINCT m.id, m.parent_id, m.menu_name, m.menu_slug,
    COALESCE(m.icon, 'fa-solid fa-circle'), m.sort_order
    FROM app_sidebar_menus m
    LEFT JOIN user_module_permissions ump ON m.id = ump.module_id AND ump.user_id = ?
    LEFT JOIN role_module_permissions rmp ON m.id = rmp.module_id AND rmp.role_id = ?
    WHERE m.status = 'active' AND (? IN (1, 2) OR COALESCE(ump.can_view, rmp.can_view, 0) = 1 OR m.id IN (
      SELECT DISTINCT mc.parent_id FROM app_sidebar_menus mc
      LEFT JOIN user_module_permissions uc ON mc.id = uc.module_id AND uc.user_id = ?
      LEFT JOIN role_module_permissions rc ON mc.id = rc.module_id AND rc.role_id = ?
      WHERE COALESCE(uc.can_view, rc.can_view, 0) = 1 AND mc.parent_id IS NOT NULL
    )) ORDER BY m.sort_order, m.id`, session.UserID, session.RoleID, session.RoleID, session.UserID, session.RoleID)
	if err != nil {
		return nil, err
	}
	menuByID := make(map[int]*dashboardMenu)
	menuOrder := make([]int, 0)
	for menuRows.Next() {
		var id int
		var parentID sql.NullInt64
		var menu dashboardMenu
		var sortOrder int
		if err := menuRows.Scan(&id, &parentID, &menu.ModuleName, &menu.ModuleSlug, &menu.Icon, &sortOrder); err != nil {
			menuRows.Close()
			return nil, err
		}
		menu.ID = id
		if parentID.Valid && parentID.Int64 > 0 {
			parentValue := int(parentID.Int64)
			menu.ParentID = &parentValue
		}
		menu.Submenus = []*dashboardMenu{}
		menuByID[id] = &menu
		menuOrder = append(menuOrder, id)
	}
	if err := menuRows.Err(); err != nil {
		menuRows.Close()
		return nil, err
	}
	if err := menuRows.Close(); err != nil {
		return nil, err
	}
	menus := make([]*dashboardMenu, 0)
	for _, id := range menuOrder {
		menu := menuByID[id]
		if menu.ParentID == nil {
			menus = append(menus, menu)
		} else if parent := menuByID[*menu.ParentID]; parent != nil {
			parent.Submenus = append(parent.Submenus, menu)
		}
	}

	var profile struct {
		FullName string `json:"fullname"`
		Email    string `json:"email"`
		Username string `json:"username"`
		RoleID   int    `json:"role_id"`
	}
	if err := pool.QueryRow("SELECT fullname, email, username, role_id FROM users WHERE id = ? AND status = 'active' AND deleted_at IS NULL", session.UserID).Scan(&profile.FullName, &profile.Email, &profile.Username, &profile.RoleID); err != nil {
		return nil, err
	}

	permissions := dashboardPermissions{}
	if session.RoleID == 1 || session.RoleID == 2 {
		permissions = dashboardPermissions{CanView: true, CanAdd: true, CanEdit: true, CanDelete: true}
	} else {
		err := pool.QueryRow(`SELECT ump.can_view, ump.can_add, ump.can_edit, ump.can_delete
      FROM user_module_permissions ump JOIN app_sidebar_menus m ON m.id = ump.module_id
      WHERE ump.user_id = ? AND m.menu_slug = 'dashboard' LIMIT 1`, session.UserID).Scan(&permissions.CanView, &permissions.CanAdd, &permissions.CanEdit, &permissions.CanDelete)
		if err == sql.ErrNoRows {
			err = pool.QueryRow(`SELECT rmp.can_view, rmp.can_add, rmp.can_edit, rmp.can_delete
        FROM role_module_permissions rmp JOIN app_sidebar_menus m ON m.id = rmp.module_id
        WHERE rmp.role_id = ? AND m.menu_slug = 'dashboard' LIMIT 1`, session.RoleID).Scan(&permissions.CanView, &permissions.CanAdd, &permissions.CanEdit, &permissions.CanDelete)
		}
		if err != nil && err != sql.ErrNoRows {
			return nil, err
		}
	}

	return fiber.Map{"appSettings": settings, "sidebarModules": menus, "userProfile": profile, "permissions": permissions}, nil
}

func RegisterV1(app *fiber.App, pool *sql.DB, secrets ...string) {
	secret := "local-development-secret"
	if len(secrets) > 0 && secrets[0] != "" {
		secret = secrets[0]
	}
	requireSession := requireDashboardSession(secret)
	app.Get("/", RenderLoginPage)
	app.Get("/login", RenderLoginPage)
	app.Get("/dashboard", requireSession, RenderDashboardPage)
	app.Get("/cases", requireSession, RenderCasesPage)
	app.Get("/database", requireSession, RenderDatabasePage)
	app.Get("/analytics", requireSession, RenderAnalyticsPage)
	app.Get("/reports", requireSession, RenderReportsPage)

	api := app.Group("/api/v1")
	api.Get("/health", func(c *fiber.Ctx) error { return c.JSON(fiber.Map{"status": "ok"}) })
	api.Get("/db-status", func(c *fiber.Ctx) error {
		if pool == nil {
			return fiber.NewError(fiber.StatusServiceUnavailable, "database is not configured")
		}
		if err := pool.Ping(); err != nil {
			return fiber.NewError(fiber.StatusServiceUnavailable, "database unavailable")
		}
		return c.JSON(fiber.Map{"status": "ok"})
	})
	RegisterLoginAPI(api, pool, secret, requireSession)
	RegisterDashboardAPI(api, pool, requireSession)
	RegisterCaseRoutes(app, pool, requireSession)
	RegisterUserRoutes(app, pool, requireSession)
	RegisterAllocationRoutes(app, pool, requireSession)
	RegisterPointsSetupRoutes(app, pool, requireSession)
	RegisterStaffMappingRoutes(app, pool, requireSession)
	RegisterDispatchRoutes(app, pool, requireSession)
	RegisterAttendanceRoutes(app, pool, requireSession)
	RegisterAuditLogsRoutes(app, pool, requireSession)
	RegisterMenuPermissionRoutes(app, pool, requireSession)
	RegisterAnalyticsRoutes(app, pool, requireSession)
	RegisterReportRoutes(app, pool, requireSession)
	RegisterPayoutRoutes(app, pool, requireSession)
	RegisterLettersRoutes(app, pool, requireSession)
	RegisterChatLocationRoutes(app, pool, requireSession)
	RegisterSystemModulesRoutes(app, pool, requireSession)
}

func RenderAuthPage(c *fiber.Ctx) error {
	return RenderLoginPage(c)
}

func renderPreviousDashboardPage(c *fiber.Ctx) error {
	return c.Type("html").SendString(`<!doctype html>
<html lang="en">
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0" />
  <title>Brainbird | Dashboard</title>
  <style>
    :root { --bg:#f5f7fb; --panel:#ffffff; --line:#e2e8f0; --text:#0f172a; --muted:#64748b; --brand:#1f6feb; --teal:#14b8a6; --green:#10b981; --amber:#f59e0b; --red:#ef4444; --shadow:0 18px 50px rgba(15,23,42,.08); }
    * { box-sizing: border-box; }
    html, body { margin: 0; min-height: 100%; font-family: Inter, 'Segoe UI', sans-serif; background: linear-gradient(180deg, #f8fafc, #eef4fb); color: var(--text); }
    body { padding: 24px; }
    a { color: inherit; text-decoration: none; }
    .layout { display: flex; gap: 24px; min-height: calc(100vh - 48px); }
    .sidebar { width: 260px; border: 1px solid var(--line); background: rgba(255,255,255,.9); border-radius: 26px; box-shadow: var(--shadow); padding: 22px 18px; display: flex; flex-direction: column; }
    .brand-row { display: flex; align-items: center; gap: 12px; padding: 8px 8px 18px; border-bottom: 1px solid var(--line); }
    .logo { width: 42px; height: 42px; border-radius: 12px; display: grid; place-items: center; background: linear-gradient(135deg, var(--brand), #60a5fa); color: #fff; font-weight: 900; }
    .nav { display: flex; flex-direction: column; gap: 8px; margin-top: 18px; }
    .nav a { padding: 13px 14px; border-radius: 14px; color: var(--muted); font-weight: 600; border: 1px solid transparent; }
    .nav a.active { background: linear-gradient(135deg, rgba(31,111,235,.08), rgba(31,111,235,.02)); color: var(--text); border-color: rgba(31,111,235,.2); }
    .alert-box { margin-top: auto; padding: 18px 16px; border-radius: 18px; border: 1px solid rgba(245,158,11,.2); background: rgba(245,158,11,.06); }
    .alert-box strong { display: block; font-size: 2rem; margin: 8px 0 4px; }
    .muted { color: var(--muted); }
    .main { flex: 1; min-width: 0; }
    .topbar { display: flex; justify-content: space-between; align-items: center; gap: 18px; margin-bottom: 20px; }
    .eyebrow { margin: 0 0 8px; font-size: 11px; letter-spacing: 0.18em; text-transform: uppercase; color: var(--muted); }
    .topbar h2 { margin: 0; font-size: clamp(2rem, 3vw, 3rem); letter-spacing: -0.06em; }
    .actions { display: flex; align-items: center; gap: 14px; }
    .ghost { padding: 12px 18px; border-radius: 12px; border: 1px solid var(--line); background: #fff; color: var(--text); cursor: pointer; }
    .user { display: flex; align-items: center; gap: 12px; padding: 8px 12px; border: 1px solid var(--line); border-radius: 14px; background: #fff; }
    .avatar { width: 36px; height: 36px; border-radius: 50%; display: grid; place-items: center; background: linear-gradient(135deg, var(--brand), var(--teal)); color: #fff; font-weight: 800; }
    .grid { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 16px; margin-bottom: 18px; }
    .card { padding: 20px 18px; border: 1px solid var(--line); border-radius: 20px; background: rgba(255,255,255,.8); }
    .card.primary { background: linear-gradient(135deg, rgba(31,111,235,.04), rgba(20,184,166,.04)); }
    .head { display: flex; justify-content: space-between; gap: 10px; color: var(--muted); font-size: 0.76rem; }
    .card strong { display: block; margin: 18px 0 8px; font-size: clamp(2rem, 2.5vw, 2.5rem); letter-spacing: -0.06em; }
    .trend.up { color: var(--green); }
    .trend.down { color: var(--red); }
    .panel-row { display: grid; grid-template-columns: 1.5fr 0.95fr; gap: 16px; margin-bottom: 18px; }
    .panel { padding: 20px 18px; border: 1px solid var(--line); border-radius: 22px; background: rgba(255,255,255,.8); }
    .panel-head { display: flex; justify-content: space-between; align-items: center; gap: 12px; margin-bottom: 22px; }
    .panel-head h3 { margin: 0; font-size: 1.35rem; }
    .chart { display: flex; align-items: end; gap: 12px; height: 220px; }
    .bar { flex: 1; display: flex; flex-direction: column; align-items: center; justify-content: end; gap: 10px; height: 100%; }
    .track { width: 100%; height: 100%; padding: 8px; border-radius: 14px 14px 10px 10px; background: linear-gradient(180deg, rgba(31,111,235,.03), rgba(20,184,166,.02)); display: flex; align-items: flex-end; justify-content: center; }
    .fill { width: 100%; min-height: 20%; border-radius: 10px 10px 8px 8px; background: linear-gradient(180deg, var(--brand), var(--teal)); box-shadow: 0 8px 18px rgba(31,111,235,.12); }
    .label { font-size: 0.7rem; color: var(--muted); }
    .donut-wrap { display: flex; align-items: center; justify-content: space-between; gap: 18px; }
    .donut { position: relative; width: 170px; height: 170px; border-radius: 50%; background: conic-gradient(var(--brand) 0 47%, var(--teal) 47% 74%, var(--amber) 74% 100%); display: grid; place-items: center; }
    .donut::before { content: ''; position: absolute; inset: 18px; border-radius: 50%; background: rgba(255,255,255,.96); }
    .donut-center { position: relative; z-index: 1; text-align: center; }
    .donut-center strong { font-size: 1.8rem; }
    .legend { list-style: none; padding: 0; margin: 0; display: flex; flex-direction: column; gap: 12px; }
    .legend li { display: flex; align-items: center; justify-content: space-between; gap: 12px; color: var(--muted); }
    .meta { display: flex; align-items: center; }
    .dot { width: 10px; height: 10px; border-radius: 50%; display: inline-block; margin-right: 10px; }
    .bottom { display: grid; grid-template-columns: 1.5fr 0.95fr; gap: 16px; }
    .table-panel table { width: 100%; border-collapse: collapse; }
    .table-panel th { text-align: left; padding: 0 0 14px; color: var(--muted); font-size: 0.72rem; letter-spacing: 0.12em; text-transform: uppercase; border-bottom: 1px solid var(--line); }
    .table-panel td { padding: 14px 0; border-bottom: 1px solid var(--line); }
    .status { display: inline-flex; align-items: center; justify-content: center; min-width: 96px; padding: 7px 10px; border-radius: 999px; font-size: 0.72rem; font-weight: 700; }
    .status.open { background: rgba(56,189,248,.12); color: var(--brand); }
    .status.progress { background: rgba(245,158,11,.12); color: var(--amber); }
    .status.resolved { background: rgba(16,185,129,.12); color: var(--green); }
    .status.waiting { background: rgba(239,68,68,.1); color: var(--red); }
    .priority { display: inline-flex; align-items: center; padding: 7px 10px; border-radius: 999px; font-size: 0.72rem; font-weight: 700; }
    .priority.high { background: rgba(239,68,68,.1); color: var(--red); }
    .priority.medium { background: rgba(245,158,11,.12); color: var(--amber); }
    .priority.low { background: rgba(16,185,129,.12); color: var(--green); }
    .status-list { display: flex; flex-direction: column; gap: 18px; }
    .db-item { padding: 16px 14px; border-radius: 16px; border: 1px solid var(--line); background: rgba(248,250,252,.9); }
    .db-head { display: flex; justify-content: space-between; align-items: center; gap: 10px; margin-bottom: 12px; }
    .db-pill { padding: 6px 9px; border-radius: 999px; font-size: 0.68rem; font-weight: 700; }
    .db-pill.good { background: rgba(16,185,129,.12); color: var(--green); }
    .db-pill.warn { background: rgba(245,158,11,.12); color: var(--amber); }
    .db-pill.bad { background: rgba(239,68,68,.1); color: var(--red); }
    .progress { height: 9px; border-radius: 999px; background: rgba(15,23,42,.05); overflow: hidden; }
    .progress span { display: block; height: 100%; border-radius: inherit; background: linear-gradient(90deg, var(--brand), var(--teal)); }
    footer { padding: 18px 6px 8px; color: var(--muted); text-align: center; font-size: 0.8rem; }
    @media (max-width: 980px) { .layout { flex-direction: column; } .sidebar { width: 100%; } .grid, .panel-row, .bottom { grid-template-columns: 1fr 1fr; } }
    @media (max-width: 680px) { .topbar { flex-direction: column; align-items: flex-start; } .actions { width: 100%; justify-content: space-between; } .grid, .panel-row, .bottom { grid-template-columns: 1fr; } .donut-wrap { flex-direction: column; } }
  </style>
</head>
<body>
  <div class="layout">
    <aside class="sidebar">
      <div class="brand-row">
        <div class="logo">B</div>
        <div>
          <p class="eyebrow">BRAINBIRD</p>
          <strong>Control center</strong>
        </div>
      </div>
      <nav class="nav">
        <a class="active" href="/dashboard">Overview</a>
        <a href="/cases">Case queue</a>
        <a href="/database">Database health</a>
        <a href="/analytics">Analytics</a>
        <a href="/reports">Reports</a>
      </nav>
      <div class="alert-box">
        <span class="eyebrow">ACTIVE ALERTS</span>
        <strong id="sidebarAlertCount">0</strong>
        <small class="muted">Live from database</small>
      </div>
    </aside>

    <main class="main">
      <header class="topbar">
        <div>
          <p class="eyebrow" style="color: var(--brand);">OPERATIONS OVERVIEW</p>
          <h2>Case intelligence dashboard</h2>
        </div>
        <div class="actions">
          <button id="refreshDashboard" class="ghost">Refresh data</button>
          <div class="user">
            <span class="avatar">A</span>
            <div>
              <strong>Admin</strong>
              <small class="muted">Operations lead</small>
            </div>
          </div>
        </div>
      </header>

      <section class="grid">
        <article class="card primary"><div class="head"><span>Total live cases</span><span class="trend up">+live</span></div><strong id="totalsCase">0</strong><small class="muted">Across database & workflow queues</small></article>
        <article class="card"><div class="head"><span>Resolved today</span><span class="trend up">+db</span></div><strong id="resolvedCount">0</strong><small class="muted">Completed within SLA window</small></article>
        <article class="card"><div class="head"><span>At-risk cases</span><span class="trend down">-watch</span></div><strong id="riskCount">0</strong><small class="muted">Needs escalation</small></article>
        <article class="card"><div class="head"><span>Database health</span><span class="trend up">live</span></div><strong id="healthScore">0%</strong><small class="muted">System uptime & sync stability</small></article>
      </section>

      <section class="panel-row">
        <article class="panel">
          <div class="panel-head"><div><p class="eyebrow">WORKFLOW FLOW</p><h3>Case volume trend</h3></div><span class="muted">Last 30 days</span></div>
          <div class="chart" id="chartBars"></div>
        </article>
        <article class="panel">
          <div class="panel-head"><div><p class="eyebrow">SEGMENTATION</p><h3>Case mix</h3></div></div>
          <div class="donut-wrap">
            <div class="donut"><div class="donut-center"><strong id="mixScore">0%</strong><span class="muted">Healthy</span></div></div>
            <ul class="legend" id="legendList"></ul>
          </div>
        </article>
      </section>

      <section class="bottom">
        <article class="panel table-panel">
          <div class="panel-head"><div><p class="eyebrow">CASE TRACKER</p><h3>Latest cases</h3></div><button class="ghost" style="padding:8px 12px;">Export</button></div>
          <table><thead><tr><th>Case ID</th><th>Source</th><th>Owner</th><th>Status</th><th>Priority</th></tr></thead><tbody id="caseTableBody"></tbody></table>
        </article>
        <article class="panel">
          <div class="panel-head"><div><p class="eyebrow">DATABASE MONITOR</p><h3>System pulse</h3></div></div>
          <div class="status-list" id="dbStatusList"></div>
        </article>
      </section>
    </main>
  </div>
  <footer>Brainbird operations suite · live database status</footer>

  <script>
    function statusTag(status) {
      var map = { open: 'Open', progress: 'In progress', resolved: 'Resolved', waiting: 'Waiting' };
      var classes = { open: 'open', progress: 'progress', resolved: 'resolved', waiting: 'waiting' };
      return '<span class="status ' + classes[status] + '">' + map[status] + '</span>';
    }
    function priorityBadge(priority) {
      var map = { high: 'High', medium: 'Medium', low: 'Low' };
      var classes = { high: 'high', medium: 'medium', low: 'low' };
      return '<span class="priority ' + classes[priority] + '">' + map[priority] + '</span>';
    }
    async function loadDashboard() {
      var response = await fetch('/api/v1/dashboard');
      var data = await response.json();
      document.getElementById('totalsCase').textContent = Number(data.summary.totalCases).toLocaleString();
      document.getElementById('resolvedCount').textContent = Number(data.summary.resolvedToday).toLocaleString();
      document.getElementById('riskCount').textContent = Number(data.summary.riskCases).toLocaleString();
      document.getElementById('healthScore').textContent = data.summary.healthScore + '%';
      document.getElementById('sidebarAlertCount').textContent = Number(data.summary.riskCases).toLocaleString();
      document.getElementById('chartBars').innerHTML = data.trend.map(function (value) {
        return '<div class="bar"><div class="track"><div class="fill" style="height:' + value + '%"></div></div><span class="label">' + Math.ceil(value / 10) + '</span></div>';
      }).join('');
      var segments = Array.isArray(data.segments) ? data.segments : [];
      var total = segments.reduce(function (sum, item) { return sum + item.value; }, 0);
      document.getElementById('legendList').innerHTML = segments.map(function (item) {
        var share = total > 0 ? Math.round((item.value / total) * 100) : 0;
        return '<li><span class="meta"><span class="dot" style="background:' + item.color + '"></span>' + item.name + '</span><strong>' + share + '%</strong></li>';
      }).join('');
      var leadingShare = total > 0 && segments.length > 0 ? Math.round((segments[0].value / total) * 100) : 0;
      document.getElementById('mixScore').textContent = leadingShare + '%';
      document.getElementById('caseTableBody').innerHTML = (Array.isArray(data.cases) ? data.cases : []).map(function (row) {
        return '<tr><td>' + row.id + '</td><td>' + row.source + '</td><td>' + row.owner + '</td><td>' + statusTag(row.status) + '</td><td>' + priorityBadge(row.priority) + '</td></tr>';
      }).join('');
      var statusItems = [
        { name: 'Core database', value: 92, label: 'good' },
        { name: 'User sync', value: 76, label: 'warn' },
        { name: 'Case indexing', value: 89, label: 'good' },
        { name: 'Audit retention', value: 67, label: 'warn' }
      ];
      document.getElementById('dbStatusList').innerHTML = statusItems.map(function (item) {
        return '<div class="db-item"><div class="db-head"><strong>' + item.name + '</strong><span class="db-pill ' + item.label + '">' + item.value + '%</span></div><div class="progress"><span style="width:' + item.value + '%"></span></div></div>';
      }).join('');
    }
    document.getElementById('refreshDashboard').addEventListener('click', loadDashboard);
    loadDashboard();
  </script>
</body>
</html>`)
}

func RenderCasesPage(c *fiber.Ctx) error     { return c.Redirect("/cases/matrix", fiber.StatusSeeOther) }
func RenderDatabasePage(c *fiber.Ctx) error  { return c.Redirect("/dashboard", fiber.StatusSeeOther) }
func RenderAnalyticsPage(c *fiber.Ctx) error { return c.Redirect("/dashboard/analytics", fiber.StatusSeeOther) }
func RenderReportsPage(c *fiber.Ctx) error   { return c.Redirect("/reports/employee_performance", fiber.StatusSeeOther) }

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
