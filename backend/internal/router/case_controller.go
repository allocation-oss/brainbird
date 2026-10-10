package router

import (
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"html"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

func escapeHTML(s string) string {
	return html.EscapeString(s)
}

//go:embed templates/cases/matrix.html
var caseMatrixHTML []byte

//go:embed templates/cases/master_edit.html
var caseMasterEditHTML []byte

//go:embed templates/cases/export.html
var caseExportHTML []byte

//go:embed templates/cases/reset.html
var caseResetHTML []byte

//go:embed templates/cases/rework.html
var caseReworkHTML []byte

//go:embed templates/cases/trash.html
var caseTrashHTML []byte

func ensureHospitalTableExists(pool *sql.DB) {
	if pool == nil {
		return
	}
	_, _ = pool.Exec(`
		CREATE TABLE IF NOT EXISTS master_hospitals (
			id INT AUTO_INCREMENT PRIMARY KEY,
			hospital_name VARCHAR(255) NOT NULL,
			hospital_address TEXT DEFAULT NULL,
			state_id INT DEFAULT NULL,
			district_id INT DEFAULT NULL,
			pin_code VARCHAR(20) DEFAULT NULL,
			registration_no VARCHAR(100) DEFAULT NULL,
			status ENUM('active','inactive') DEFAULT 'active',
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			UNIQUE KEY unique_hospital_name (hospital_name)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
	`)
}

func ensureUserExportPrefTableExists(pool *sql.DB) {
	if pool == nil {
		return
	}
	_, _ = pool.Exec(`
		CREATE TABLE IF NOT EXISTS user_export_preferences (
			id INT AUTO_INCREMENT PRIMARY KEY,
			user_id INT NOT NULL UNIQUE,
			selected_columns TEXT NOT NULL,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
	`)
}

func ensureHospitalIdInCases(pool *sql.DB) {
	if pool == nil {
		return
	}

	var colName string
	err := pool.QueryRow("SELECT COLUMN_NAME FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_NAME='cases' AND COLUMN_NAME='hospital_id'").Scan(&colName)
	if err != nil {
		_, _ = pool.Exec("ALTER TABLE cases ADD COLUMN hospital_id INT(11) DEFAULT NULL AFTER hospital_name")
	}
}

func writeCaseAuditLog(pool *sql.DB, c *fiber.Ctx, session dashboardSession, actionPerformed string, caseID int64, caseData map[string]any) {
	if pool == nil {
		return
	}
	userID := session.UserID
	userName := session.FullName
	userRole := fmt.Sprintf("Role %d", session.RoleID)
	if session.RoleID == 1 {
		userRole = "Super Admin"
	} else if session.RoleID == 2 {
		userRole = "Admin"
	}

	var rowClaim, rowIns, rowType, rowSub, rowHosp, rowStatus, rowPat, rowFO sql.NullString
	if caseID > 0 {
		_ = pool.QueryRow(`
			SELECT claim_number, CAST(insurance_company_id AS CHAR), CAST(case_type_id AS CHAR), 
			       CAST(case_sub_type_id AS CHAR), hospital_name, status, patient_name, CAST(fo_id AS CHAR)
			FROM cases WHERE id = ? LIMIT 1
		`, caseID).Scan(&rowClaim, &rowIns, &rowType, &rowSub, &rowHosp, &rowStatus, &rowPat, &rowFO)
	}

	claim := rowClaim.String
	if claim == "" && caseData != nil {
		if v, ok := caseData["claim_number"].(string); ok {
			claim = v
		}
	}

	actionType := "CREATE"
	label := "New case created"
	if actionPerformed == "MODIFIED RECORD" {
		actionType = "UPDATE"
		label = "Case updated"
	} else if actionPerformed == "DELETED RECORD" {
		actionType = "DELETE"
		label = "Case deleted"
	}

	desc := label
	if claim != "" {
		desc += " — Claim: " + claim
	}

	details := []string{
		claim,
		rowIns.String,
		rowType.String,
		rowSub.String,
		rowHosp.String,
		rowStatus.String,
		rowPat.String,
		rowFO.String,
		strconv.FormatInt(caseID, 10),
	}
	detailsJSON, _ := json.Marshal(details)

	ip := c.IP()
	_, _ = pool.Exec(`
		INSERT INTO audit_logs (user_id, user_name, user_role, module_name, action_type, action_performed, table_affected, record_id, claim_number, description, details, ip_address, created_at)
		VALUES (?, ?, ?, 'Cases', ?, ?, 'cases', ?, ?, ?, ?, ?, NOW())
	`, userID, userName, userRole, actionType, actionPerformed, caseID, claim, desc, string(detailsJSON), ip)
}

func getSessionInfo(c *fiber.Ctx) (dashboardSession, string, string, string) {
	var session dashboardSession
	if s, ok := c.Locals("dashboardSession").(dashboardSession); ok {
		session = s
	} else {
		session = dashboardSession{UserID: 1, RoleID: 1, FullName: "Admin User"}
	}
	userName := session.FullName
	if userName == "" {
		userName = "Account"
	}
	userRole := fmt.Sprintf("Role %d", session.RoleID)
	roleMap := map[int]string{
		1: "Super Admin", 2: "Admin", 3: "Case Manager", 4: "Allocation Manager",
		5: "Back Office", 6: "Field Officer", 7: "Area Manager",
	}
	if r, ok := roleMap[session.RoleID]; ok {
		userRole = r
	}
	userInitial := "A"
	if len(strings.TrimSpace(userName)) > 0 {
		userInitial = strings.ToUpper(string([]rune(strings.TrimSpace(userName))[0]))
	}
	return session, userName, userRole, userInitial
}

func populateCommonPlaceholders(html string, userName, userRole, userInitial string) string {
	html = strings.ReplaceAll(html, "{{USER_NAME}}", escapeHTML(userName))
	html = strings.ReplaceAll(html, "{{USER_ROLE}}", escapeHTML(userRole))
	html = strings.ReplaceAll(html, "{{USER_INITIAL}}", escapeHTML(userInitial))
	return html
}

func buildInsurersOptions(pool *sql.DB) string {
	if pool == nil {
		return ""
	}
	rows, err := pool.Query("SELECT id, company_name FROM master_insurance_companies WHERE status='active' ORDER BY company_name ASC")
	if err != nil {
		return ""
	}
	defer rows.Close()
	var sb strings.Builder
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err == nil {
			sb.WriteString(fmt.Sprintf(`<option value="%d">%s</option>`, id, escapeHTML(name)))
		}
	}
	return sb.String()
}

func buildStatesOptions(pool *sql.DB) string {
	if pool == nil {
		return ""
	}
	rows, err := pool.Query("SELECT id, state_name FROM master_states ORDER BY state_name ASC")
	if err != nil {
		return ""
	}
	defer rows.Close()
	var sb strings.Builder
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err == nil {
			sb.WriteString(fmt.Sprintf(`<option value="%d">%s</option>`, id, escapeHTML(name)))
		}
	}
	return sb.String()
}

func buildUsersOptions(pool *sql.DB) string {
	if pool == nil {
		return ""
	}
	rows, err := pool.Query("SELECT id, fullname FROM users WHERE status = 'active' ORDER BY fullname ASC")
	if err != nil {
		return ""
	}
	defer rows.Close()
	var sb strings.Builder
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err == nil {
			sb.WriteString(fmt.Sprintf(`<option value="%d">%s</option>`, id, escapeHTML(name)))
		}
	}
	return sb.String()
}

func buildCaseTypesJSON(pool *sql.DB) string {
	if pool == nil {
		return "[]"
	}
	rows, err := pool.Query("SELECT id, type_name, insurance_company_id FROM master_case_types WHERE status='active'")
	if err != nil {
		return "[]"
	}
	defer rows.Close()
	type Item struct {
		ID                 int64  `json:"id"`
		TypeName           string `json:"type_name"`
		InsuranceCompanyID int64  `json:"insurance_company_id"`
	}
	list := make([]Item, 0, 100)
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.ID, &it.TypeName, &it.InsuranceCompanyID); err == nil {
			list = append(list, it)
		}
	}
	b, _ := json.Marshal(list)
	return string(b)
}

func buildSubTypesJSON(pool *sql.DB) string {
	if pool == nil {
		return "[]"
	}
	rows, err := pool.Query("SELECT id, sub_type_name, case_type_id FROM master_case_sub_types WHERE status='active' ORDER BY sub_type_name ASC")
	if err != nil {
		return "[]"
	}
	defer rows.Close()
	type Item struct {
		ID          int64  `json:"id"`
		SubTypeName string `json:"sub_type_name"`
		CaseTypeID  int64  `json:"case_type_id"`
	}
	list := make([]Item, 0, 500)
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.ID, &it.SubTypeName, &it.CaseTypeID); err == nil {
			list = append(list, it)
		}
	}
	b, _ := json.Marshal(list)
	return string(b)
}

