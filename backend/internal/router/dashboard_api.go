package router

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gofiber/fiber/v2"
)

func RegisterDashboardAPI(api fiber.Router, pool *sql.DB, requireSession fiber.Handler) {
	api.Get("/dashboard/layout", requireSession, func(c *fiber.Ctx) error {
		session, ok := c.Locals("dashboardSession").(dashboardSession)
		if !ok {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "authentication required"})
		}
		layout, err := buildDashboardLayoutFromDB(pool, session)
		if err != nil {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "dashboard layout unavailable"})
		}
		return c.JSON(layout)
	})
	respond := func(c *fiber.Ctx, selectFields func(DashboardResponse) any) error {
		session, ok := c.Locals("dashboardSession").(dashboardSession)
		if !ok {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "authentication required"})
		}
		payload, err := buildDashboardResponseFromDB(pool, session)
		if err != nil {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "dashboard data unavailable"})
		}
		return c.JSON(selectFields(payload))
	}
	api.Get("/dashboard", requireSession, func(c *fiber.Ctx) error {
		return respond(c, func(value DashboardResponse) any { return value })
	})
	api.Get("/dashboard/case-status", requireSession, func(c *fiber.Ctx) error {
		session, ok := c.Locals("dashboardSession").(dashboardSession)
		if !ok {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "authentication required"})
		}
		month := c.Query("filter_month_year")
		if month == "" {
			month = c.Query("month")
		}
		date := c.Query("allocation_date")
		if date == "" {
			date = c.Query("date")
		}
		segments, total, err := fetchCaseStatusSegments(pool, session, month, date)
		if err != nil {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "case status unavailable"})
		}
		return c.JSON(fiber.Map{"segments": segments, "total": total})
	})
	api.Get("/overview", requireSession, func(c *fiber.Ctx) error {
		return respond(c, func(value DashboardResponse) any { return value })
	})
	api.Get("/cases", requireSession, func(c *fiber.Ctx) error {
		session, ok := c.Locals("dashboardSession").(dashboardSession)
		if !ok {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "authentication required"})
		}
		payload, err := fetchDashboardCases(pool, session, c)
		if err != nil {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "case data unavailable"})
		}
		return c.JSON(payload)
	})
	api.Get("/database", requireSession, func(c *fiber.Ctx) error {
		return respond(c, func(value DashboardResponse) any {
			return fiber.Map{"summary": value.Summary, "segments": value.Segments}
		})
	})
	api.Get("/analytics", requireSession, func(c *fiber.Ctx) error {
		return respond(c, func(value DashboardResponse) any { return fiber.Map{"trend": value.Trend, "segments": value.Segments} })
	})
	api.Get("/reports", requireSession, func(c *fiber.Ctx) error {
		return respond(c, func(value DashboardResponse) any { return fiber.Map{"summary": value.Summary, "cases": value.Cases} })
	})
	api.Get("/dashboard/workload-stats", requireSession, func(c *fiber.Ctx) error {
		return respond(c, func(value DashboardResponse) any {
			return fiber.Map{
				"companyOpenCases": value.CompanyOpenCases,
				"stateOpenCases":   value.StateOpenCases,
				"foOpenCases":      value.StateOpenCases,
			}
		})
	})
	api.Get("/dashboard/direct-close/options", requireSession, func(c *fiber.Ctx) error {
		fieldOfficers, err := loadDirectCloseUsers(pool, 6)
		if err != nil {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "field officers unavailable"})
		}
		reportMakers, err := loadDirectCloseUsers(pool, 5)
		if err != nil {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "report makers unavailable"})
		}
		return c.JSON(fiber.Map{"fieldOfficers": fieldOfficers, "reportMakers": reportMakers})
	})
	registerDashboardCaseRemarksAPI(api, pool, requireSession)
	api.Post("/cases/mail-am", requireSession, handleMailAreaManager(pool))
	api.Post("/cases/:id/mail-am", requireSession, handleMailSingleCaseAM(pool))
	api.Post("/cases/:id/direct-close", requireSession, func(c *fiber.Ctx) error {
		session, ok := c.Locals("dashboardSession").(dashboardSession)
		if !ok {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "authentication required"})
		}
		caseID, err := strconv.ParseInt(c.Params("id"), 10, 64)
		if err != nil || caseID < 1 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid case id"})
		}
		var request directCloseRequest
		if err := c.BodyParser(&request); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid direct-close request"})
		}
		mode, validMode := normalizeDirectCloseMode(request.Mode)
		if !validMode {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "choose withdrawal or denial"})
		}
		status := strings.ToUpper(strings.TrimSpace(request.Status))
		if mode == "withdraw" {
			status = "WITHDRAW"
		} else if status != "REJECTED" && status != "PAYABLE" && status != "QUERY" && status != "INCONCLUSIVE" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "select a valid denial status"})
		}
		verdict := strings.TrimSpace(request.Verdict)
		if verdict == "" && mode == "withdraw" {
			verdict = "WITHDRAW"
		}
		if len(verdict) > 255 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "verdict must be 255 characters or fewer"})
		}
		remarks := strings.TrimSpace(request.Remarks)
		if remarks == "" {
			if mode == "withdraw" {
				remarks = "Case directly withdrawn from Dashboard."
			} else {
				remarks = "Case directly closed from Dashboard."
			}
		}

		partColumns := map[string]string{"hospital": "fo_hospital_id", "patient": "fo_patient_id", "pharmacy": "fo_pharmacy_id", "pathology": "fo_pathology_id", "doctor": "fo_doctor_id"}
		assignedParts := make(map[string]int64)
		for _, assignment := range request.Assignments {
			if assignment.FieldOfficerID < 1 || len(assignment.Parts) == 0 {
				return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "choose a field officer and at least one part"})
			}
			var valid int
			if err := pool.QueryRow("SELECT COUNT(*) FROM users WHERE id = ? AND role_id = 6 AND status = 'active' AND deleted_at IS NULL", assignment.FieldOfficerID).Scan(&valid); err != nil {
				return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "field officer validation unavailable"})
			}
			if valid == 0 {
				return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "selected field officer is unavailable"})
			}
			for _, part := range assignment.Parts {
				if _, ok := partColumns[part]; !ok {
					return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid assignment part"})
				}
				if _, exists := assignedParts[part]; exists {
					return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "a case part can only be assigned once"})
				}
				assignedParts[part] = assignment.FieldOfficerID
			}
		}
		if request.ReportMakerID > 0 {
			var valid int
			if err := pool.QueryRow("SELECT COUNT(*) FROM users WHERE id = ? AND role_id = 5 AND status = 'active' AND deleted_at IS NULL", request.ReportMakerID).Scan(&valid); err != nil {
				return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "report maker validation unavailable"})
			}
			if valid == 0 {
				return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "selected report maker is unavailable"})
			}
		}
		deniedParts := request.DeniedParts
		if mode == "denial" && len(deniedParts) == 0 {
			for _, part := range []string{"hospital", "patient", "pharmacy", "pathology", "doctor"} {
				if _, ok := assignedParts[part]; ok {
					deniedParts = append(deniedParts, part)
				}
			}
		}
		if mode == "denial" && len(deniedParts) == 0 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "select the denied part(s) for this case"})
		}
		if mode == "withdraw" {
			deniedParts = nil
			request.DeniedPartRegistrations = nil
		}
		registryEntries, err := buildDenialRegistryEntries(deniedParts, request.DeniedPartRegistrations)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}

		where, scopeArgs, err := dashboardCaseScope(pool, session)
		if err != nil {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "case scope unavailable"})
		}
		tx, err := pool.Begin()
		if err != nil {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "could not start case update"})
		}
		defer tx.Rollback()
		lookupArgs := append(append([]any{}, scopeArgs...), caseID)
		var claimNumber, hospitalName, patientName string
		var oldStatus sql.NullString
		if err := tx.QueryRow("SELECT c.claim_number, c.status, COALESCE(c.hospital_name,''), COALESCE(c.patient_name,'') FROM cases c WHERE "+where+" AND c.id = ? FOR UPDATE", lookupArgs...).Scan(&claimNumber, &oldStatus, &hospitalName, &patientName); err != nil {
			if err == sql.ErrNoRows {
				return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "case not found"})
			}
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "case lookup failed"})
		}

		sets := []string{"status = ?", "case_verdict = ?", "final_remark = ?", "is_completed = 1", "report_submission_date = NOW()", "modified_by = ?"}
		updateArgs := []any{status, verdict, remarks, session.UserID}
		if len(deniedParts) > 0 {
			sets = append(sets, "denied_parts = ?")
			updateArgs = append(updateArgs, strings.Join(deniedParts, ","))
		}
		for _, part := range []string{"hospital", "patient", "pharmacy", "pathology", "doctor"} {
			if officerID, ok := assignedParts[part]; ok {
				sets = append(sets, partColumns[part]+" = ?")
				updateArgs = append(updateArgs, officerID)
			}
		}
		if len(request.Assignments) > 0 {
			sets = append(sets, "fo_id = ?")
			updateArgs = append(updateArgs, request.Assignments[0].FieldOfficerID)
		}
		if request.ReportMakerID > 0 {
			sets = append(sets, "report_maker_id = ?")
			updateArgs = append(updateArgs, request.ReportMakerID)
		}
		updateArgs = append(updateArgs, caseID)
		if _, err := tx.Exec("UPDATE cases SET "+strings.Join(sets, ", ")+" WHERE id = ? AND deleted_at IS NULL", updateArgs...); err != nil {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "case update failed"})
		}
		for _, entry := range registryEntries {
			var entityName any
			switch entry.Part {
			case "hospital":
				entityName = hospitalName
			case "patient":
				entityName = patientName
			}
			denialReason := "Case Denied for part(s): " + entry.EntityType + "."
			if remarks != "" {
				denialReason += " " + remarks
			}
			if _, err := tx.Exec(`INSERT INTO denial_registry (case_id, claim_number, entity_type, entity_name, unique_id, denial_reason, denied_by)
				VALUES (?, ?, ?, ?, ?, ?, ?)`, caseID, claimNumber, entry.EntityType, entityName, entry.UniqueID, denialReason, session.UserID); err != nil {
				return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "denial registration could not be saved"})
			}
		}
		if _, err := tx.Exec("INSERT INTO case_status_history (case_id, old_status, new_status, changed_by, remarks) VALUES (?, ?, ?, ?, ?)", caseID, oldStatus.String, status, session.UserID, remarks); err != nil {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "case history update failed"})
		}
		if err := tx.Commit(); err != nil {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "case update could not be committed"})
		}
		return c.JSON(fiber.Map{"status": "ok", "claimNumber": claimNumber})
	})
	registerDashboardTargetsAPI(api, pool, requireSession)
}

