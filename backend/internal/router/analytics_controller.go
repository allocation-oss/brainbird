package router

import (
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

//go:embed templates/analytics/index.html
var analyticsHTML []byte

//go:embed templates/forensic_analytics/index.html
var forensicAnalyticsHTML []byte

func ensureAnalyticsTables(pool *sql.DB) {
	if pool == nil {
		return
	}
	_, _ = pool.Exec(`
		CREATE TABLE IF NOT EXISTS user_performance_targets (
			id INT AUTO_INCREMENT PRIMARY KEY,
			user_id INT NOT NULL,
			daily_allocation INT DEFAULT 0,
			monthly_allocation INT DEFAULT 0,
			daily_closure INT DEFAULT 0,
			monthly_closure INT DEFAULT 0,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			UNIQUE KEY uq_user_targets (user_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
	`)
}

func getRbacBaseWhere(pool *sql.DB, userId, roleId int) string {
	baseWhere := "c.deleted_at IS NULL AND c.claim_number IS NOT NULL AND TRIM(c.claim_number) != ''"
	if roleId == 1 {
		return baseWhere
	}

	var mapCheck int
	_ = pool.QueryRow("SELECT COUNT(*) FROM rm_insurance_map WHERE rm_id = ?", userId).Scan(&mapCheck)
	if mapCheck > 0 {
		baseWhere += fmt.Sprintf(" AND c.insurance_company_id IN (SELECT insurance_company_id FROM rm_insurance_map WHERE rm_id = %d)", userId)
	} else if roleId == 3 || roleId == 4 || roleId == 5 || roleId == 7 {
		baseWhere += " AND c.insurance_company_id IN (-1)"
	}

	if roleId == 2 {
		baseWhere += fmt.Sprintf(" AND c.cm_id = %d", userId)
	} else if roleId == 7 {
		baseWhere += fmt.Sprintf(" AND c.allocation_manager_id = %d", userId)
	} else if roleId != 3 && roleId != 4 && roleId != 5 {
		baseWhere += fmt.Sprintf(" AND (c.cm_id = %d OR c.allocation_manager_id = %d OR c.fo_id = %d OR c.icm_id = %d)", userId, userId, userId, userId)
	}

	return baseWhere
}

type filterCond struct {
	WhereSQL string
	Params   []interface{}
}

func buildAnalyticsFilterConditions(startDate, endDate, scope string, masterFilters []map[string]string, verdictFilter, baseWhere string, insFilter, almFilter int) filterCond {
	where := []string{
		baseWhere,
		"UPPER(c.status) != 'TRASHED'",
	}
	var params []interface{}

	if scope == "closed" || scope == "dtd" {
		where = append(where, "c.is_completed = 1")
	} else if scope == "open" {
		where = append(where, "(c.is_completed = 0 OR c.is_completed IS NULL)")
	}

	if startDate != "" && endDate != "" {
		if scope == "open" {
			where = append(where, "DATE(COALESCE(c.allocation_date, c.created_at)) BETWEEN ? AND ?")
		} else {
			where = append(where, "DATE(COALESCE(NULLIF(c.report_submission_date, '0000-00-00 00:00:00'), c.updated_at, c.created_at)) BETWEEN ? AND ?")
		}
		params = append(params, startDate, endDate)
	}

	if insFilter > 0 {
		where = append(where, "c.insurance_company_id = ?")
		params = append(params, insFilter)
	}

	if almFilter > 0 {
		where = append(where, "c.allocation_manager_id = ?")
		params = append(params, almFilter)
	}

	if verdictFilter != "" && verdictFilter != "ALL" {
		vUpper := strings.ToUpper(verdictFilter)
		switch vUpper {
		case "PAYABLE":
			where = append(where, "(UPPER(c.case_verdict) LIKE '%PAYABLE%' OR UPPER(c.case_verdict) LIKE '%GENUINE%' OR UPPER(c.status) = 'PAYABLE')")
		case "REJECTED":
			where = append(where, "(UPPER(c.case_verdict) LIKE '%REJECT%' OR UPPER(c.case_verdict) LIKE '%MOF%' OR UPPER(c.case_verdict) LIKE '%PED%' OR UPPER(c.case_verdict) LIKE '%EXCLUSION%' OR UPPER(c.case_verdict) LIKE '%FRAUD%' OR UPPER(c.case_verdict) LIKE '%OPD%' OR UPPER(c.case_verdict) LIKE '%CASHLESS%' OR UPPER(c.case_verdict) LIKE '%MISREPRESENTATION%' OR UPPER(c.case_verdict) LIKE '%WAITING%' OR UPPER(c.case_verdict) LIKE '%NON%' OR UPPER(c.case_verdict) LIKE '%REPUDIATED%' OR UPPER(c.status) = 'REJECTED')")
		case "QUERY":
			where = append(where, "(UPPER(c.case_verdict) LIKE '%QUERY%' OR UPPER(c.status) = 'QUERY')")
		case "INCONCLUSIVE":
			where = append(where, "(UPPER(c.case_verdict) LIKE '%INCONCLUSIVE%' OR UPPER(c.status) = 'INCONCLUSIVE')")
		case "WITHDRAW":
			where = append(where, "(UPPER(c.case_verdict) LIKE '%WITHDRAW%' OR UPPER(c.status) = 'WITHDRAW')")
		case "REWORK":
			where = append(where, "(UPPER(c.status) = 'REWORK' OR UPPER(c.case_verdict) LIKE '%REWORK%')")
		default:
			where = append(where, "(UPPER(c.case_verdict) LIKE ? OR UPPER(c.status) LIKE ?)")
			params = append(params, "%"+vUpper+"%", "%"+vUpper+"%")
		}
	}

	allowedCols := map[string]string{
		"insurance_company": "ic.company_name",
		"case_type":         "t.type_name",
		"case_sub_type":     "st.sub_type_name",
		"claim_number":      "c.claim_number",
		"patient_name":      "c.patient_name",
		"patient_mobile":    "c.patient_mobile",
		"insured_name":      "c.insured_name",
		"policy_no":         "c.policy_no",
		"hospital_name":     "c.hospital_name",
		"hospital_reg_no":   "c.hospital_reg_no",
		"state_name":        "s.state_name",
		"district_name":     "d.district_name",
		"diagnosis":         "c.diagnosis",
		"trigger_reason":    "c.trigger_reason",
		"status":            "c.status",
		"case_verdict":      "c.case_verdict",
		"am_name":           "am.fullname",
		"alm_name":          "alm.fullname",
		"rm_name":           "rm.fullname",
		"fo_name":           "fo.fullname",
	}

	for _, f := range masterFilters {
		col := f["col"]
		val := strings.TrimSpace(f["val"])
		if dbCol, ok := allowedCols[col]; ok && val != "" {
			where = append(where, fmt.Sprintf("%s LIKE ?", dbCol))
			params = append(params, "%"+val+"%")
		}
	}

	return filterCond{
		WhereSQL: strings.Join(where, " AND "),
		Params:   params,
	}
}

func RegisterAnalyticsRoutes(app *fiber.App, pool *sql.DB, requireSession fiber.Handler) {
	ensureAnalyticsTables(pool)

	renderAnalytics := func(c *fiber.Ctx) error {
		htmlStr := string(analyticsHTML)
		htmlStr = strings.ReplaceAll(htmlStr, "{{SIDEBAR_PARTIAL}}", string(dashboardSidebarPartial))
		htmlStr = strings.ReplaceAll(htmlStr, "{{HEADER_PARTIAL}}", string(dashboardHeaderPartial))
		htmlStr = strings.ReplaceAll(htmlStr, "{{FOOTER_PARTIAL}}", string(dashboardFooterPartial))
		c.Type("html", "utf-8")
		return c.SendString(htmlStr)
	}

	renderForensic := func(c *fiber.Ctx) error {
		htmlStr := string(forensicAnalyticsHTML)
		htmlStr = strings.ReplaceAll(htmlStr, "{{SIDEBAR_PARTIAL}}", string(dashboardSidebarPartial))
		htmlStr = strings.ReplaceAll(htmlStr, "{{HEADER_PARTIAL}}", string(dashboardHeaderPartial))
		htmlStr = strings.ReplaceAll(htmlStr, "{{FOOTER_PARTIAL}}", string(dashboardFooterPartial))
		c.Type("html", "utf-8")
		return c.SendString(htmlStr)
	}

	app.Get("/dashboard/analytics", requireSession, renderAnalytics)
	app.Get("/analytics", requireSession, renderAnalytics)
	app.Get("/forensic_analytics", requireSession, renderForensic)
	app.Get("/app_forensic_analytics", requireSession, renderForensic)

	// API Meta (Dropdown list items)
	app.Get("/api/analytics/meta", requireSession, func(c *fiber.Ctx) error {
		type namedItem struct {
			ID   int    `json:"id"`
			Name string `json:"company_name,omitempty"`
			Full string `json:"fullname,omitempty"`
		}

		insurances := []namedItem{}
		iRows, err := pool.Query("SELECT id, company_name FROM master_insurance_companies WHERE status = 'active' ORDER BY company_name ASC")
		if err == nil {
			defer iRows.Close()
			for iRows.Next() {
				var it namedItem
				if err := iRows.Scan(&it.ID, &it.Name); err == nil {
					insurances = append(insurances, it)
				}
			}
		}

		alms := []namedItem{}
		aRows, err := pool.Query("SELECT id, fullname FROM users WHERE role_id IN (4, 7) AND status = 'active' ORDER BY fullname ASC")
		if err == nil {
			defer aRows.Close()
			for aRows.Next() {
				var it namedItem
				if err := aRows.Scan(&it.ID, &it.Full); err == nil {
					alms = append(alms, it)
				}
			}
		}

		rms := []namedItem{}
		rRows, err := pool.Query("SELECT id, fullname FROM users WHERE role_id = 3 AND status = 'active' ORDER BY fullname ASC")
		if err == nil {
			defer rRows.Close()
			for rRows.Next() {
				var it namedItem
				if err := rRows.Scan(&it.ID, &it.Full); err == nil {
					rms = append(rms, it)
				}
			}
		}

		cms := []namedItem{}
		cRows, err := pool.Query("SELECT id, fullname FROM users WHERE role_id = 2 AND status = 'active' ORDER BY fullname ASC")
		if err == nil {
			defer cRows.Close()
			for cRows.Next() {
				var it namedItem
				if err := cRows.Scan(&it.ID, &it.Full); err == nil {
					cms = append(cms, it)
				}
			}
		}

		return c.JSON(fiber.Map{
			"status":     "success",
			"insurances": insurances,
			"teamMembers": fiber.Map{
				"alms": alms,
				"rms":  rms,
				"cms":  cms,
			},
		})
	})

	// API Dashboard Data
	app.Post("/api/analytics/dashboard-data", requireSession, func(c *fiber.Ctx) error {
		sess, ok := c.Locals(sessionContextKey).(dashboardSession)
		userId := int64(1)
		roleId := 1
		if ok {
			userId = sess.UserID
			roleId = sess.RoleID
		}

		// Action: save_targets
		if c.FormValue("action") == "save_targets" {
			dAlloc, _ := strconv.Atoi(c.FormValue("daily_allocation"))
			mAlloc, _ := strconv.Atoi(c.FormValue("monthly_allocation"))
			dClose, _ := strconv.Atoi(c.FormValue("daily_closure"))
			mClose, _ := strconv.Atoi(c.FormValue("monthly_closure"))

			var exists int
			_ = pool.QueryRow("SELECT id FROM user_performance_targets WHERE user_id = ?", userId).Scan(&exists)
			if exists > 0 {
				_, _ = pool.Exec(`
					UPDATE user_performance_targets 
					SET daily_allocation=?, monthly_allocation=?, daily_closure=?, monthly_closure=?, updated_at=NOW() 
					WHERE user_id=?`, dAlloc, mAlloc, dClose, mClose, userId)
			} else {
				_, _ = pool.Exec(`
					INSERT INTO user_performance_targets (user_id, daily_allocation, monthly_allocation, daily_closure, monthly_closure, updated_at) 
					VALUES (?, ?, ?, ?, ?, NOW())`, userId, dAlloc, mAlloc, dClose, mClose)
			}
			return c.JSON(fiber.Map{"status": "success", "message": "Targets Saved Successfully!"})
		}

		// Fetch current targets
		targetData := fiber.Map{
			"daily_allocation":   0,
			"monthly_allocation": 0,
			"daily_closure":      0,
			"monthly_closure":    0,
		}
		var dAlloc, mAlloc, dClose, mClose int
		err := pool.QueryRow("SELECT daily_allocation, monthly_allocation, daily_closure, monthly_closure FROM user_performance_targets WHERE user_id = ?", userId).Scan(&dAlloc, &mAlloc, &dClose, &mClose)
		if err == nil {
			targetData = fiber.Map{
				"daily_allocation":   dAlloc,
				"monthly_allocation": mAlloc,
				"daily_closure":      dClose,
				"monthly_closure":    mClose,
			}
		}

		baseWhere := getRbacBaseWhere(pool, int(userId), roleId)

		startDateRaw := c.FormValue("start_date")
		endDateRaw := c.FormValue("end_date")
		scope := c.FormValue("base_scope", "closed")
		verdictFilter := strings.TrimSpace(c.FormValue("verdict_filter"))
		slaBucket := strings.TrimSpace(c.FormValue("sla_bucket"))
		insFilter, _ := strconv.Atoi(c.FormValue("insurance_filter"))
		almFilter, _ := strconv.Atoi(c.FormValue("alm_filter"))
		pulseRole := c.FormValue("pulse_role", "RM")
		pulseUserId, _ := strconv.Atoi(c.FormValue("pulse_user_id"))

		masterFilters := []map[string]string{}
		mfJSON := c.FormValue("master_filters_json")
		if mfJSON != "" {
			_ = json.Unmarshal([]byte(mfJSON), &masterFilters)
		}

		now := time.Now()
		var sObj, eObj time.Time
		if startDateRaw != "" {
			sObj, _ = time.Parse("2006-01-02", startDateRaw)
		}
		if sObj.IsZero() {
			sObj = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)
		}

		if endDateRaw != "" {
			eObj, _ = time.Parse("2006-01-02", endDateRaw)
		}
		if eObj.IsZero() {
			firstNextMonth := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.Local)
			eObj = firstNextMonth.AddDate(0, 0, -1)
		}

		startDate := sObj.Format("2006-01-02")
		endDate := eObj.Format("2006-01-02")

		calcEndObj := eObj
		if calcEndObj.After(now) {
			calcEndObj = now
		}

		elapsedDays := 1
		if !sObj.After(calcEndObj) {
			elapsedDays = int(calcEndObj.Sub(sObj).Hours()/24) + 1
		}

		// DTD Logic
		dayDifference := int(eObj.Sub(sObj).Hours() / 24)
		prevStartDateObj := sObj.AddDate(0, -1, 0)
		prevStartDate := prevStartDateObj.Format("2006-01-02")
		prevMtdEndDate := prevStartDateObj.AddDate(0, 0, dayDifference).Format("2006-01-02")

		curScope := scope
		if scope == "dtd" {
			curScope = "closed"
		}
		curCond := buildAnalyticsFilterConditions(startDate, endDate, curScope, masterFilters, "", baseWhere, insFilter, almFilter)
		prevMtdCond := buildAnalyticsFilterConditions(prevStartDate, prevMtdEndDate, curScope, masterFilters, "", baseWhere, insFilter, almFilter)

		joinTables := `
			LEFT JOIN master_insurance_companies ic ON c.insurance_company_id = ic.id
			LEFT JOIN master_case_types t ON c.case_type_id = t.id
			LEFT JOIN master_case_sub_types st ON c.case_sub_type_id = st.id
			LEFT JOIN master_states s ON c.state_id = s.id
			LEFT JOIN master_districts d ON c.city_id = d.id
			LEFT JOIN users am ON c.cm_id = am.id
			LEFT JOIN users icm ON c.icm_id = icm.id
			LEFT JOIN users alm ON c.allocation_manager_id = alm.id
			LEFT JOIN users fo ON c.fo_id = fo.id
			LEFT JOIN users rm ON c.report_maker_id = rm.id
		`

		sqlSummary := fmt.Sprintf(`
			SELECT 
				COUNT(c.id) as total_evaluated,
				SUM(CASE WHEN UPPER(c.case_verdict) LIKE '%%PAYABLE%%' OR UPPER(c.case_verdict) LIKE '%%GENUINE%%' OR UPPER(c.status) = 'PAYABLE' THEN 1 ELSE 0 END) as payable,
				SUM(CASE WHEN UPPER(c.case_verdict) LIKE '%%REJECT%%' OR UPPER(c.case_verdict) LIKE '%%MOF%%' OR UPPER(c.case_verdict) LIKE '%%PED%%' OR UPPER(c.case_verdict) LIKE '%%EXCLUSION%%' OR UPPER(c.case_verdict) LIKE '%%FRAUD%%' OR UPPER(c.case_verdict) LIKE '%%OPD%%' OR UPPER(c.case_verdict) LIKE '%%CASHLESS%%' OR UPPER(c.case_verdict) LIKE '%%MISREPRESENTATION%%' OR UPPER(c.case_verdict) LIKE '%%WAITING%%' OR UPPER(c.case_verdict) LIKE '%%NON%%' OR UPPER(c.case_verdict) LIKE '%%REPUDIATED%%' OR UPPER(c.status) = 'REJECTED' THEN 1 ELSE 0 END) as rejected,
				SUM(CASE WHEN UPPER(c.case_verdict) LIKE '%%QUERY%%' OR UPPER(c.status) = 'QUERY' THEN 1 ELSE 0 END) as query,
				SUM(CASE WHEN UPPER(c.case_verdict) LIKE '%%INCONCLUSIVE%%' OR UPPER(c.status) = 'INCONCLUSIVE' THEN 1 ELSE 0 END) as incon,
				SUM(CASE WHEN UPPER(c.case_verdict) LIKE '%%WITHDRAW%%' OR UPPER(c.status) = 'WITHDRAW' THEN 1 ELSE 0 END) as withdraw,
				SUM(CASE WHEN UPPER(c.status) = 'REWORK' OR UPPER(c.case_verdict) LIKE '%%REWORK%%' THEN 1 ELSE 0 END) as rework
			FROM cases c
			%s
			WHERE %s
		`, joinTables, curCond.WhereSQL)

		var totalEval, payable, rejected, query, incon, withdraw, rework sql.NullInt64
		_ = pool.QueryRow(sqlSummary, curCond.Params...).Scan(&totalEval, &payable, &rejected, &query, &incon, &withdraw, &rework)

		summaryTotals := fiber.Map{
			"total_evaluated": totalEval.Int64,
			"payable":         payable.Int64,
			"rejected":        rejected.Int64,
			"query":           query.Int64,
			"incon":           incon.Int64,
			"withdraw":        withdraw.Int64,
			"rework":          rework.Int64,
		}

		sqlPrevSummary := fmt.Sprintf(`
			SELECT 
				COUNT(c.id) as total_evaluated,
				SUM(CASE WHEN UPPER(c.case_verdict) LIKE '%%PAYABLE%%' OR UPPER(c.case_verdict) LIKE '%%GENUINE%%' OR UPPER(c.status) = 'PAYABLE' THEN 1 ELSE 0 END) as payable,
				SUM(CASE WHEN UPPER(c.case_verdict) LIKE '%%REJECT%%' OR UPPER(c.case_verdict) LIKE '%%MOF%%' OR UPPER(c.case_verdict) LIKE '%%PED%%' OR UPPER(c.case_verdict) LIKE '%%EXCLUSION%%' OR UPPER(c.case_verdict) LIKE '%%FRAUD%%' OR UPPER(c.case_verdict) LIKE '%%OPD%%' OR UPPER(c.case_verdict) LIKE '%%CASHLESS%%' OR UPPER(c.case_verdict) LIKE '%%MISREPRESENTATION%%' OR UPPER(c.case_verdict) LIKE '%%WAITING%%' OR UPPER(c.case_verdict) LIKE '%%NON%%' OR UPPER(c.case_verdict) LIKE '%%REPUDIATED%%' OR UPPER(c.status) = 'REJECTED' THEN 1 ELSE 0 END) as rejected,
				SUM(CASE WHEN UPPER(c.case_verdict) LIKE '%%QUERY%%' OR UPPER(c.status) = 'QUERY' THEN 1 ELSE 0 END) as query
			FROM cases c
			%s
			WHERE %s
		`, joinTables, prevMtdCond.WhereSQL)

		var pTotalEval, pPayable, pRejected, pQuery sql.NullInt64
		_ = pool.QueryRow(sqlPrevSummary, prevMtdCond.Params...).Scan(&pTotalEval, &pPayable, &pRejected, &pQuery)

		previousTotals := fiber.Map{
			"total_evaluated": pTotalEval.Int64,
			"payable":         pPayable.Int64,
			"rejected":        pRejected.Int64,
			"query":           pQuery.Int64,
		}

		// Days in target month
		firstNextM := time.Date(sObj.Year(), sObj.Month()+1, 1, 0, 0, 0, 0, time.Local)
		daysInTargetMonth := firstNextM.AddDate(0, 0, -1).Day()

		// Previous full month
		prevFullStartObj := time.Date(sObj.Year(), sObj.Month()-1, 1, 0, 0, 0, 0, time.Local)
		prevFullStart := prevFullStartObj.Format("2006-01-02")
		prevFullEndObj := time.Date(sObj.Year(), sObj.Month(), 1, 0, 0, 0, 0, time.Local).AddDate(0, 0, -1)
		prevFullEnd := prevFullEndObj.Format("2006-01-02")

		// Closed metrics (closing date)
		todayStr := now.Format("2006-01-02")
		todayCloseCond := buildAnalyticsFilterConditions(todayStr, todayStr, "closed", masterFilters, "", baseWhere, insFilter, almFilter)
		var todayClosedCount int
		_ = pool.QueryRow(fmt.Sprintf("SELECT COUNT(c.id) FROM cases c %s WHERE %s", joinTables, todayCloseCond.WhereSQL), todayCloseCond.Params...).Scan(&todayClosedCount)

		mtdCloseCond := buildAnalyticsFilterConditions(startDate, endDate, "closed", masterFilters, "", baseWhere, insFilter, almFilter)
		var mtdClosedCount int
		_ = pool.QueryRow(fmt.Sprintf("SELECT COUNT(c.id) FROM cases c %s WHERE %s", joinTables, mtdCloseCond.WhereSQL), mtdCloseCond.Params...).Scan(&mtdClosedCount)

		var velocityClosed float64
		if elapsedDays > 0 {
			velocityClosed = math.Round(float64(mtdClosedCount)/float64(elapsedDays)*10) / 10
		}
		projectedClosed := int(math.Round(velocityClosed * float64(daysInTargetMonth)))

		prevFullCloseCond := buildAnalyticsFilterConditions(prevFullStart, prevFullEnd, "closed", masterFilters, "", baseWhere, insFilter, almFilter)
		var prevFullClosedCount int
		_ = pool.QueryRow(fmt.Sprintf("SELECT COUNT(c.id) FROM cases c %s WHERE %s", joinTables, prevFullCloseCond.WhereSQL), prevFullCloseCond.Params...).Scan(&prevFullClosedCount)
		deficitSurplusClosed := projectedClosed - prevFullClosedCount

		// SLA Buckets
		openCond := buildAnalyticsFilterConditions(startDate, endDate, "open", masterFilters, "", baseWhere, insFilter, almFilter)
		sqlSLA := fmt.Sprintf(`
			SELECT 
				SUM(CASE WHEN TIMESTAMPDIFF(HOUR, COALESCE(c.allocation_date, c.created_at), NOW()) <= 24 THEN 1 ELSE 0 END) as bucket_0_24,
				SUM(CASE WHEN TIMESTAMPDIFF(HOUR, COALESCE(c.allocation_date, c.created_at), NOW()) > 24 AND TIMESTAMPDIFF(HOUR, COALESCE(c.allocation_date, c.created_at), NOW()) <= 48 THEN 1 ELSE 0 END) as bucket_24_48,
				SUM(CASE WHEN TIMESTAMPDIFF(HOUR, COALESCE(c.allocation_date, c.created_at), NOW()) > 48 AND TIMESTAMPDIFF(HOUR, COALESCE(c.allocation_date, c.created_at), NOW()) <= 72 THEN 1 ELSE 0 END) as bucket_48_72,
				SUM(CASE WHEN TIMESTAMPDIFF(HOUR, COALESCE(c.allocation_date, c.created_at), NOW()) > 72 THEN 1 ELSE 0 END) as bucket_72_plus
			FROM cases c
			%s
			WHERE %s
		`, joinTables, openCond.WhereSQL)

		var b024, b2448, b4872, b72p sql.NullInt64
		_ = pool.QueryRow(sqlSLA, openCond.Params...).Scan(&b024, &b2448, &b4872, &b72p)

		// 4-Month Trailing Trend
		threeMonthStart := sObj.AddDate(0, -3, 0).Format("2006-01-01")
		trendEndDate := eObj.Format("2006-01-02")
		trendCond := buildAnalyticsFilterConditions(threeMonthStart, trendEndDate, curScope, masterFilters, "", baseWhere, insFilter, almFilter)
		dateField := "COALESCE(NULLIF(c.report_submission_date, '0000-00-00 00:00:00'), c.updated_at, c.created_at)"
		if scope == "open" || scope == "total" {
			dateField = "COALESCE(c.allocation_date, c.created_at)"
		}

		sqlTrend := fmt.Sprintf(`
			SELECT 
				DATE_FORMAT(%s, '%%Y-%%m') as month_grp,
				DATE_FORMAT(%s, '%%b %%y') as month_label,
				COUNT(c.id) as total_cases,
				SUM(CASE WHEN UPPER(c.case_verdict) LIKE '%%PAYABLE%%' OR UPPER(c.case_verdict) LIKE '%%GENUINE%%' OR UPPER(c.status) = 'PAYABLE' THEN 1 ELSE 0 END) as payable,
				SUM(CASE WHEN UPPER(c.case_verdict) LIKE '%%REJECT%%' OR UPPER(c.case_verdict) LIKE '%%MOF%%' OR UPPER(c.case_verdict) LIKE '%%PED%%' OR UPPER(c.case_verdict) LIKE '%%EXCLUSION%%' OR UPPER(c.case_verdict) LIKE '%%FRAUD%%' OR UPPER(c.case_verdict) LIKE '%%OPD%%' OR UPPER(c.case_verdict) LIKE '%%CASHLESS%%' OR UPPER(c.case_verdict) LIKE '%%MISREPRESENTATION%%' OR UPPER(c.case_verdict) LIKE '%%WAITING%%' OR UPPER(c.case_verdict) LIKE '%%NON%%' OR UPPER(c.case_verdict) LIKE '%%REPUDIATED%%' OR UPPER(c.status) = 'REJECTED' THEN 1 ELSE 0 END) as rejected,
				SUM(CASE WHEN UPPER(c.case_verdict) LIKE '%%QUERY%%' OR UPPER(c.status) = 'QUERY' THEN 1 ELSE 0 END) as query,
				SUM(CASE WHEN UPPER(c.case_verdict) LIKE '%%INCONCLUSIVE%%' OR UPPER(c.status) = 'INCONCLUSIVE' THEN 1 ELSE 0 END) as incon,
				SUM(CASE WHEN UPPER(c.case_verdict) LIKE '%%WITHDRAW%%' OR UPPER(c.status) = 'WITHDRAW' THEN 1 ELSE 0 END) as withdraw,
				SUM(CASE WHEN UPPER(c.status) = 'REWORK' OR UPPER(c.case_verdict) LIKE '%%REWORK%%' THEN 1 ELSE 0 END) as rework
			FROM cases c
			%s
			WHERE %s
			GROUP BY month_grp, month_label
			ORDER BY month_grp ASC
		`, dateField, dateField, joinTables, trendCond.WhereSQL)

		trendRows, err := pool.Query(sqlTrend, trendCond.Params...)
		trendData := []fiber.Map{}
		if err == nil {
			defer trendRows.Close()
			for trendRows.Next() {
				var mGrp, mLabel string
				var tot, pay, rej, qry, inc, wth, rwk sql.NullInt64
				if err := trendRows.Scan(&mGrp, &mLabel, &tot, &pay, &rej, &qry, &inc, &wth, &rwk); err == nil {
					trendData = append(trendData, fiber.Map{
						"month_grp":    mGrp,
						"month_label":  mLabel,
						"total_cases":  tot.Int64,
						"payable":      pay.Int64,
						"rejected":     rej.Int64,
						"query":        qry.Int64,
						"incon":        inc.Int64,
						"withdraw":     wth.Int64,
						"rework":       rwk.Int64,
					})
				}
			}
		}

		// Intraday Hourly Pulse
		pulseDateField := "COALESCE(NULLIF(c.report_submission_date, '0000-00-00 00:00:00'), c.updated_at, c.created_at)"
		if pulseRole == "ALM" || pulseRole == "CM" {
			pulseDateField = "COALESCE(c.allocation_date, c.created_at)"
		}

		var targetPulseDate string
		_ = pool.QueryRow(fmt.Sprintf(`
			SELECT DATE(%s) as max_date 
			FROM cases c 
			WHERE DATE(%s) BETWEEN ? AND ? 
			ORDER BY max_date DESC LIMIT 1
		`, pulseDateField, pulseDateField), startDate, endDate).Scan(&targetPulseDate)

		if targetPulseDate == "" {
			targetPulseDate = endDate
		}

		pulseWhere := []string{baseWhere, fmt.Sprintf("DATE(%s) = ?", pulseDateField)}
		pulseParams := []interface{}{targetPulseDate}

		if pulseUserId > 0 {
			if pulseRole == "ALM" {
				pulseWhere = append(pulseWhere, "c.allocation_manager_id = ?")
			} else if pulseRole == "CM" {
				pulseWhere = append(pulseWhere, "c.cm_id = ?")
			} else {
				pulseWhere = append(pulseWhere, "c.report_maker_id = ?")
			}
			pulseParams = append(pulseParams, pulseUserId)
		}
		if insFilter > 0 {
			pulseWhere = append(pulseWhere, "c.insurance_company_id = ?")
			pulseParams = append(pulseParams, insFilter)
		}
		if almFilter > 0 {
			pulseWhere = append(pulseWhere, "c.allocation_manager_id = ?")
			pulseParams = append(pulseParams, almFilter)
		}

		sqlIntraday := fmt.Sprintf(`
			SELECT HOUR(%s) as hour_slot, COUNT(c.id) as case_count
			FROM cases c
			WHERE %s
			GROUP BY hour_slot
			ORDER BY hour_slot ASC
		`, pulseDateField, strings.Join(pulseWhere, " AND "))

		hourlyPulseMap := make(map[int]int)
		pRows, err := pool.Query(sqlIntraday, pulseParams...)
		if err == nil {
			defer pRows.Close()
			for pRows.Next() {
				var slot, cnt int
				if err := pRows.Scan(&slot, &cnt); err == nil {
					hourlyPulseMap[slot] = cnt
				}
			}
		}

		isToday := (targetPulseDate == now.Format("2006-01-02"))
		currentHour := 20
		if isToday {
			currentHour = now.Hour()
		}

		startWorkingHour := 9
		endWorkingHour := 20
		maxH := currentHour
		if endWorkingHour > maxH {
			maxH = endWorkingHour
		}

		hourlyLabels := []string{}
		hourlyData := []int{}
		totalTodaySoFar := 0

		for h := startWorkingHour; h <= maxH; h++ {
			hourlyLabels = append(hourlyLabels, fmt.Sprintf("%02d:00", h))
			cnt := hourlyPulseMap[h]
			hourlyData = append(hourlyData, cnt)
			if h <= currentHour {
				totalTodaySoFar += cnt
			}
		}

		workedHours := currentHour - startWorkingHour + 1
		if workedHours < 1 {
			workedHours = 1
		}
		if workedHours > 11 {
			workedHours = 11
		}

		var hourlySpeed float64
		if totalTodaySoFar > 0 {
			hourlySpeed = float64(totalTodaySoFar) / float64(workedHours)
		}
		dayEndProjected := int(math.Round(hourlySpeed * 11))

		advancedMetrics := fiber.Map{
			"velocity":        velocityClosed,
			"projected":       projectedClosed,
			"target_pace":     prevFullClosedCount,
			"deficit_surplus": deficitSurplusClosed,
			"today_closed":    todayClosedCount,
			"mtd_closed":      mtdClosedCount,
			"sla_buckets": fiber.Map{
				"0_24":    b024.Int64,
				"24_48":   b2448.Int64,
				"48_72":   b4872.Int64,
				"72_plus": b72p.Int64,
			},
			"trend": trendData,
			"intradayPulse": fiber.Map{
				"labels":          hourlyLabels,
				"data":            hourlyData,
				"totalTodaySoFar": totalTodaySoFar,
				"hourlySpeed":     math.Round(hourlySpeed*10) / 10,
				"dayEndProjected": dayEndProjected,
			},
		}

		// Pivot Table with 3 Months DTD
		p1StartObj := sObj.AddDate(0, -1, 0)
		p1Start := p1StartObj.Format("2006-01-02")
		p1End := eObj.AddDate(0, -1, 0).Format("2006-01-02")
		p1Label := p1StartObj.Format("Jan 06") + " (DTD)"

		p2StartObj := sObj.AddDate(0, -2, 0)
		p2Start := p2StartObj.Format("2006-01-02")
		p2End := eObj.AddDate(0, -2, 0).Format("2006-01-02")
		p2Label := p2StartObj.Format("Jan 06") + " (DTD)"

		p3StartObj := sObj.AddDate(0, -3, 0)
		p3Start := p3StartObj.Format("2006-01-02")
		p3End := eObj.AddDate(0, -3, 0).Format("2006-01-02")
		p3Label := p3StartObj.Format("Jan 06") + " (DTD)"

		pivotBaseCond := buildAnalyticsFilterConditions(p3Start, endDate, curScope, masterFilters, "", baseWhere, insFilter, almFilter)
		dateFieldPivot := "DATE(COALESCE(NULLIF(c.report_submission_date, '0000-00-00 00:00:00'), c.updated_at, c.created_at))"
		if scope == "open" || scope == "total" {
			dateFieldPivot = "DATE(COALESCE(c.allocation_date, c.created_at))"
		}

		sqlPivot := fmt.Sprintf(`
			SELECT 
				COALESCE(s.state_name, 'Unknown State') as state, 
				COALESCE(ic.company_name, 'Unknown Insurer') as company, 
				COALESCE(alm.fullname, 'Unassigned ALM') as alm_name,
				COALESCE(icm.fullname, 'Unassigned ICM') as icm_name,
				COALESCE(rm.fullname, 'Unassigned RM') as rm_name,
				COALESCE(t.type_name, 'Unknown Type') as type_name,
				COALESCE(st.sub_type_name, 'Unknown Subtype') as sub_type_name,
				COALESCE(d.district_name, 'Unknown City') as district,
				
				SUM(CASE WHEN %s BETWEEN '%s' AND '%s' THEN 1 ELSE 0 END) as total_evaluated,
				SUM(CASE WHEN %s BETWEEN '%s' AND '%s' AND (UPPER(c.case_verdict) LIKE '%%PAYABLE%%' OR UPPER(c.case_verdict) LIKE '%%GENUINE%%' OR UPPER(c.status) = 'PAYABLE') THEN 1 ELSE 0 END) as payable,
				SUM(CASE WHEN %s BETWEEN '%s' AND '%s' AND (UPPER(c.case_verdict) LIKE '%%REJECT%%' OR UPPER(c.case_verdict) LIKE '%%MOF%%' OR UPPER(c.case_verdict) LIKE '%%PED%%' OR UPPER(c.case_verdict) LIKE '%%EXCLUSION%%' OR UPPER(c.case_verdict) LIKE '%%FRAUD%%' OR UPPER(c.case_verdict) LIKE '%%OPD%%' OR UPPER(c.case_verdict) LIKE '%%CASHLESS%%' OR UPPER(c.case_verdict) LIKE '%%MISREPRESENTATION%%' OR UPPER(c.case_verdict) LIKE '%%WAITING%%' OR UPPER(c.case_verdict) LIKE '%%NON%%' OR UPPER(c.case_verdict) LIKE '%%REPUDIATED%%' OR UPPER(c.status) = 'REJECTED') THEN 1 ELSE 0 END) as rejected,
				SUM(CASE WHEN %s BETWEEN '%s' AND '%s' AND (UPPER(c.case_verdict) LIKE '%%QUERY%%' OR UPPER(c.status) = 'QUERY') THEN 1 ELSE 0 END) as query,
				SUM(CASE WHEN %s BETWEEN '%s' AND '%s' AND (UPPER(c.case_verdict) LIKE '%%INCONCLUSIVE%%' OR UPPER(c.status) = 'INCONCLUSIVE') THEN 1 ELSE 0 END) as incon,
				SUM(CASE WHEN %s BETWEEN '%s' AND '%s' AND (UPPER(c.case_verdict) LIKE '%%WITHDRAW%%' OR UPPER(c.status) = 'WITHDRAW') THEN 1 ELSE 0 END) as withdraw,
				SUM(CASE WHEN %s BETWEEN '%s' AND '%s' AND (UPPER(c.status) = 'REWORK' OR UPPER(c.case_verdict) LIKE '%%REWORK%%') THEN 1 ELSE 0 END) as rework,
				SUM(CASE WHEN %s BETWEEN '%s' AND '%s' THEN 1 ELSE 0 END) as prev1_evaluated,
				SUM(CASE WHEN %s BETWEEN '%s' AND '%s' THEN 1 ELSE 0 END) as prev2_evaluated,
				SUM(CASE WHEN %s BETWEEN '%s' AND '%s' THEN 1 ELSE 0 END) as prev3_evaluated
			FROM cases c
			%s
			WHERE %s
			GROUP BY s.state_name, ic.company_name, alm.fullname, icm.fullname, rm.fullname, t.type_name, st.sub_type_name, d.district_name
			HAVING total_evaluated > 0 OR prev1_evaluated > 0 OR prev2_evaluated > 0 OR prev3_evaluated > 0
			LIMIT 500
		`, dateFieldPivot, startDate, endDate,
			dateFieldPivot, startDate, endDate,
			dateFieldPivot, startDate, endDate,
			dateFieldPivot, startDate, endDate,
			dateFieldPivot, startDate, endDate,
			dateFieldPivot, startDate, endDate,
			dateFieldPivot, startDate, endDate,
			dateFieldPivot, p1Start, p1End,
			dateFieldPivot, p2Start, p2End,
			dateFieldPivot, p3Start, p3End,
			joinTables, pivotBaseCond.WhereSQL)

		pivotData := []fiber.Map{}
		pvtRows, err := pool.Query(sqlPivot, pivotBaseCond.Params...)
		if err == nil {
			defer pvtRows.Close()
			for pvtRows.Next() {
				var st, cp, alm, icm, rm, tp, stp, ds string
				var tot, pay, rej, qry, inc, wth, rwk, p1, p2, p3 sql.NullInt64
				if err := pvtRows.Scan(&st, &cp, &alm, &icm, &rm, &tp, &stp, &ds, &tot, &pay, &rej, &qry, &inc, &wth, &rwk, &p1, &p2, &p3); err == nil {
					pivotData = append(pivotData, fiber.Map{
						"state":            st,
						"company":          cp,
						"alm_name":         alm,
						"icm_name":         icm,
						"rm_name":          rm,
						"type_name":        tp,
						"sub_type_name":    stp,
						"district":         ds,
						"total_evaluated":  tot.Int64,
						"payable":          pay.Int64,
						"rejected":         rej.Int64,
						"query":            qry.Int64,
						"incon":            inc.Int64,
						"withdraw":         wth.Int64,
						"rework":           rwk.Int64,
						"prev1_evaluated":  p1.Int64,
						"prev2_evaluated":  p2.Int64,
						"prev3_evaluated":  p3.Int64,
					})
				}
			}
		}

		// DTD Breakdown Cards
		type dtdItem struct {
			Label   string `json:"label"`
			CurVol  int    `json:"cur_vol"`
			PrevVol int    `json:"prev_vol"`
		}

		loadDtdBreakdown := func(groupCol, labelAlias string) []fiber.Map {
			q := fmt.Sprintf(`
				SELECT 
					COALESCE(%s, 'Unknown') as %s,
					SUM(CASE WHEN %s BETWEEN '%s' AND '%s' THEN 1 ELSE 0 END) as cur_vol,
					SUM(CASE WHEN %s BETWEEN '%s' AND '%s' THEN 1 ELSE 0 END) as prev_vol
				FROM cases c %s WHERE %s GROUP BY %s
			`, groupCol, labelAlias, dateFieldPivot, startDate, endDate, dateFieldPivot, p1Start, p1End, joinTables, pivotBaseCond.WhereSQL, groupCol)

			res := []fiber.Map{}
			r, err := pool.Query(q, pivotBaseCond.Params...)
			if err == nil {
				defer r.Close()
				for r.Next() {
					var lbl string
					var cv, pv sql.NullInt64
					if err := r.Scan(&lbl, &cv, &pv); err == nil {
						res = append(res, fiber.Map{
							labelAlias: lbl,
							"cur_vol":  cv.Int64,
							"prev_vol": pv.Int64,
						})
					}
				}
			}
			return res
		}

		dtdStateData := loadDtdBreakdown("s.state_name", "state")
		dtdAlmData := loadDtdBreakdown("alm.fullname", "alm")
		dtdInsData := loadDtdBreakdown("ic.company_name", "insurer")
		dtdSubtypeData := loadDtdBreakdown("st.sub_type_name", "sub_type")
		dtdDistrictData := loadDtdBreakdown("d.district_name", "district")

		// Detail Ledger (limit 200)
		ledgerCond := buildAnalyticsFilterConditions(startDate, endDate, scope, masterFilters, verdictFilter, baseWhere, insFilter, almFilter)
		if slaBucket != "" {
			switch slaBucket {
			case "0_24":
				ledgerCond.WhereSQL += " AND TIMESTAMPDIFF(HOUR, COALESCE(c.allocation_date, c.created_at), NOW()) <= 24"
			case "24_48":
				ledgerCond.WhereSQL += " AND TIMESTAMPDIFF(HOUR, COALESCE(c.allocation_date, c.created_at), NOW()) > 24 AND TIMESTAMPDIFF(HOUR, COALESCE(c.allocation_date, c.created_at), NOW()) <= 48"
			case "48_72":
				ledgerCond.WhereSQL += " AND TIMESTAMPDIFF(HOUR, COALESCE(c.allocation_date, c.created_at), NOW()) > 48 AND TIMESTAMPDIFF(HOUR, COALESCE(c.allocation_date, c.created_at), NOW()) <= 72"
			case "72_plus":
				ledgerCond.WhereSQL += " AND TIMESTAMPDIFF(HOUR, COALESCE(c.allocation_date, c.created_at), NOW()) > 72"
			}
		}

		sqlLedger := fmt.Sprintf(`
			SELECT 
				c.id,
				c.claim_number, 
				COALESCE(c.patient_name, '--') as patient_name, 
				COALESCE(ic.company_name, '--') as company_name, 
				COALESCE(t.type_name, '--') as type_name, 
				COALESCE(c.hospital_name, '--') as hospital_name,
				COALESCE(s.state_name, '--') as state_name,
				COALESCE(d.district_name, '--') as city_name,
				COALESCE(c.case_verdict, c.status, 'CLOSED') as case_verdict, 
				c.status as raw_status,
				0 as is_rework_case,
				DATE_FORMAT(COALESCE(NULLIF(c.report_submission_date, '0000-00-00 00:00:00'), c.updated_at, c.created_at), '%%d %%b %%Y') as closed_on,
				DATE_FORMAT(COALESCE(c.allocation_date, c.created_at), '%%d %%b %%Y %%h:%%i %%p') as allocated_on,
				TIMESTAMPDIFF(HOUR, COALESCE(c.allocation_date, c.created_at), NOW()) as aging_hours,
				TIMESTAMPDIFF(SECOND, COALESCE(c.allocation_date, c.created_at), NOW()) as aging_seconds,
				COALESCE(fo.fullname, 'Unassigned') as fo_name,
				COALESCE(rm.fullname, 'Unassigned') as rm_name,
				COALESCE(am.fullname, 'Unassigned') as am_name,
				COALESCE(alm.fullname, 'Unassigned') as alm_name
			FROM cases c
			%s
			WHERE %s
			ORDER BY c.id DESC LIMIT 200
		`, joinTables, ledgerCond.WhereSQL)

		ledgerData := []fiber.Map{}
		lRows, err := pool.Query(sqlLedger, ledgerCond.Params...)
		if err == nil {
			defer lRows.Close()
			for lRows.Next() {
				var id, isRework, agingHours, agingSecs int
				var claimNo, patName, compName, typName, hospName, stName, cityName, verdict, rawStat, closedOn, allocOn, foName, rmName, amName, almName sql.NullString
				if err := lRows.Scan(&id, &claimNo, &patName, &compName, &typName, &hospName, &stName, &cityName, &verdict, &rawStat, &isRework, &closedOn, &allocOn, &agingHours, &agingSecs, &foName, &rmName, &amName, &almName); err == nil {
					ledgerData = append(ledgerData, fiber.Map{
						"id":              id,
						"claim_number":    claimNo.String,
						"patient_name":    patName.String,
						"company_name":    compName.String,
						"type_name":       typName.String,
						"hospital_name":   hospName.String,
						"state_name":      stName.String,
						"city_name":       cityName.String,
						"case_verdict":    verdict.String,
						"raw_status":      rawStat.String,
						"is_rework_case":  isRework,
						"closed_on":       closedOn.String,
						"allocated_on":    allocOn.String,
						"aging_hours":     agingHours,
						"aging_seconds":   agingSecs,
						"fo_name":         foName.String,
						"rm_name":         rmName.String,
						"am_name":         amName.String,
						"alm_name":        almName.String,
					})
				}
			}
		}

		return c.JSON(fiber.Map{
			"status":          "success",
			"summaryTotals":   summaryTotals,
			"previousTotals":  previousTotals,
			"advancedMetrics": advancedMetrics,
			"targetData":      targetData,
			"pivotLabels": fiber.Map{
				"p1": p1Label,
				"p2": p2Label,
				"p3": p3Label,
			},
			"pivotData":  pivotData,
			"ledgerData": ledgerData,
			"dtdStats": fiber.Map{
				"state":    dtdStateData,
				"alm":      dtdAlmData,
				"ins":      dtdInsData,
				"subtype":  dtdSubtypeData,
				"district": dtdDistrictData,
			},
		})
	})
}