func buildQMOptions(pool *sql.DB) (string, string) {
	if pool == nil {
		return "", ""
	}
	typeRows, err := pool.Query("SELECT MIN(id), type_name FROM master_case_types WHERE status='active' GROUP BY type_name ORDER BY type_name ASC")
	var typeSb strings.Builder
	if err == nil {
		defer typeRows.Close()
		for typeRows.Next() {
			var id int64
			var name string
			if err := typeRows.Scan(&id, &name); err == nil {
				typeSb.WriteString(fmt.Sprintf(`<option value="%d">%s</option>`, id, escapeHTML(name)))
			}
		}
	}
	subRows, err := pool.Query("SELECT MIN(id), sub_type_name FROM master_case_sub_types WHERE status='active' GROUP BY sub_type_name ORDER BY sub_type_name ASC")
	var subSb strings.Builder
	if err == nil {
		defer subRows.Close()
		for subRows.Next() {
			var id int64
			var name string
			if err := subRows.Scan(&id, &name); err == nil {
				subSb.WriteString(fmt.Sprintf(`<option value="%d">%s</option>`, id, escapeHTML(name)))
			}
		}
	}
	return typeSb.String(), subSb.String()
}

func RegisterCaseRoutes(app *fiber.App, pool *sql.DB, requireSession fiber.Handler) {
	ensureHospitalTableExists(pool)
	ensureUserExportPrefTableExists(pool)
	ensureHospitalIdInCases(pool)

	// Page: Case Matrix Add / Edit
	app.Get("/cases/add", requireSession, func(c *fiber.Ctx) error {
		return renderMatrix(c, pool, "")
	})
	app.Get("/cases/matrix", requireSession, func(c *fiber.Ctx) error {
		return renderMatrix(c, pool, "")
	})
	app.Get("/cases/edit/:id", requireSession, func(c *fiber.Ctx) error {
		return renderMatrix(c, pool, c.Params("id"))
	})

	// Page: Master Edit
	app.Get("/master_edit", requireSession, func(c *fiber.Ctx) error {
		return renderMasterEdit(c, pool)
	})

	// Page: Case Reset
	app.Get("/case_reset", requireSession, func(c *fiber.Ctx) error {
		return renderCaseReset(c, pool)
	})

	// Page: Rework Cases
	app.Get("/admin/rework_cases", requireSession, func(c *fiber.Ctx) error {
		return renderRework(c, pool)
	})

	// Page: Case Dustbin / Cleanup
	app.Get("/admin/case_cleanup", requireSession, func(c *fiber.Ctx) error {
		return renderTrash(c, pool)
	})

	// Page: Export Data
	app.Get("/cases/export_data", requireSession, func(c *fiber.Ctx) error {
		return renderExport(c, pool)
	})

	// APIs: Case Operations
	app.Post("/api/cases/get-cities", handleGetCities(pool))
	app.Post("/api/cases/get-mapped-managers", handleGetMappedManagers(pool))
	app.Post("/api/cases/quick-map-manager", handleQuickMapManager(pool))
	app.Post("/api/cases/search-hospital", handleSearchHospital(pool))
	app.Post("/api/cases/search-claim", handleSearchClaim(pool))
	app.Post("/api/master-edit/fetch", handleMasterEditFetch(pool))
	app.Get("/api/cases/fetch/:id", handleCaseFetch(pool))
	app.Post("/api/master-edit/submit", handleMasterEditSubmit(pool))
	app.Post("/api/cases/submit", handleCaseSubmit(pool))
	app.Post("/api/cases/action", handleCaseAction(pool))

	// APIs: Export Operations
	app.All("/api/export/action", handleExportAction(pool))
}

func renderMatrix(c *fiber.Ctx, pool *sql.DB, caseID string) error {
	_, userName, userRole, userInitial := getSessionInfo(c)
	html := string(caseMatrixHTML)
	html = populateCommonPlaceholders(html, userName, userRole, userInitial)

	html = strings.ReplaceAll(html, "{{CASE_ID}}", caseID)
	html = strings.ReplaceAll(html, "{{INSURERS_OPTIONS}}", buildInsurersOptions(pool))
	html = strings.ReplaceAll(html, "{{STATES_OPTIONS}}", buildStatesOptions(pool))
	html = strings.ReplaceAll(html, "{{USERS_OPTIONS}}", buildUsersOptions(pool))
	qmTypes, qmSubs := buildQMOptions(pool)
	html = strings.ReplaceAll(html, "{{QM_TYPES_OPTIONS}}", qmTypes)
	html = strings.ReplaceAll(html, "{{QM_SUBTYPES_OPTIONS}}", qmSubs)
	html = strings.ReplaceAll(html, "{{CASE_TYPES_JSON}}", buildCaseTypesJSON(pool))
	html = strings.ReplaceAll(html, "{{SUB_TYPES_JSON}}", buildSubTypesJSON(pool))

	c.Type("html", "utf-8")
	return c.SendString(html)
}

func renderMasterEdit(c *fiber.Ctx, pool *sql.DB) error {
	_, userName, userRole, userInitial := getSessionInfo(c)
	html := string(caseMasterEditHTML)
	html = populateCommonPlaceholders(html, userName, userRole, userInitial)

	html = strings.ReplaceAll(html, "{{INSURERS_OPTIONS}}", buildInsurersOptions(pool))
	html = strings.ReplaceAll(html, "{{STATES_OPTIONS}}", buildStatesOptions(pool))
	html = strings.ReplaceAll(html, "{{USERS_OPTIONS}}", buildUsersOptions(pool))
	html = strings.ReplaceAll(html, "{{CASE_TYPES_JSON}}", buildCaseTypesJSON(pool))
	html = strings.ReplaceAll(html, "{{SUB_TYPES_JSON}}", buildSubTypesJSON(pool))

	c.Type("html", "utf-8")
	return c.SendString(html)
}

func renderCaseReset(c *fiber.Ctx, pool *sql.DB) error {
	_, userName, userRole, userInitial := getSessionInfo(c)
	html := string(caseResetHTML)
	html = populateCommonPlaceholders(html, userName, userRole, userInitial)
	html = strings.ReplaceAll(html, "{{INSURERS_OPTIONS}}", buildInsurersOptions(pool))

	c.Type("html", "utf-8")
	return c.SendString(html)
}

func renderRework(c *fiber.Ctx, pool *sql.DB) error {
	_, userName, userRole, userInitial := getSessionInfo(c)
	html := string(caseReworkHTML)
	html = populateCommonPlaceholders(html, userName, userRole, userInitial)
	html = strings.ReplaceAll(html, "{{INSURERS_OPTIONS}}", buildInsurersOptions(pool))

	c.Type("html", "utf-8")
	return c.SendString(html)
}

func renderTrash(c *fiber.Ctx, pool *sql.DB) error {
	_, userName, userRole, userInitial := getSessionInfo(c)
	html := string(caseTrashHTML)
	html = populateCommonPlaceholders(html, userName, userRole, userInitial)
	html = strings.ReplaceAll(html, "{{INSURERS_OPTIONS}}", buildInsurersOptions(pool))

	c.Type("html", "utf-8")
	return c.SendString(html)
}

func renderExport(c *fiber.Ctx, pool *sql.DB) error {
	session, userName, userRole, userInitial := getSessionInfo(c)
	html := string(caseExportHTML)
	html = populateCommonPlaceholders(html, userName, userRole, userInitial)

	// Distinct insurers from active cases
	var insSb, statesSb, hospSb, amSb, almSb strings.Builder
	if pool != nil {
		rows, err := pool.Query(`SELECT MIN(ic.id) as id, ic.company_name FROM cases c JOIN master_insurance_companies ic ON c.insurance_company_id = ic.id WHERE c.deleted_at IS NULL AND c.claim_number IS NOT NULL AND TRIM(c.claim_number) != '' GROUP BY ic.company_name ORDER BY ic.company_name ASC`)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var id int64
				var name string
				if err := rows.Scan(&id, &name); err == nil {
					insSb.WriteString(fmt.Sprintf(`<option value="%d">%s</option>`, id, escapeHTML(name)))
				}
			}
		}

		rowsS, err := pool.Query(`SELECT MIN(s.id) as id, s.state_name FROM cases c JOIN master_states s ON c.state_id = s.id WHERE c.deleted_at IS NULL AND c.claim_number IS NOT NULL AND TRIM(c.claim_number) != '' GROUP BY s.state_name ORDER BY s.state_name ASC`)
		if err == nil {
			defer rowsS.Close()
			for rowsS.Next() {
				var id int64
				var name string
				if err := rowsS.Scan(&id, &name); err == nil {
					statesSb.WriteString(fmt.Sprintf(`<option value="%d">%s</option>`, id, escapeHTML(name)))
				}
			}
		}

		rowsH, err := pool.Query(`SELECT hospital_name FROM cases WHERE hospital_name IS NOT NULL AND hospital_name != '' AND deleted_at IS NULL AND claim_number IS NOT NULL AND TRIM(claim_number) != '' GROUP BY hospital_name ORDER BY hospital_name ASC`)
		if err == nil {
			defer rowsH.Close()
			for rowsH.Next() {
				var name string
				if err := rowsH.Scan(&name); err == nil {
					hospSb.WriteString(fmt.Sprintf(`<option value="%s">%s</option>`, escapeHTML(name), escapeHTML(name)))
				}
			}
		}

		rowsAM, err := pool.Query(`SELECT MIN(u.id) as id, u.fullname FROM cases c JOIN users u ON c.cm_id = u.id WHERE c.deleted_at IS NULL AND c.claim_number IS NOT NULL AND TRIM(c.claim_number) != '' GROUP BY u.fullname ORDER BY u.fullname ASC`)
		if err == nil {
			defer rowsAM.Close()
			for rowsAM.Next() {
				var id int64
				var name string
				if err := rowsAM.Scan(&id, &name); err == nil {
					amSb.WriteString(fmt.Sprintf(`<option value="%d">%s</option>`, id, escapeHTML(name)))
				}
			}
		}

		rowsALM, err := pool.Query(`SELECT MIN(u.id) as id, u.fullname FROM cases c JOIN users u ON c.allocation_manager_id = u.id WHERE c.deleted_at IS NULL AND c.claim_number IS NOT NULL AND TRIM(c.claim_number) != '' GROUP BY u.fullname ORDER BY u.fullname ASC`)
		if err == nil {
			defer rowsALM.Close()
			for rowsALM.Next() {
				var id int64
				var name string
				if err := rowsALM.Scan(&id, &name); err == nil {
					almSb.WriteString(fmt.Sprintf(`<option value="%d">%s</option>`, id, escapeHTML(name)))
				}
			}
		}
	}

	savedColsJSON := "null"
	if pool != nil {
		var rawCols string
		if err := pool.QueryRow("SELECT selected_columns FROM user_export_preferences WHERE user_id = ?", session.UserID).Scan(&rawCols); err == nil && rawCols != "" {
			savedColsJSON = rawCols
		}
	}

	html = strings.ReplaceAll(html, "{{INSURERS_OPTIONS}}", insSb.String())
	html = strings.ReplaceAll(html, "{{STATES_OPTIONS}}", statesSb.String())
	html = strings.ReplaceAll(html, "{{HOSPITALS_OPTIONS}}", hospSb.String())
	html = strings.ReplaceAll(html, "{{AMS_OPTIONS}}", amSb.String())
	html = strings.ReplaceAll(html, "{{ALMS_OPTIONS}}", almSb.String())
	html = strings.ReplaceAll(html, "{{SAVED_EXPORT_COLS_JSON}}", savedColsJSON)

	c.Type("html", "utf-8")
	return c.SendString(html)
}