func normalizeDirectCloseMode(value string) (string, bool) {
	mode := strings.ToLower(strings.TrimSpace(value))
	if mode == "" {
		return "close", true
	}
	return mode, mode == "withdraw" || mode == "denial"
}

type directCloseAssignment struct {
	FieldOfficerID int64    `json:"fieldOfficerId"`
	Parts          []string `json:"parts"`
}

type directCloseRequest struct {
	Mode                    string                        `json:"mode"`
	Status                  string                        `json:"status"`
	Verdict                 string                        `json:"verdict"`
	Remarks                 string                        `json:"remarks"`
	Assignments             []directCloseAssignment       `json:"assignments"`
	DeniedParts             []string                      `json:"deniedParts"`
	DeniedPartRegistrations []deniedPartRegistrationInput `json:"deniedPartRegistrations"`
	ReportMakerID           int64                         `json:"reportMakerId"`
}

type deniedPartRegistrationInput struct {
	Part     string `json:"part"`
	UniqueID string `json:"uniqueId"`
}

type denialRegistryEntry struct {
	Part       string
	EntityType string
	UniqueID   string
}

func buildDenialRegistryEntries(parts []string, registrations []deniedPartRegistrationInput) ([]denialRegistryEntry, error) {
	entityTypes := map[string]string{"hospital": "Hospital", "patient": "Patient", "pharmacy": "Pharmacy", "pathology": "Pathology", "doctor": "Doctor"}
	registrationByPart := make(map[string]string, len(registrations))
	for _, registration := range registrations {
		part := strings.ToLower(strings.TrimSpace(registration.Part))
		if _, ok := entityTypes[part]; !ok {
			return nil, fmt.Errorf("invalid denied part registration")
		}
		uniqueID := strings.TrimSpace(registration.UniqueID)
		if uniqueID == "" || utf8.RuneCountInString(uniqueID) > 100 {
			return nil, fmt.Errorf("enter a registration number or ID of at most 100 characters")
		}
		if _, exists := registrationByPart[part]; exists {
			return nil, fmt.Errorf("duplicate registration for denied part")
		}
		registrationByPart[part] = uniqueID
	}

	entries := make([]denialRegistryEntry, 0, len(parts))
	seenParts := make(map[string]struct{}, len(parts))
	for _, value := range parts {
		part := strings.ToLower(strings.TrimSpace(value))
		entityType, ok := entityTypes[part]
		if !ok {
			return nil, fmt.Errorf("invalid denied part")
		}
		if _, exists := seenParts[part]; exists {
			return nil, fmt.Errorf("duplicate denied part")
		}
		seenParts[part] = struct{}{}
		uniqueID, ok := registrationByPart[part]
		if !ok {
			return nil, fmt.Errorf("enter a registration number or ID for every denied part")
		}
		entries = append(entries, denialRegistryEntry{Part: part, EntityType: entityType, UniqueID: uniqueID})
	}
	if len(registrationByPart) != len(entries) {
		return nil, fmt.Errorf("registration details must match the selected denied parts")
	}
	return entries, nil
}