func handleGetCities(pool *sql.DB) fiber.Handler {
	return func(c *fiber.Ctx) error {
		stateID, _ := strconv.Atoi(c.FormValue("state_id"))
		options := "<option value=''>-- Select City/District --</option>"
		if pool != nil && stateID > 0 {
			rows, err := pool.Query("SELECT id, district_name FROM master_districts WHERE state_id = ? ORDER BY district_name ASC", stateID)
			if err == nil {
				defer rows.Close()
				var sb strings.Builder
				sb.WriteString(options)
				for rows.Next() {
					var id int
					var name string
					if err := rows.Scan(&id, &name); err == nil {
						sb.WriteString(fmt.Sprintf("<option value='%d'>%s</option>", id, escapeHTML(name)))
					}
				}
				options = sb.String()
			}
		}
		c.Type("html", "utf-8")
		return c.SendString(options)
	}
}

func handleGetMappedManagers(pool *sql.DB) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if pool == nil {
			return c.JSON(fiber.Map{"status": "error", "message": "Database unavailable"})
		}
		insuranceID, _ := strconv.Atoi(c.FormValue("insurance_id"))
		typeID, _ := strconv.Atoi(c.FormValue("type_id"))
		subTypeID, _ := strconv.Atoi(c.FormValue("sub_type_id"))
		stateID, _ := strconv.Atoi(c.FormValue("hosp_state_id"))
		cityID, _ := strconv.Atoi(c.FormValue("hosp_city_id"))

		type Manager struct {
			ID       int64  `json:"id"`
			FullName string `json:"fullname"`
		}

		// AMs
		ams := make([]Manager, 0)
		if stateID > 0 {
			rows, err := pool.Query(`
				SELECT DISTINCT u.id, u.fullname 
				FROM users u
				JOIN master_geo_area_mapping map ON u.id = map.area_manager_id
				WHERE u.status = 'active' 
				  AND map.status = 'active' 
				  AND map.state_id = ?
				  AND (map.district_id = ? OR map.district_id = 0 OR map.district_id IS NULL)
				ORDER BY u.fullname ASC
			`, stateID, cityID)
			if err == nil {
				defer rows.Close()
				for rows.Next() {
					var m Manager
					if err := rows.Scan(&m.ID, &m.FullName); err == nil {
						ams = append(ams, m)
					}
				}
			}
		}

		// ICMs
		icms := make([]Manager, 0)
		if insuranceID > 0 {
			rows, err := pool.Query(`
				SELECT DISTINCT u.id, u.fullname 
				FROM users u
				JOIN icm_insurance_map map ON u.id = map.icm_id
				WHERE u.status = 'active' AND map.status = 'active' AND map.insurance_company_id = ?
				ORDER BY u.fullname ASC
			`, insuranceID)
			if err == nil {
				defer rows.Close()
				for rows.Next() {
					var m Manager
					if err := rows.Scan(&m.ID, &m.FullName); err == nil {
						icms = append(icms, m)
					}
				}
			}
		}

		// ALMs
		alms := make([]Manager, 0)
		if stateID > 0 || insuranceID > 0 || typeID > 0 {
			rows, err := pool.Query(`
				SELECT DISTINCT u.id, u.fullname 
				FROM users u
				JOIN master_geo_alloc_mapping map ON u.id = map.allocation_manager_id
				WHERE u.status = 'active'
				  AND (map.state_id = ? OR map.state_id IS NULL OR map.state_id = 0)
				  AND (map.district_id = ? OR map.district_id IS NULL OR map.district_id = 0)
				  AND (map.insurance_company_id = ? OR map.insurance_company_id IS NULL OR map.insurance_company_id = 0)
				  AND (map.category_id = ? OR map.category_id IS NULL OR map.category_id = 0)
				  AND (map.subcategory_id = ? OR map.subcategory_id IS NULL OR map.subcategory_id = 0)
				ORDER BY u.fullname ASC
			`, stateID, cityID, insuranceID, typeID, subTypeID)
			if err == nil {
				defer rows.Close()
				for rows.Next() {
					var m Manager
					if err := rows.Scan(&m.ID, &m.FullName); err == nil {
						alms = append(alms, m)
					}
				}
			}
		}

		// RMs
		rms := make([]Manager, 0)
		if insuranceID > 0 {
			rows, err := pool.Query(`
				SELECT DISTINCT u.id, u.fullname 
				FROM users u
				JOIN rm_insurance_map map ON u.id = map.rm_id
				WHERE u.status = 'active' AND map.insurance_company_id = ?
				ORDER BY u.fullname ASC
			`, insuranceID)
			if err == nil {
				defer rows.Close()
				for rows.Next() {
					var m Manager
					if err := rows.Scan(&m.ID, &m.FullName); err == nil {
						rms = append(rms, m)
					}
				}
			}
		}

		return c.JSON(fiber.Map{
			"status": "success",
			"ams":    ams,
			"icms":   icms,
			"alms":   alms,
			"rms":    rms,
		})
	}
}

func handleQuickMapManager(pool *sql.DB) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if pool == nil {
			return c.JSON(fiber.Map{"status": "error", "message": "Database unavailable"})
		}
		roleType := c.FormValue("role_type")
		userID, _ := strconv.Atoi(c.FormValue("user_id"))
		insuranceID, _ := strconv.Atoi(c.FormValue("insurance_id"))
		typeID, _ := strconv.Atoi(c.FormValue("type_id"))
		subTypeID, _ := strconv.Atoi(c.FormValue("sub_type_id"))
		stateID, _ := strconv.Atoi(c.FormValue("state_id"))
		cityID, _ := strconv.Atoi(c.FormValue("city_id"))

		if userID <= 0 {
			return c.JSON(fiber.Map{"status": "error", "message": "Invalid User ID for mapping."})
		}

		var err error
		switch roleType {
		case "ICM":
			if insuranceID > 0 {
				_, err = pool.Exec("INSERT INTO icm_insurance_map (icm_id, insurance_company_id, status) VALUES (?, ?, 'active')", userID, insuranceID)
			}
		case "AM":
			if stateID > 0 {
				dist := cityID
				if dist <= 0 {
					dist = 0
				}
				_, err = pool.Exec("INSERT INTO master_geo_area_mapping (area_manager_id, state_id, district_id, status) VALUES (?, ?, ?, 'active')", userID, stateID, dist)
			}
		case "ALM":
			var sVal, dVal, iVal, tVal, stVal any
			if stateID > 0 {
				sVal = stateID
			}
			if cityID > 0 {
				dVal = cityID
			}
			if insuranceID > 0 {
				iVal = insuranceID
			}
			if typeID > 0 {
				tVal = typeID
			}
			if subTypeID > 0 {
				stVal = subTypeID
			}
			_, err = pool.Exec(`
				INSERT INTO master_geo_alloc_mapping (allocation_manager_id, state_id, district_id, insurance_company_id, category_id, subcategory_id)
				VALUES (?, ?, ?, ?, ?, ?)
			`, userID, sVal, dVal, iVal, tVal, stVal)
		case "RM":
			if insuranceID > 0 {
				_, err = pool.Exec("INSERT IGNORE INTO rm_insurance_map (rm_id, insurance_company_id) VALUES (?, ?)", userID, insuranceID)
			}
		}

		if err != nil {
			return c.JSON(fiber.Map{"status": "error", "message": err.Error()})
		}
		return c.JSON(fiber.Map{"status": "success", "message": "Quick Manager Mapping Saved Successfully!"})
	}
}

func handleSearchHospital(pool *sql.DB) fiber.Handler {
	return func(c *fiber.Ctx) error {
		term := strings.TrimSpace(c.FormValue("term"))
		if len(term) < 1 || pool == nil {
			return c.JSON(fiber.Map{"status": "success", "data": []any{}})
		}
		searchParam := "%" + term + "%"

		type HospItem struct {
			ID              int64          `json:"id"`
			HospitalName    string         `json:"hospital_name"`
			HospitalAddress sql.NullString `json:"hospital_address"`
			StateID         sql.NullInt64  `json:"state_id"`
			DistrictID      sql.NullInt64  `json:"district_id"`
			PinCode         sql.NullString `json:"pin_code"`
			RegistrationNo  sql.NullString `json:"registration_no"`
			StateName       sql.NullString `json:"state_name"`
			DistrictName    sql.NullString `json:"district_name"`
		}

		rows, err := pool.Query(`
			SELECT h.id, h.hospital_name, h.hospital_address, h.state_id, h.district_id, h.pin_code, h.registration_no,
			       s.state_name, d.district_name 
			FROM master_hospitals h
			LEFT JOIN master_states s ON h.state_id = s.id
			LEFT JOIN master_districts d ON h.district_id = d.id
			WHERE h.hospital_name LIKE ? AND h.status = 'active' LIMIT 15
		`, searchParam)
		resList := make([]map[string]any, 0)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var h HospItem
				if err := rows.Scan(&h.ID, &h.HospitalName, &h.HospitalAddress, &h.StateID, &h.DistrictID, &h.PinCode, &h.RegistrationNo, &h.StateName, &h.DistrictName); err == nil {
					resList = append(resList, map[string]any{
						"id":               h.ID,
						"hospital_name":    h.HospitalName,
						"hospital_address": h.HospitalAddress.String,
						"state_id":         h.StateID.Int64,
						"district_id":      h.DistrictID.Int64,
						"pin_code":         h.PinCode.String,
						"registration_no":  h.RegistrationNo.String,
						"state_name":       h.StateName.String,
						"district_name":    h.DistrictName.String,
					})
				}
			}
		}

		if len(resList) == 0 {
			rowsC, err := pool.Query(`
				SELECT 0 as id, c.hospital_name, c.hospital_address, c.state_id, c.city_id as district_id, c.hospital_pin_code as pin_code, c.hospital_reg_no as registration_no,
				       s.state_name, d.district_name 
				FROM cases c
				LEFT JOIN master_states s ON c.state_id = s.id
				LEFT JOIN master_districts d ON c.city_id = d.id
				WHERE c.hospital_name LIKE ? AND c.hospital_name IS NOT NULL
				GROUP BY c.hospital_name, c.state_id, c.city_id LIMIT 15
			`, searchParam)
			if err == nil {
				defer rowsC.Close()
				for rowsC.Next() {
					var h HospItem
					if err := rowsC.Scan(&h.ID, &h.HospitalName, &h.HospitalAddress, &h.StateID, &h.DistrictID, &h.PinCode, &h.RegistrationNo, &h.StateName, &h.DistrictName); err == nil {
						resList = append(resList, map[string]any{
							"id":               h.ID,
							"hospital_name":    h.HospitalName,
							"hospital_address": h.HospitalAddress.String,
							"state_id":         h.StateID.Int64,
							"district_id":      h.DistrictID.Int64,
							"pin_code":         h.PinCode.String,
							"registration_no":  h.RegistrationNo.String,
							"state_name":       h.StateName.String,
							"district_name":    h.DistrictName.String,
						})
					}
				}
			}
		}

		return c.JSON(fiber.Map{"status": "success", "data": resList})
	}
}

func handleSearchClaim(pool *sql.DB) fiber.Handler {
	return func(c *fiber.Ctx) error {
		term := strings.TrimSpace(c.FormValue("term"))
		if len(term) < 1 || pool == nil {
			return c.JSON(fiber.Map{"status": "success", "data": []string{}})
		}
		rows, err := pool.Query("SELECT claim_number FROM cases WHERE claim_number LIKE ? LIMIT 10", "%"+term+"%")
		res := make([]string, 0)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var claim string
				if err := rows.Scan(&claim); err == nil {
					res = append(res, claim)
				}
			}
		}
		return c.JSON(fiber.Map{"status": "success", "data": res})
	}
}

func fetchCaseMap(pool *sql.DB, query string, args ...any) (map[string]any, error) {
	rows, err := pool.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	if !rows.Next() {
		return nil, sql.ErrNoRows
	}
	values := make([]any, len(cols))
	valuePtrs := make([]any, len(cols))
	for i := range values {
		valuePtrs[i] = &values[i]
	}
	if err := rows.Scan(valuePtrs...); err != nil {
		return nil, err
	}
	entry := make(map[string]any)
	for i, col := range cols {
		val := values[i]
		if b, ok := val.([]byte); ok {
			entry[col] = string(b)
		} else {
			entry[col] = val
		}
	}
	return entry, nil
}

func handleMasterEditFetch(pool *sql.DB) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if pool == nil {
			return c.JSON(fiber.Map{"status": "error", "message": "Database unavailable"})
		}
		claimNo := strings.TrimSpace(c.FormValue("claim_number"))
		insID := strings.TrimSpace(c.FormValue("insurance_company_id"))
		if claimNo == "" {
			return c.JSON(fiber.Map{"status": "error", "message": "Claim Number is required."})
		}

		query := `
			SELECT c.*, ic.company_name, t.type_name, st.sub_type_name
			FROM cases c
			LEFT JOIN master_insurance_companies ic ON c.insurance_company_id = ic.id
			LEFT JOIN master_case_types t ON c.case_type_id = t.id
			LEFT JOIN master_case_sub_types st ON c.case_sub_type_id = st.id
			WHERE TRIM(c.claim_number) = ?
		`
		args := []any{claimNo}
		if insID != "" {
			query += " AND c.insurance_company_id = ?"
			args = append(args, insID)
		}
		query += " ORDER BY c.id DESC LIMIT 1"

		caseData, err := fetchCaseMap(pool, query, args...)
		if err != nil {
			return c.JSON(fiber.Map{"status": "error", "message": "No case record found for Claim Number: " + claimNo})
		}
		return c.JSON(fiber.Map{"status": "success", "data": caseData})
	}
}

func handleCaseFetch(pool *sql.DB) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if pool == nil {
			return c.JSON(fiber.Map{"status": "error", "message": "Database unavailable"})
		}
		id := c.Params("id")
		query := `
			SELECT c.*, ic.company_name, t.type_name, st.sub_type_name
			FROM cases c
			LEFT JOIN master_insurance_companies ic ON c.insurance_company_id = ic.id
			LEFT JOIN master_case_types t ON c.case_type_id = t.id
			LEFT JOIN master_case_sub_types st ON c.case_sub_type_id = st.id
			WHERE c.id = ? LIMIT 1
		`
		caseData, err := fetchCaseMap(pool, query, id)
		if err != nil {
			return c.JSON(fiber.Map{"status": "error", "message": "Target case not found."})
		}
		return c.JSON(fiber.Map{"status": "success", "data": caseData})
	}
}