type directCloseUser struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	EmploymentType string `json:"employmentType"`
}

func loadDirectCloseUsers(pool *sql.DB, roleID int) ([]directCloseUser, error) {
	rows, err := pool.Query("SELECT id, fullname, employment_type FROM users WHERE role_id = ? AND status = 'active' AND deleted_at IS NULL ORDER BY fullname", roleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := make([]directCloseUser, 0)
	for rows.Next() {
		var user directCloseUser
		if err := rows.Scan(&user.ID, &user.Name, &user.EmploymentType); err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func fetchDashboardCases(pool *sql.DB, session dashboardSession, c *fiber.Ctx) (fiber.Map, error) {
	where, args, err := dashboardCaseScope(pool, session)
	if err != nil {
		return nil, err
	}
	if caseStatus := strings.ToLower(strings.TrimSpace(c.Query("case_status"))); caseStatus != "" {
		where += " AND LOWER(COALESCE(NULLIF(c.status, ''), 'open')) = ?"
		args = append(args, caseStatus)
	}
	if c.Query("completed_today") == "1" {
		where += " AND c.is_completed = 1 AND DATE(c.report_submission_date) = CURDATE()"
	}
	if c.Query("overdue") == "1" {
		where += " AND c.is_completed = 0 AND TIMESTAMPDIFF(HOUR, COALESCE(c.allocation_date, c.created_at), NOW()) > COALESCE(NULLIF(t.tat_hours, 0), 48)"
	}
	if value := strings.TrimSpace(c.Query("claim_number")); value != "" {
		where += " AND (c.claim_number LIKE ? OR c.patient_name LIKE ?)"
		args = append(args, "%"+value+"%", "%"+value+"%")
	}
	if value := strings.TrimSpace(c.Query("hospital_name")); value != "" {
		where += " AND c.hospital_name LIKE ?"
		args = append(args, "%"+value+"%")
	}
	if value := strings.TrimSpace(c.Query("fo_id")); value != "" {
		if value == "0" || strings.ToLower(value) == "unassigned" {
			where += " AND (c.fo_id IS NULL OR c.fo_id = 0)"
		} else {
			where += " AND c.fo_id = ?"
			args = append(args, value)
		}
	}
	if value := strings.TrimSpace(c.Query("insurance_id")); value != "" {
		if value == "0" || strings.ToLower(value) == "unassigned" {
			where += " AND (c.insurance_company_id IS NULL OR c.insurance_company_id = 0)"
		} else {
			where += " AND c.insurance_company_id = ?"
			args = append(args, value)
		}
	}
	for queryName, columnName := range map[string]string{
		"type_id": "case_type_id", "sub_type_id": "case_sub_type_id",
		"state_id": "state_id", "city_id": "city_id", "am_id": "cm_id", "alloc_mgr_id": "allocation_manager_id", "rm_id": "report_maker_id",
	} {
		if value := strings.TrimSpace(c.Query(queryName)); value != "" {
			where += " AND c." + columnName + " = ?"
			args = append(args, value)
		}
	}
	if value := strings.TrimSpace(c.Query("allocation_date")); value != "" {
		where += " AND DATE(COALESCE(c.allocation_date,c.created_at)) = ?"
		args = append(args, value)
	}
	if value := c.Query("filter_month_year"); value != "" {
		where += " AND DATE_FORMAT(COALESCE(c.allocation_date, c.created_at), '%Y-%m') = ?"
		args = append(args, value)
	}
	if category := strings.ToLower(strings.TrimSpace(c.Query("category"))); category != "" {
		switch category {
		case "reimbursement":
			where += " AND LOWER(t.type_name) LIKE '%reimbursement%'"
		case "cashless":
			where += " AND LOWER(t.type_name) LIKE '%cashless%'"
		case "other":
			where += " AND LOWER(t.type_name) NOT LIKE '%cashless%' AND LOWER(t.type_name) NOT LIKE '%reimbursement%'"
		}
	}
	var tabCounts struct {
		Total     int `json:"total"`
		Open      int `json:"open"`
		Completed int `json:"completed"`
	}
	countQuery := `SELECT COUNT(*),
		COALESCE(SUM(c.is_completed = 0 AND (LOWER(c.status) = 'open' OR c.status = '' OR c.status IS NULL) AND (c.fo_id IS NULL OR c.fo_id = 0)),0),
		COALESCE(SUM(c.is_completed = 1),0)
		FROM cases c LEFT JOIN master_case_types t ON c.case_type_id = t.id WHERE ` + where
	if err := pool.QueryRow(countQuery, args...).Scan(&tabCounts.Total, &tabCounts.Open, &tabCounts.Completed); err != nil {
		return nil, err
	}
	status := c.Query("tab_status", "total")
	if c.Query("open_cases_only") == "1" {
		where += " AND (c.is_completed = 0 OR LOWER(c.status) = 'open')"
	} else if status == "open" {
		if c.Query("fo_id") != "" && c.Query("fo_id") != "0" && strings.ToLower(c.Query("fo_id")) != "unassigned" {
			where += " AND (c.is_completed = 0 OR LOWER(c.status) = 'open')"
		} else if c.Query("insurance_id") != "" {
			where += " AND (c.is_completed = 0 OR LOWER(c.status) = 'open')"
		} else {
			where += " AND c.is_completed = 0 AND (LOWER(c.status) = 'open' OR c.status = '' OR c.status IS NULL) AND (c.fo_id IS NULL OR c.fo_id = 0)"
		}
	} else if status == "completed" || status == "closed" {
		where += " AND c.is_completed = 1"
	}
	page := c.QueryInt("page", 1)
	if page < 1 {
		page = 1
	}
	limit := c.QueryInt("limit", 25)
	if limit < 1 {
		limit = 25
	} else if limit > 100 {
		limit = 100
	}
	var total int
	if err := pool.QueryRow("SELECT COUNT(*) FROM cases c LEFT JOIN master_case_types t ON c.case_type_id = t.id WHERE "+where, args...).Scan(&total); err != nil {
		return nil, err
	}
	offset := (page - 1) * limit
	queryArgs := append(append([]any{}, args...), limit, offset)
	orderBy := "(c.is_completed = 0 AND TIMESTAMPDIFF(SECOND, COALESCE(c.allocation_date, c.created_at), NOW()) > (COALESCE(NULLIF(t.tat_hours, 0), 48) * 3600)) DESC, COALESCE(c.allocation_date, c.created_at) DESC, c.id DESC"
	if status == "completed" || status == "closed" {
		orderBy = "COALESCE(c.report_submission_date, c.updated_at, c.created_at) DESC, c.id DESC"
	}
	rows, err := pool.Query(`SELECT c.id, COALESCE(c.claim_number, ''), COALESCE(c.patient_name, ''),
		COALESCE(ic.company_name, ''), COALESCE(t.type_name, ''), COALESCE(st.sub_type_name, ''),
		COALESCE(c.hospital_name, ''), COALESCE(s.state_name, ''), COALESCE(d.district_name, ''),
		DATE_FORMAT(COALESCE(c.allocation_date, c.created_at), '%Y-%m-%d %H:%i:%s'),
		CASE WHEN c.is_completed = 1 THEN DATE_FORMAT(COALESCE(c.report_submission_date, c.updated_at, c.created_at), '%Y-%m-%d %H:%i:%s') ELSE '' END,
		TIMESTAMPDIFF(SECOND, COALESCE(c.allocation_date, c.created_at), COALESCE(c.report_submission_date, NOW())),
		COALESCE(NULLIF(t.tat_hours, 0), 48), COALESCE(am.fullname, 'Unassigned'), COALESCE(am.email, ''),
		COALESCE(icm.fullname, ''), COALESCE(alm.fullname, ''), COALESCE(fo.fullname, ''), COALESCE(rm.fullname, ''),
		LOWER(COALESCE(NULLIF(c.status, ''), 'open')), COALESCE(c.ai_risk_score, 0), COALESCE(c.ai_flag_reason, ''), c.is_completed, COALESCE(c.fo_id, 0),
		COALESCE(c.case_verdict, ''), COALESCE(c.final_remark, '')
		FROM cases c LEFT JOIN master_insurance_companies ic ON c.insurance_company_id = ic.id
		LEFT JOIN master_case_types t ON c.case_type_id = t.id
		LEFT JOIN master_case_sub_types st ON c.case_sub_type_id = st.id
		LEFT JOIN master_states s ON c.state_id = s.id
		LEFT JOIN master_districts d ON c.city_id = d.id
		LEFT JOIN users am ON c.cm_id = am.id
		LEFT JOIN users icm ON c.icm_id = icm.id
		LEFT JOIN users alm ON c.allocation_manager_id = alm.id
		LEFT JOIN users fo ON c.fo_id = fo.id
		LEFT JOIN users rm ON c.report_maker_id = rm.id
		WHERE `+where+` ORDER BY `+orderBy+` LIMIT ? OFFSET ?`, queryArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cases := make([]CaseRow, 0, limit)
	for rows.Next() {
		var row CaseRow
		var status string
		var riskReason string
		var completed bool
		var foID int64
		if err := rows.Scan(&row.CaseID, &row.ClaimNumber, &row.Patient, &row.Insurance, &row.CaseType, &row.SubType, &row.Hospital, &row.State, &row.City, &row.AllocatedAt, &row.ClosedAt, &row.ElapsedSeconds, &row.TATHours, &row.AreaManager, &row.AreaManagerEmail, &row.CaseManager, &row.AllocationMgr, &row.FieldOfficer, &row.ReportMaker, &status, &row.RiskScore, &riskReason, &completed, &foID, &row.Verdict, &row.FinalRemark); err != nil {
			return nil, err
		}
		if row.ClaimNumber == "" {
			row.ClaimNumber = fmt.Sprintf("%d", row.CaseID)
		}
		row.ID = row.ClaimNumber
		row.Source = row.Insurance
		row.RiskReason = riskReason
		row.Priority = fmt.Sprintf("%.0f%%", row.RiskScore*100)
		row.Status = "waiting"
		if completed {
			row.Status = "resolved"
		} else if status == "open" && foID == 0 {
			row.Status = "open"
		} else if status == "open" {
			row.Status = "progress"
		}
		cases = append(cases, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := loadDashboardCaseRemarks(pool, session, cases); err != nil {
		return nil, err
	}
	return fiber.Map{"cases": cases, "total": total, "page": page, "limit": limit, "tabCounts": tabCounts}, nil
}

func fetchCaseStatusSegments(pool *sql.DB, session dashboardSession, month, date string) ([]Segment, int, error) {
	if pool == nil {
		return nil, 0, fmt.Errorf("database not configured")
	}
	where, args, err := dashboardCaseScope(pool, session)
	if err != nil {
		return nil, 0, err
	}
	if date = strings.TrimSpace(date); date != "" {
		where += " AND DATE(COALESCE(c.allocation_date, c.created_at)) = ?"
		args = append(args, date)
	} else if month = strings.TrimSpace(month); month != "" {
		where += " AND DATE_FORMAT(COALESCE(c.allocation_date, c.created_at), '%Y-%m') = ?"
		args = append(args, month)
	}

	statusRows, err := pool.Query(`SELECT LOWER(COALESCE(NULLIF(c.status, ''), 'open')), COUNT(*)
    FROM cases c WHERE `+where+` GROUP BY 1 ORDER BY COUNT(*) DESC`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer statusRows.Close()

	segments := make([]Segment, 0)
	colors := map[string]string{"open": "#2563eb", "payable": "#14b8a6", "rejected": "#ef4444", "query": "#f59e0b", "inconclusive": "#64748b", "withdraw": "#94a3b8", "rework": "#8b5cf6"}
	total := 0
	for statusRows.Next() {
		var status string
		var count int
		if err := statusRows.Scan(&status, &count); err != nil {
			return nil, 0, err
		}
		color := colors[status]
		if color == "" {
			color = "#64748b"
		}
		name := status
		if len(status) > 0 {
			name = strings.ToUpper(status[:1]) + status[1:]
		}
		total += count
		segments = append(segments, Segment{Name: name, Value: count, Color: color})
	}
	if err := statusRows.Err(); err != nil {
		return nil, 0, err
	}
	return segments, total, nil
}