func handleMasterEditSubmit(pool *sql.DB) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if pool == nil {
			return c.JSON(fiber.Map{"status": "error", "message": "Database unavailable"})
		}
		session, _, _, _ := getSessionInfo(c)
		caseID, _ := strconv.ParseInt(c.FormValue("case_id"), 10, 64)
		if caseID <= 0 {
			return c.JSON(fiber.Map{"status": "error", "message": "Target Case ID is required."})
		}

		ensureHospitalIdInCases(pool)
		hospName := strings.TrimSpace(c.FormValue("hospital_name"))
		var hospID any
		if hospName != "" {
			var existingID int64
			err := pool.QueryRow("SELECT id FROM master_hospitals WHERE hospital_name = ?", hospName).Scan(&existingID)
			if err != nil {
				res, insErr := pool.Exec(`
					INSERT INTO master_hospitals (hospital_name, hospital_address, state_id, district_id, pin_code, registration_no, status, created_at)
					VALUES (?, ?, ?, ?, ?, ?, 'active', NOW())
				`, hospName, strings.TrimSpace(c.FormValue("hospital_address")), nullableInt(c.FormValue("state_id")), nullableInt(c.FormValue("city_id")), strings.TrimSpace(c.FormValue("pin_code")), strings.TrimSpace(c.FormValue("hospital_reg_no")))
				if insErr == nil {
					newHospID, _ := res.LastInsertId()
					hospID = newHospID
				}
			} else {
				hospID = existingID
			}
		}

		query := `
			UPDATE cases SET 
				claim_number = ?, insurance_company_id = ?, case_type_id = ?, case_sub_type_id = ?,
				patient_name = ?, patient_mobile = ?, patient_aadhar = ?, patient_age = ?, gender = ?,
				relationship_with_insured = ?, patient_occupation = ?, patient_address = ?, patient_state_id = ?,
				patient_city_id = ?, patient_pin_code = ?, other_details = ?, diagnosis = ?, trigger_reason = ?,
				insured_name = ?, insured_mobile = ?, policy_no = ?, policy_type = ?, assured_amount = ?, claim_amount = ?,
				portability_details = ?, previous_claim_history = ?, hospital_name = ?, hospital_id = ?, hospital_address = ?,
				hospital_reg_no = ?, state_id = ?, city_id = ?, hospital_pin_code = ?, hospital_lat = ?, hospital_lng = ?,
				mlc_number = ?, accidental_claim = ?, plan_of_management = ?, collected_documents = ?, geographical_distance = ?,
				cm_id = ?, icm_id = ?, allocation_manager_id = ?, report_maker_id = ?, fo_id = ?, fo_hospital_id = ?,
				fo_patient_id = ?, fo_pharmacy_id = ?, fo_pathology_id = ?, fo_doctor_id = ?, modified_by = ?
		`
		args := []any{
			strings.TrimSpace(c.FormValue("claim_number")),
			nullableInt(c.FormValue("insurance_id")),
			nullableInt(c.FormValue("type_id")),
			nullableInt(c.FormValue("sub_type_id")),
			strings.TrimSpace(c.FormValue("patient_name")),
			strings.TrimSpace(c.FormValue("patient_mobile")),
			strings.TrimSpace(c.FormValue("patient_aadhar")),
			strings.TrimSpace(c.FormValue("patient_age")),
			nullableStr(c.FormValue("gender")),
			strings.TrimSpace(c.FormValue("relationship_with_insured")),
			strings.TrimSpace(c.FormValue("patient_occupation")),
			strings.TrimSpace(c.FormValue("patient_address")),
			nullableInt(c.FormValue("patient_state_id")),
			nullableInt(c.FormValue("patient_city_id")),
			strings.TrimSpace(c.FormValue("patient_pin_code")),
			strings.TrimSpace(c.FormValue("other_details")),
			strings.TrimSpace(c.FormValue("diagnosis")),
			strings.TrimSpace(c.FormValue("trigger_reason")),
			strings.TrimSpace(c.FormValue("insured_name")),
			strings.TrimSpace(c.FormValue("insured_mobile")),
			strings.TrimSpace(c.FormValue("policy_no")),
			strings.TrimSpace(c.FormValue("policy_type")),
			nullableFloat(c.FormValue("assured_amount")),
			nullableFloat(c.FormValue("claim_amount")),
			strings.TrimSpace(c.FormValue("portability_details")),
			strings.TrimSpace(c.FormValue("previous_claim_history")),
			hospName,
			hospID,
			strings.TrimSpace(c.FormValue("hospital_address")),
			strings.TrimSpace(c.FormValue("hospital_reg_no")),
			nullableInt(c.FormValue("state_id")),
			nullableInt(c.FormValue("city_id")),
			strings.TrimSpace(c.FormValue("pin_code")),
			strings.TrimSpace(c.FormValue("hospital_lat")),
			strings.TrimSpace(c.FormValue("hospital_lng")),
			strings.TrimSpace(c.FormValue("mlc_number")),
			nullableStr(c.FormValue("accidental_claim")),
			strings.TrimSpace(c.FormValue("plan_of_management")),
			strings.TrimSpace(c.FormValue("collected_documents")),
			strings.TrimSpace(c.FormValue("geographical_distance")),
			nullableInt(c.FormValue("cm_id")),
			nullableInt(c.FormValue("icm_id")),
			nullableInt(c.FormValue("alm_id")),
			nullableInt(c.FormValue("report_maker_id")),
			nullableInt(c.FormValue("fo_id")),
			nullableInt(c.FormValue("fo_hospital_id")),
			nullableInt(c.FormValue("fo_patient_id")),
			nullableInt(c.FormValue("fo_pharmacy_id")),
			nullableInt(c.FormValue("fo_pathology_id")),
			nullableInt(c.FormValue("fo_doctor_id")),
			session.UserID,
		}

		if v := c.FormValue("allocation_date"); v != "" {
			query += ", allocation_date = ?"
			args = append(args, v)
		}
		if v := c.FormValue("status"); v != "" {
			query += ", status = ?"
			args = append(args, v)
		}
		if v := c.FormValue("case_verdict"); v != "" {
			query += ", case_verdict = ?"
			args = append(args, v)
		}
		if v := c.FormValue("final_remark"); v != "" {
			query += ", final_remark = ?"
			args = append(args, v)
		}
		if v := c.FormValue("fo_final_remark"); v != "" {
			query += ", fo_final_remark = ?"
			args = append(args, v)
		}

		query += " WHERE id = ?"
		args = append(args, caseID)

		_, err := pool.Exec(query, args...)
		if err != nil {
			return c.JSON(fiber.Map{"status": "error", "message": "Master Save Error: " + err.Error()})
		}

		writeCaseAuditLog(pool, c, session, "MODIFIED RECORD", caseID, map[string]any{"claim_number": c.FormValue("claim_number")})
		return c.JSON(fiber.Map{"status": "success", "message": "Master Case Details Updated Successfully!"})
	}
}

func handleCaseSubmit(pool *sql.DB) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if pool == nil {
			return c.JSON(fiber.Map{"status": "error", "message": "Database unavailable"})
		}
		session, _, _, _ := getSessionInfo(c)
		caseID, _ := strconv.ParseInt(c.FormValue("case_id"), 10, 64)
		claimNo := strings.TrimSpace(c.FormValue("claim_number"))

		if claimNo == "" {
			return c.JSON(fiber.Map{"status": "error", "message": "Claim Number is required."})
		}

		ensureHospitalIdInCases(pool)
		hospName := strings.TrimSpace(c.FormValue("hospital_name"))
		var hospID any
		if hospName != "" {
			var existingID int64
			err := pool.QueryRow("SELECT id FROM master_hospitals WHERE hospital_name = ?", hospName).Scan(&existingID)
			if err != nil {
				res, insErr := pool.Exec(`
					INSERT INTO master_hospitals (hospital_name, hospital_address, state_id, district_id, pin_code, registration_no, status, created_at)
					VALUES (?, ?, ?, ?, ?, ?, 'active', NOW())
				`, hospName, strings.TrimSpace(c.FormValue("hospital_address")), nullableInt(c.FormValue("state_id")), nullableInt(c.FormValue("city_id")), strings.TrimSpace(c.FormValue("pin_code")), strings.TrimSpace(c.FormValue("hospital_reg_no")))
				if insErr == nil {
					newHospID, _ := res.LastInsertId()
					hospID = newHospID
				}
			} else {
				hospID = existingID
			}
		}

		cmID := nullableInt(c.FormValue("cm_id"))
		if cmID == nil {
			cmID = nullableInt(c.FormValue("am_id"))
		}

		allocDate := c.FormValue("allocation_date")
		if allocDate == "" {
			allocDate = time.Now().Format("2006-01-02 15:04:05")
		}

		if caseID > 0 {
			// UPDATE
			query := `
				UPDATE cases SET 
					claim_number = ?, insurance_company_id = ?, case_type_id = ?, case_sub_type_id = ?,
					patient_name = ?, patient_mobile = ?, patient_aadhar = ?, patient_address = ?,
					patient_state_id = ?, patient_city_id = ?, patient_pin_code = ?, patient_age = ?,
					gender = ?, patient_occupation = ?, relationship_with_insured = ?, insured_name = ?,
					insured_mobile = ?, policy_no = ?, policy_type = ?, assured_amount = ?, claim_amount = ?,
					portability_details = ?, previous_claim_history = ?, hospital_name = ?, hospital_id = ?,
					hospital_address = ?, hospital_reg_no = ?, state_id = ?, city_id = ?, hospital_pin_code = ?,
					hospital_lat = ?, hospital_lng = ?, mlc_number = ?, accidental_claim = ?, plan_of_management = ?,
					collected_documents = ?, geographical_distance = ?, diagnosis = ?, trigger_reason = ?,
					cm_id = ?, icm_id = ?, allocation_manager_id = ?, allocation_date = ?, modified_by = ?
				WHERE id = ?
			`
			_, err := pool.Exec(query,
				claimNo, nullableInt(c.FormValue("insurance_id")), nullableInt(c.FormValue("type_id")), nullableInt(c.FormValue("sub_type_id")),
				strings.TrimSpace(c.FormValue("patient_name")), strings.TrimSpace(c.FormValue("patient_mobile")), strings.TrimSpace(c.FormValue("patient_aadhar")), strings.TrimSpace(c.FormValue("patient_address")),
				nullableInt(c.FormValue("patient_state_id")), nullableInt(c.FormValue("patient_city_id")), strings.TrimSpace(c.FormValue("patient_pin_code")), strings.TrimSpace(c.FormValue("patient_age")),
				nullableStr(c.FormValue("gender")), strings.TrimSpace(c.FormValue("patient_occupation")), strings.TrimSpace(c.FormValue("relationship_with_insured")), strings.TrimSpace(c.FormValue("insured_name")),
				strings.TrimSpace(c.FormValue("insured_mobile")), strings.TrimSpace(c.FormValue("policy_no")), strings.TrimSpace(c.FormValue("policy_type")), nullableFloat(c.FormValue("assured_amount")), nullableFloat(c.FormValue("claim_amount")),
				strings.TrimSpace(c.FormValue("portability_details")), strings.TrimSpace(c.FormValue("previous_claim_history")), hospName, hospID,
				strings.TrimSpace(c.FormValue("hospital_address")), strings.TrimSpace(c.FormValue("hospital_reg_no")), nullableInt(c.FormValue("state_id")), nullableInt(c.FormValue("city_id")), strings.TrimSpace(c.FormValue("pin_code")),
				c.FormValue("hospital_lat"), c.FormValue("hospital_lng"), strings.TrimSpace(c.FormValue("mlc_number")), nullableStr(c.FormValue("accidental_claim")), strings.TrimSpace(c.FormValue("plan_of_management")),
				strings.TrimSpace(c.FormValue("collected_documents")), strings.TrimSpace(c.FormValue("geographical_distance")), strings.TrimSpace(c.FormValue("diagnosis")), strings.TrimSpace(c.FormValue("trigger_reason")),
				cmID, nullableInt(c.FormValue("icm_id")), nullableInt(c.FormValue("alm_id")), allocDate, session.UserID,
				caseID,
			)
			if err != nil {
				return c.JSON(fiber.Map{"status": "error", "message": "Database Save Error: " + err.Error()})
			}
			writeCaseAuditLog(pool, c, session, "MODIFIED RECORD", caseID, map[string]any{"claim_number": claimNo})
			return c.JSON(fiber.Map{"status": "success", "message": "Case Record Updated Successfully!", "id": caseID})
		} else {
			// INSERT
			var existingID int64
			err := pool.QueryRow("SELECT id FROM cases WHERE claim_number = ?", claimNo).Scan(&existingID)
			if err == nil && c.FormValue("is_rework") != "1" && c.FormValue("is_rework") != "true" {
				return c.JSON(fiber.Map{"status": "error", "message": "Claim Number already exists!"})
			}

			insertSQL := `
				INSERT INTO cases (
					claim_number, insurance_company_id, case_type_id, case_sub_type_id,
					patient_name, patient_mobile, patient_aadhar, patient_address,
					patient_state_id, patient_city_id, patient_pin_code, patient_age,
					gender, patient_occupation, relationship_with_insured, insured_name,
					insured_mobile, policy_no, policy_type, assured_amount, claim_amount,
					portability_details, previous_claim_history, hospital_name, hospital_id,
					hospital_address, hospital_reg_no, state_id, city_id, hospital_pin_code,
					hospital_lat, hospital_lng, mlc_number, accidental_claim, plan_of_management,
					collected_documents, geographical_distance, diagnosis, trigger_reason,
					cm_id, icm_id, allocation_manager_id, allocation_date, status, created_by, created_at
				) VALUES (
					?, ?, ?, ?,
					?, ?, ?, ?,
					?, ?, ?, ?,
					?, ?, ?, ?,
					?, ?, ?, ?, ?,
					?, ?, ?, ?,
					?, ?, ?, ?, ?,
					?, ?, ?, ?, ?,
					?, ?, ?, ?,
					?, ?, ?, ?, 'OPEN', ?, NOW()
				)
			`
			res, err := pool.Exec(insertSQL,
				claimNo, nullableInt(c.FormValue("insurance_id")), nullableInt(c.FormValue("type_id")), nullableInt(c.FormValue("sub_type_id")),
				strings.TrimSpace(c.FormValue("patient_name")), strings.TrimSpace(c.FormValue("patient_mobile")), strings.TrimSpace(c.FormValue("patient_aadhar")), strings.TrimSpace(c.FormValue("patient_address")),
				nullableInt(c.FormValue("patient_state_id")), nullableInt(c.FormValue("patient_city_id")), strings.TrimSpace(c.FormValue("patient_pin_code")), strings.TrimSpace(c.FormValue("patient_age")),
				nullableStr(c.FormValue("gender")), strings.TrimSpace(c.FormValue("patient_occupation")), strings.TrimSpace(c.FormValue("relationship_with_insured")), strings.TrimSpace(c.FormValue("insured_name")),
				strings.TrimSpace(c.FormValue("insured_mobile")), strings.TrimSpace(c.FormValue("policy_no")), strings.TrimSpace(c.FormValue("policy_type")), nullableFloat(c.FormValue("assured_amount")), nullableFloat(c.FormValue("claim_amount")),
				strings.TrimSpace(c.FormValue("portability_details")), strings.TrimSpace(c.FormValue("previous_claim_history")), hospName, hospID,
				strings.TrimSpace(c.FormValue("hospital_address")), strings.TrimSpace(c.FormValue("hospital_reg_no")), nullableInt(c.FormValue("state_id")), nullableInt(c.FormValue("city_id")), strings.TrimSpace(c.FormValue("pin_code")),
				c.FormValue("hospital_lat"), c.FormValue("hospital_lng"), strings.TrimSpace(c.FormValue("mlc_number")), nullableStr(c.FormValue("accidental_claim")), strings.TrimSpace(c.FormValue("plan_of_management")),
				strings.TrimSpace(c.FormValue("collected_documents")), strings.TrimSpace(c.FormValue("geographical_distance")), strings.TrimSpace(c.FormValue("diagnosis")), strings.TrimSpace(c.FormValue("trigger_reason")),
				cmID, nullableInt(c.FormValue("icm_id")), nullableInt(c.FormValue("alm_id")), allocDate, session.UserID,
			)
			if err != nil {
				return c.JSON(fiber.Map{"status": "error", "message": "Database Save Error: " + err.Error()})
			}
			newID, _ := res.LastInsertId()
			writeCaseAuditLog(pool, c, session, "RECORD INSERTED", newID, map[string]any{"claim_number": claimNo})
			return c.JSON(fiber.Map{"status": "success", "message": "Case Registered Successfully!", "id": newID})
		}
	}
}

func handleCaseAction(pool *sql.DB) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if pool == nil {
			return c.JSON(fiber.Map{"status": "error", "message": "Database unavailable"})
		}
		action := c.FormValue("action")
		claimNumber := strings.TrimSpace(c.FormValue("claim_number"))
		insID := strings.TrimSpace(c.FormValue("insurance_id"))
		caseIDStr := c.FormValue("id")
		if caseIDStr == "" {
			caseIDStr = c.FormValue("case_id")
		}

		var targetID int64
		var targetClaim string
		var oldStatus, oldVerdict, oldRemark, oldClosingDate sql.NullString

		if caseIDStr != "" {
			_ = pool.QueryRow("SELECT id, claim_number, status, case_verdict, final_remark, report_submission_date FROM cases WHERE id = ? LIMIT 1", caseIDStr).
				Scan(&targetID, &targetClaim, &oldStatus, &oldVerdict, &oldRemark, &oldClosingDate)
		} else if claimNumber != "" {
			q := "SELECT id, claim_number, status, case_verdict, final_remark, report_submission_date FROM cases WHERE TRIM(claim_number) = ?"
			args := []any{claimNumber}
			if insID != "" {
				q += " AND insurance_company_id = ?"
				args = append(args, insID)
			}
			q += " ORDER BY id DESC LIMIT 1"
			_ = pool.QueryRow(q, args...).Scan(&targetID, &targetClaim, &oldStatus, &oldVerdict, &oldRemark, &oldClosingDate)
		}

		if targetID <= 0 {
			return c.JSON(fiber.Map{"status": "error", "message": "Target case not found in database. Please check Claim Number and Insurance."})
		}

		session, _, _, _ := getSessionInfo(c)
		msg := "Action completed successfully"

		switch action {
		case "execute_purge":
			nodes := c.Context().PostArgs().PeekMulti("nodes")
			hasNode := func(name string) bool {
				if len(nodes) == 0 {
					return true
				}
				for _, n := range nodes {
					if string(n) == "all" || string(n) == name {
						return true
					}
				}
				return false
			}

			q := "UPDATE cases SET status = 'OPEN', is_completed = 0, modified_by = ?"
			args := []any{session.UserID}

			if hasNode("fo") {
				q += `, fo_id = NULL, fo_hospital_id = NULL, fo_patient_id = NULL, fo_pharmacy_id = NULL,
				        fo_pathology_id = NULL, fo_doctor_id = NULL, fo_name = NULL, fo_final_remark = NULL, fo_attachments = NULL`
			}
			if hasNode("check") {
				q += `, hospital_done_at = NULL, patient_done_at = NULL, pharmacy_done_at = NULL, pathology_done_at = NULL`
			}
			if hasNode("rep") {
				q += `, report_maker_id = NULL, case_verdict = 'PENDING', final_remark = NULL, report_submission_date = NULL, report_done_at = NULL`
			}
			if hasNode("qc") {
				q += `, qc_done_at = NULL`
			}
			q += " WHERE id = ?"
			args = append(args, targetID)

			_, err := pool.Exec(q, args...)
			if err != nil {
				return c.JSON(fiber.Map{"status": "error", "message": err.Error()})
			}
			_, _ = pool.Exec("DELETE FROM case_rework_history WHERE case_id = ?", targetID)
			msg = fmt.Sprintf("Case #%s post-creation data completely reset!", targetClaim)

		case "send_to_rework":
			reason := strings.TrimSpace(c.FormValue("rework_reason"))
			if reason == "" {
				reason = "Sent to Rework"
			}
			_, _ = pool.Exec(`
				INSERT INTO case_rework_history (case_id, old_status, old_verdict, old_final_remark, old_closing_date, rework_reason, reworked_by, rework_date)
				VALUES (?, ?, ?, ?, ?, ?, ?, NOW())
			`, targetID, oldStatus.String, oldVerdict.String, oldRemark.String, oldClosingDate.String, reason, session.UserID)

			_, err := pool.Exec(`
				UPDATE cases SET status = 'REWORK', case_verdict = 'PENDING', is_completed = 0, report_submission_date = NULL, modified_by = ?
				WHERE id = ?
			`, session.UserID, targetID)
			if err != nil {
				return c.JSON(fiber.Map{"status": "error", "message": "Rework History Error: " + err.Error()})
			}
			msg = "Case moved to Rework successfully!"

		case "soft_delete":
			_, err := pool.Exec("UPDATE cases SET deleted_at = NOW(), status = 'TRASHED', modified_by = ? WHERE id = ?", session.UserID, targetID)
			if err != nil {
				return c.JSON(fiber.Map{"status": "error", "message": err.Error()})
			}
			writeCaseAuditLog(pool, c, session, "DELETED RECORD", targetID, map[string]any{"claim_number": targetClaim})
			msg = "Case moved to Dustbin!"

		case "permanent_delete":
			_, err := pool.Exec("DELETE FROM cases WHERE id = ?", targetID)
			if err != nil {
				return c.JSON(fiber.Map{"status": "error", "message": err.Error()})
			}
			writeCaseAuditLog(pool, c, session, "DELETED RECORD", targetID, map[string]any{"claim_number": targetClaim})
			msg = "Case permanently deleted!"

		case "restore_case":
			_, err := pool.Exec("UPDATE cases SET deleted_at = NULL, status = 'OPEN', modified_by = ? WHERE id = ?", session.UserID, targetID)
			if err != nil {
				return c.JSON(fiber.Map{"status": "error", "message": err.Error()})
			}
			writeCaseAuditLog(pool, c, session, "MODIFIED RECORD", targetID, map[string]any{"claim_number": targetClaim})
			msg = "Case restored successfully!"

		default:
			return c.JSON(fiber.Map{"status": "error", "message": "Invalid action requested."})
		}

		return c.JSON(fiber.Map{"status": "success", "message": msg})
	}
}

func handleExportAction(pool *sql.DB) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if pool == nil {
			return c.JSON(fiber.Map{"status": "error", "message": "Database unavailable"})
		}
		action := c.FormValue("action")
		if action == "" {
			action = c.Query("action")
		}

		session, _, _, _ := getSessionInfo(c)

		if action == "save_preferences" {
			ensureUserExportPrefTableExists(pool)
			cols := c.Context().PostArgs().PeekMulti("columns")
			if session.UserID > 0 && len(cols) > 0 {
				colStrings := make([]string, len(cols))
				for i, v := range cols {
					colStrings[i] = string(v)
				}
				b, _ := json.Marshal(colStrings)
				_, _ = pool.Exec(`
					INSERT INTO user_export_preferences (user_id, selected_columns)
					VALUES (?, ?)
					ON DUPLICATE KEY UPDATE selected_columns = VALUES(selected_columns)
				`, session.UserID, string(b))
			}
			return c.JSON(fiber.Map{"status": "success"})
		}

		if action == "get_categories" {
			insID := c.FormValue("ins_id")
			if insID == "" {
				insID = c.Query("ins_id")
			}
			rows, err := pool.Query(`
				SELECT MIN(ct.id) as id, ct.type_name 
				FROM cases c 
				JOIN master_case_types ct ON c.case_type_id = ct.id 
				WHERE c.insurance_company_id = ? AND c.deleted_at IS NULL AND c.claim_number IS NOT NULL AND TRIM(c.claim_number) != '' 
				GROUP BY ct.type_name ORDER BY ct.type_name ASC
			`, insID)
			data := make([]map[string]any, 0)
			if err == nil {
				defer rows.Close()
				for rows.Next() {
					var id int64
					var name string
					if err := rows.Scan(&id, &name); err == nil {
						data = append(data, map[string]any{"id": id, "type_name": name})
					}
				}
			}
			return c.JSON(fiber.Map{"status": "success", "data": data})
		}

		if action == "get_sub_types" {
			catID := c.FormValue("cat_id")
			if catID == "" {
				catID = c.Query("cat_id")
			}
			rows, err := pool.Query(`
				SELECT MIN(st.id) as id, st.sub_type_name 
				FROM cases c 
				JOIN master_case_sub_types st ON c.case_sub_type_id = st.id 
				WHERE c.case_type_id = ? AND c.deleted_at IS NULL AND c.claim_number IS NOT NULL AND TRIM(c.claim_number) != '' 
				GROUP BY st.sub_type_name ORDER BY st.sub_type_name ASC
			`, catID)
			data := make([]map[string]any, 0)
			if err == nil {
				defer rows.Close()
				for rows.Next() {
					var id int64
					var name string
					if err := rows.Scan(&id, &name); err == nil {
						data = append(data, map[string]any{"id": id, "sub_type_name": name})
					}
				}
			}
			return c.JSON(fiber.Map{"status": "success", "data": data})
		}

		if action == "get_districts" {
			stateID := c.FormValue("state_id")
			if stateID == "" {
				stateID = c.Query("state_id")
			}
			rows, err := pool.Query(`
				SELECT MIN(d.id) as id, d.district_name 
				FROM cases c 
				JOIN master_districts d ON c.city_id = d.id 
				WHERE c.state_id = ? AND c.deleted_at IS NULL AND c.claim_number IS NOT NULL AND TRIM(c.claim_number) != '' 
				GROUP BY d.district_name ORDER BY d.district_name ASC
			`, stateID)
			data := make([]map[string]any, 0)
			if err == nil {
				defer rows.Close()
				for rows.Next() {
					var id int64
					var name string
					if err := rows.Scan(&id, &name); err == nil {
						data = append(data, map[string]any{"id": id, "district_name": name})
					}
				}
			}
			return c.JSON(fiber.Map{"status": "success", "data": data})
		}

		// Filter construction for fetch_preview & fetch_export_data
		where := []string{
			"c.deleted_at IS NULL",
			"c.claim_number IS NOT NULL",
			"TRIM(c.claim_number) != ''",
		}
		params := make([]any, 0)

		getVal := func(key string) string {
			v := c.FormValue(key)
			if v == "" {
				v = c.Query(key)
			}
			return strings.TrimSpace(v)
		}

		if v := getVal("ins_id"); v != "" {
			where = append(where, "c.insurance_company_id = ?")
			params = append(params, v)
		}
		if v := getVal("cat_id"); v != "" {
			where = append(where, "c.case_type_id = ?")
			params = append(params, v)
		}
		if v := getVal("sub_id"); v != "" {
			where = append(where, "c.case_sub_type_id = ?")
			params = append(params, v)
		}
		if v := getVal("alloc_from_date"); v != "" {
			where = append(where, "DATE(COALESCE(c.allocation_date, c.created_at)) >= ?")
			params = append(params, v)
		}
		if v := getVal("alloc_to_date"); v != "" {
			where = append(where, "DATE(COALESCE(c.allocation_date, c.created_at)) <= ?")
			params = append(params, v)
		}
		if v := getVal("alloc_month"); v != "" {
			where = append(where, "DATE_FORMAT(COALESCE(c.allocation_date, c.created_at), '%Y-%m') = ?")
			params = append(params, v)
		}
		if v := getVal("closing_month"); v != "" {
			where = append(where, "DATE_FORMAT(c.report_submission_date, '%Y-%m') = ?")
			params = append(params, v)
		}

		multiDates := c.Context().PostArgs().PeekMulti("multi_dates")
		if len(multiDates) > 0 {
			placeholders := make([]string, len(multiDates))
			for i, d := range multiDates {
				placeholders[i] = "?"
				params = append(params, string(d))
			}
			where = append(where, fmt.Sprintf("DAYNAME(COALESCE(c.allocation_date, c.created_at)) IN (%s)", strings.Join(placeholders, ",")))
		}

		if v := getVal("emp_type"); v != "" {
			where = append(where, "fo.employment_type = ?")
			params = append(params, v)
		}
		if v := getVal("state_id"); v != "" {
			where = append(where, "c.state_id = ?")
			params = append(params, v)
		}
		if v := getVal("city_id"); v != "" {
			where = append(where, "c.city_id = ?")
			params = append(params, v)
		}
		if v := getVal("hospital"); v != "" {
			where = append(where, "c.hospital_name = ?")
			params = append(params, v)
		}
		if v := getVal("am_id"); v != "" {
			where = append(where, "c.cm_id = ?")
			params = append(params, v)
		}
		if v := getVal("alm_id"); v != "" {
			where = append(where, "c.allocation_manager_id = ?")
			params = append(params, v)
		}

		if v := getVal("status_filter"); v != "" {
			if strings.EqualFold(v, "complete") {
				where = append(where, "c.is_completed = 1")
			} else if strings.EqualFold(v, "open") {
				where = append(where, "(c.is_completed = 0 OR c.is_completed IS NULL)")
			}
		}

		if v := getVal("days_filter"); v != "" {
			switch v {
			case "0_3":
				where = append(where, "DATEDIFF(CURDATE(), COALESCE(c.allocation_date, c.created_at)) BETWEEN 0 AND 3")
			case "4_7":
				where = append(where, "DATEDIFF(CURDATE(), COALESCE(c.allocation_date, c.created_at)) BETWEEN 4 AND 7")
			case "8_15":
				where = append(where, "DATEDIFF(CURDATE(), COALESCE(c.allocation_date, c.created_at)) BETWEEN 8 AND 15")
			case "15_plus":
				where = append(where, "DATEDIFF(CURDATE(), COALESCE(c.allocation_date, c.created_at)) > 15")
			}
		}

		whereSQL := strings.Join(where, " AND ")
		statusCaseSQL := "CASE WHEN c.is_completed = 1 THEN 'Complete' ELSE 'Open' END as status"

		if action == "fetch_preview" {
			limitStr := getVal("limit")
			limit := 25
			if limitStr == "ALL" {
				limit = 0
			} else if n, err := strconv.Atoi(limitStr); err == nil && n > 0 {
				limit = n
			}
			page, _ := strconv.Atoi(getVal("page"))
			if page < 1 {
				page = 1
			}

			countSQL := fmt.Sprintf("SELECT COUNT(c.id) FROM cases c LEFT JOIN users fo ON c.fo_id = fo.id WHERE %s", whereSQL)
			var totalRecords int
			_ = pool.QueryRow(countSQL, params...).Scan(&totalRecords)

			totalPages := 1
			limitSQL := ""
			if limit > 0 {
				totalPages = int(math.Ceil(float64(totalRecords) / float64(limit)))
				offset := (page - 1) * limit
				limitSQL = fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)
			}

			query := fmt.Sprintf(`
				SELECT c.claim_number, ic.company_name, ct.type_name, st.sub_type_name, 
				       DATE_FORMAT(COALESCE(c.allocation_date, c.created_at), '%%d %%b %%Y %%h:%%i %%p') as allocation_date,
				       DATE_FORMAT(c.report_submission_date, '%%d %%b %%Y %%h:%%i %%p') as closing_date,
				       c.patient_name, d.district_name, s.state_name,
				       fo.fullname as fo_name, fo.employment_type,
				       c.claim_amount, c.diagnosis as patient_diagnosis,
				       rm.fullname as rm_name, am.fullname as am_name, icm.fullname as cm_name, alm.fullname as alm_name,
				       DATEDIFF(IFNULL(c.report_submission_date, CURDATE()), COALESCE(c.allocation_date, c.created_at)) as aging_days,
				       %s
				FROM cases c
				LEFT JOIN master_insurance_companies ic ON c.insurance_company_id = ic.id
				LEFT JOIN master_case_types ct ON c.case_type_id = ct.id
				LEFT JOIN master_case_sub_types st ON c.case_sub_type_id = st.id
				LEFT JOIN master_states s ON c.state_id = s.id
				LEFT JOIN master_districts d ON c.city_id = d.id
				LEFT JOIN users am ON c.cm_id = am.id
				LEFT JOIN users icm ON c.icm_id = icm.id
				LEFT JOIN users alm ON c.allocation_manager_id = alm.id
				LEFT JOIN users fo ON c.fo_id = fo.id
				LEFT JOIN users rm ON c.report_maker_id = rm.id
				WHERE %s ORDER BY COALESCE(c.allocation_date, c.created_at) ASC %s
			`, statusCaseSQL, whereSQL, limitSQL)

			rows, err := pool.Query(query, params...)
			data := make([]map[string]any, 0)
			if err == nil {
				defer rows.Close()
				cols, _ := rows.Columns()
				for rows.Next() {
					vals := make([]any, len(cols))
					ptrs := make([]any, len(cols))
					for i := range vals {
						ptrs[i] = &vals[i]
					}
					if err := rows.Scan(ptrs...); err == nil {
						rowMap := make(map[string]any)
						for i, col := range cols {
							if b, ok := vals[i].([]byte); ok {
								rowMap[col] = string(b)
							} else {
								rowMap[col] = vals[i]
							}
						}
						data = append(data, rowMap)
					}
				}
			}

			return c.JSON(fiber.Map{
				"status":        "success",
				"data":          data,
				"total_records": totalRecords,
				"total_pages":   totalPages,
				"current_page":  page,
			})
		}

		if action == "fetch_export_data" {
			limitSQL := ""
			limitStr := getVal("limit")
			if limitStr != "" && limitStr != "ALL" {
				if n, err := strconv.Atoi(limitStr); err == nil && n > 0 {
					limitSQL = fmt.Sprintf(" LIMIT %d", n)
				}
			}

			smartFOSQL := `
				TRIM(BOTH ', ' FROM CONCAT_WS(', ',
					IF(fo_hosp.id IS NOT NULL, CONCAT(fo_hosp.fullname, ' (Hosp)'), NULL),
					IF(fo_pat.id IS NOT NULL AND fo_pat.id != IFNULL(fo_hosp.id, 0), CONCAT(fo_pat.fullname, ' (Pat)'), NULL),
					IF(fo_phar.id IS NOT NULL AND fo_phar.id != IFNULL(fo_hosp.id, 0) AND fo_phar.id != IFNULL(fo_pat.id, 0), CONCAT(fo_phar.fullname, ' (Phar)'), NULL),
					IF(fo_path.id IS NOT NULL AND fo_path.id != IFNULL(fo_hosp.id, 0) AND fo_path.id != IFNULL(fo_pat.id, 0) AND fo_path.id != IFNULL(fo_phar.id, 0), CONCAT(fo_path.fullname, ' (Path)'), NULL),
					IF(fo_hosp.id IS NULL AND fo_pat.id IS NULL AND fo_phar.id IS NULL AND fo_path.id IS NULL AND fo.id IS NOT NULL, CONCAT(fo.fullname, ' (All)'), NULL)
				)) as fo_name
			`

			query := fmt.Sprintf(`
				SELECT c.*, ic.company_name, ct.type_name, st.sub_type_name, 
				       COALESCE(c.allocation_date, c.created_at) as allocation_date_raw,
				       DATE_FORMAT(COALESCE(c.allocation_date, c.created_at), '%%Y-%%m-01') as allocation_month_raw,
				       c.report_submission_date as closing_date_raw,
				       DATE_FORMAT(c.report_submission_date, '%%Y-%%m-01') as closing_month_raw,
				       s.state_name, d.district_name,
				       am.fullname as am_name, icm.fullname as cm_name, alm.fullname as alm_name, 
				       %s, 
				       fo.employment_type as fo_employment_type,
				       rm.fullname as rm_name,
				       c.status as decision_status,
				       %s
				FROM cases c
				LEFT JOIN master_insurance_companies ic ON c.insurance_company_id = ic.id
				LEFT JOIN master_case_types ct ON c.case_type_id = ct.id
				LEFT JOIN master_case_sub_types st ON c.case_sub_type_id = st.id
				LEFT JOIN master_states s ON c.state_id = s.id
				LEFT JOIN master_districts d ON c.city_id = d.id
				LEFT JOIN users am ON c.cm_id = am.id
				LEFT JOIN users icm ON c.icm_id = icm.id
				LEFT JOIN users alm ON c.allocation_manager_id = alm.id
				LEFT JOIN users fo ON c.fo_id = fo.id
				LEFT JOIN users fo_hosp ON c.fo_hospital_id = fo_hosp.id
				LEFT JOIN users fo_pat ON c.fo_patient_id = fo_pat.id
				LEFT JOIN users fo_phar ON c.fo_pharmacy_id = fo_phar.id
				LEFT JOIN users fo_path ON c.fo_pathology_id = fo_path.id
				LEFT JOIN users rm ON c.report_maker_id = rm.id
				WHERE %s ORDER BY COALESCE(c.allocation_date, c.created_at) ASC %s
			`, smartFOSQL, statusCaseSQL, whereSQL, limitSQL)

			rows, err := pool.Query(query, params...)
			data := make([]map[string]any, 0)
			if err == nil {
				defer rows.Close()
				cols, _ := rows.Columns()
				for rows.Next() {
					vals := make([]any, len(cols))
					ptrs := make([]any, len(cols))
					for i := range vals {
						ptrs[i] = &vals[i]
					}
					if err := rows.Scan(ptrs...); err == nil {
						rowMap := make(map[string]any)
						for i, col := range cols {
							if b, ok := vals[i].([]byte); ok {
								rowMap[col] = string(b)
							} else {
								rowMap[col] = vals[i]
							}
						}
						data = append(data, rowMap)
					}
				}
			}

			return c.JSON(fiber.Map{"status": "success", "data": data})
		}

		return c.JSON(fiber.Map{"status": "error", "message": "Invalid action"})
	}
}

func nullableInt(val string) any {
	val = strings.TrimSpace(val)
	if val == "" {
		return nil
	}
	n, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		return nil
	}
	return n
}

func nullableFloat(val string) any {
	val = strings.TrimSpace(val)
	if val == "" {
		return nil
	}
	f, err := strconv.ParseFloat(val, 64)
	if err != nil {
		return nil
	}
	return f
}

func nullableStr(val string) any {
	val = strings.TrimSpace(val)
	if val == "" {
		return nil
	}
	return val
}
