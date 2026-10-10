package router

import (
	"database/sql"
	_ "embed"
	"fmt"
	"html"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

//go:embed templates/reports/employee_performance.html
var employeePerformanceHTML []byte

func ensureReportTables(pool *sql.DB) {
	if pool == nil {
		return
	}
	_, _ = pool.Exec(`
		CREATE TABLE IF NOT EXISTS performance_points_config (
			id INT AUTO_INCREMENT PRIMARY KEY,
			company_id INT DEFAULT NULL,
			category_id INT DEFAULT NULL,
			subcategory_id INT DEFAULT NULL,
			points DECIMAL(5,2) DEFAULT 1.00,
			tat_hours INT DEFAULT 48,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
	`)
}

func resolveRoleColumn(role string) string {
	switch role {
	case "Report Maker", "Back Office":
		return "report_maker_id"
	case "Area Manager", "Case Manager":
		return "cm_id"
	case "Allocation Manager":
		return "allocation_manager_id"
	case "ICM":
		return "icm_id"
	default:
		return "report_maker_id"
	}
}

func calcEarnedPoints(basePts float64, typeName string, isFoRole bool, foHospitalID, foPatientID, foPharmacyID, foPathologyID, foDoctorID, empId int) float64 {
	if !isFoRole {
		return basePts
	}

	isCashless := strings.Contains(strings.ToUpper(typeName), "CASHLESS")
	parts := []int{foHospitalID, foPatientID, foPharmacyID, foPathologyID, foDoctorID}

	pCount := 0
	validFos := make(map[int]bool)
	for _, pid := range parts {
		if pid > 0 {
			validFos[pid] = true
			if pid == empId {
				pCount++
			}
		}
	}

	uniqueFoCount := len(validFos)
	if uniqueFoCount == 0 {
		uniqueFoCount = 1
	}

	if isCashless {
		if pCount > 0 {
			return basePts / float64(uniqueFoCount)
		}
		return 0.0
	}

	return (basePts / 5.0) * float64(pCount)
}

func RegisterReportRoutes(app *fiber.App, pool *sql.DB, requireSession fiber.Handler) {
	ensureReportTables(pool)

	// HTML View
	app.Get("/reports/employee_performance", requireSession, func(c *fiber.Ctx) error {
		htmlStr := string(employeePerformanceHTML)
		htmlStr = strings.ReplaceAll(htmlStr, "{{SIDEBAR_PARTIAL}}", string(dashboardSidebarPartial))
		htmlStr = strings.ReplaceAll(htmlStr, "{{HEADER_PARTIAL}}", string(dashboardHeaderPartial))
		htmlStr = strings.ReplaceAll(htmlStr, "{{FOOTER_PARTIAL}}", string(dashboardFooterPartial))
		c.Type("html", "utf-8")
		return c.SendString(htmlStr)
	})

	// Meta info
	app.Get("/api/reports/meta", requireSession, func(c *fiber.Ctx) error {
		statuses := []string{}
		sRows, err := pool.Query("SELECT DISTINCT status FROM cases WHERE status IS NOT NULL AND TRIM(status) != '' ORDER BY status ASC")
		if err == nil {
			defer sRows.Close()
			for sRows.Next() {
				var s string
				if err := sRows.Scan(&s); err == nil {
					statuses = append(statuses, s)
				}
			}
		}

		roles := []string{"Report Maker", "Field Officer (FO)", "Allocation Manager", "Case Manager", "ICM"}
		return c.JSON(fiber.Map{
			"status":   "success",
			"statuses": statuses,
			"roles":    roles,
		})
	})

	pointsSubquery := `(
		SELECT ppc.company_id, mct_sub.type_name as cat_name, mcst_sub.sub_type_name as subcat_name, MAX(ppc.points) as points 
		FROM performance_points_config ppc 
		LEFT JOIN master_case_types mct_sub ON mct_sub.id = ppc.category_id 
		LEFT JOIN master_case_sub_types mcst_sub ON mcst_sub.id = ppc.subcategory_id 
		GROUP BY ppc.company_id, mct_sub.type_name, mcst_sub.sub_type_name
	)`

	uFoCountSql := `(
		(CASE WHEN c.fo_hospital_id > 0 THEN 1 ELSE 0 END) +
		(CASE WHEN c.fo_patient_id > 0 AND c.fo_patient_id != COALESCE(c.fo_hospital_id, 0) THEN 1 ELSE 0 END) +
		(CASE WHEN c.fo_pharmacy_id > 0 AND c.fo_pharmacy_id NOT IN (COALESCE(c.fo_hospital_id, 0), COALESCE(c.fo_patient_id, 0)) THEN 1 ELSE 0 END) +
		(CASE WHEN c.fo_pathology_id > 0 AND c.fo_pathology_id NOT IN (COALESCE(c.fo_hospital_id, 0), COALESCE(c.fo_patient_id, 0), COALESCE(c.fo_pharmacy_id, 0)) THEN 1 ELSE 0 END) +
		(CASE WHEN c.fo_doctor_id > 0 AND c.fo_doctor_id NOT IN (COALESCE(c.fo_hospital_id, 0), COALESCE(c.fo_patient_id, 0), COALESCE(c.fo_pharmacy_id, 0), COALESCE(c.fo_pathology_id, 0)) THEN 1 ELSE 0 END)
	)`

	// POST /api/reports/get_performance_matrix
	app.Post("/api/reports/get_performance_matrix", requireSession, func(c *fiber.Ctx) error {
		role := strings.TrimSpace(c.FormValue("role"))
		month := strings.TrimSpace(c.FormValue("month"))
		if month == "" {
			month = time.Now().Format("2006-01")
		}
		staffType := strings.TrimSpace(c.FormValue("staff_type", "salary"))
		statusFilter := strings.TrimSpace(c.FormValue("status"))

		if role == "" || month == "" {
			return c.Status(400).JSON(fiber.Map{"status": "error", "message": "Role and Month are required parameters."})
		}

		isFoRole := (role == "FO" || role == "Field Officer")
		isALM := (role == "Allocation Manager")
		roleColumn := resolveRoleColumn(role)

		dateCol := "c.report_submission_date"
		completionSql := " AND c.report_submission_date IS NOT NULL "
		if isALM {
			dateCol = "COALESCE(c.allocation_date, c.created_at)"
			completionSql = ""
		}

		statusSql := ""
		if statusFilter != "" {
			statusSql = fmt.Sprintf(" AND c.status = '%s' ", strings.ReplaceAll(statusFilter, "'", "''"))
		}

		startDate := month + "-01 00:00:00"
		endParsed, _ := time.Parse("2006-01-02", month+"-01")
		if endParsed.IsZero() {
			endParsed = time.Now()
		}
		nextM := time.Date(endParsed.Year(), endParsed.Month()+1, 1, 0, 0, 0, 0, time.Local)
		endDate := nextM.AddDate(0, 0, -1).Format("2006-01-02") + " 23:59:59"

		activeUserIds := []int{}
		if isFoRole {
			q := fmt.Sprintf(`
				SELECT c.fo_hospital_id, c.fo_patient_id, c.fo_pharmacy_id, c.fo_pathology_id, c.fo_doctor_id 
				FROM cases c 
				WHERE 1=1 %s %s AND %s >= ? AND %s <= ?`, completionSql, statusSql, dateCol, dateCol)
			rows, err := pool.Query(q, startDate, endDate)
			if err == nil {
				defer rows.Close()
				idMap := make(map[int]bool)
				for rows.Next() {
					var h, p, ph, pt, d sql.NullInt64
					if err := rows.Scan(&h, &p, &ph, &pt, &d); err == nil {
						if h.Valid && h.Int64 > 0 { idMap[int(h.Int64)] = true }
						if p.Valid && p.Int64 > 0 { idMap[int(p.Int64)] = true }
						if ph.Valid && ph.Int64 > 0 { idMap[int(ph.Int64)] = true }
						if pt.Valid && pt.Int64 > 0 { idMap[int(pt.Int64)] = true }
						if d.Valid && d.Int64 > 0 { idMap[int(d.Int64)] = true }
					}
				}
				for k := range idMap {
					activeUserIds = append(activeUserIds, k)
				}
			}
		} else {
			q := fmt.Sprintf(`
				SELECT DISTINCT c.%s AS emp_id 
				FROM cases c 
				WHERE c.%s IS NOT NULL %s %s AND %s >= ? AND %s <= ?`, roleColumn, roleColumn, completionSql, statusSql, dateCol, dateCol)
			rows, err := pool.Query(q, startDate, endDate)
			if err == nil {
				defer rows.Close()
				for rows.Next() {
					var eid int
					if err := rows.Scan(&eid); err == nil && eid > 0 {
						activeUserIds = append(activeUserIds, eid)
					}
				}
			}
		}

		emptySummary := fiber.Map{
			"status": "success",
			"data":   []interface{}{},
			"summary": fiber.Map{
				"total_points":     "0.00",
				"active_employees": 0,
				"top_scorer_name":  "N/A",
				"top_scorer_pts":   0,
			},
		}

		if len(activeUserIds) == 0 {
			return c.JSON(emptySummary)
		}

		strIds := make([]string, len(activeUserIds))
		for i, id := range activeUserIds {
			strIds[i] = strconv.Itoa(id)
		}
		idsCsv := strings.Join(strIds, ",")

		isReportMakerRole := (role == "Report Maker" || role == "Back Office")
		actualRoleName := role
		if role == "Report Maker" {
			actualRoleName = "Back Office"
		}

		var dbRoleId int
		_ = pool.QueryRow("SELECT id FROM roles WHERE role_name = ?", actualRoleName).Scan(&dbRoleId)

		userQuery := fmt.Sprintf(`
			SELECT u.id, u.fullname, COALESCE(r.role_name, 'Staff') as role_name, u.employment_type 
			FROM users u 
			LEFT JOIN roles r ON u.role_id = r.id 
			WHERE u.id IN (%s)`, idsCsv)

		var userParams []interface{}
		if dbRoleId > 0 && !isReportMakerRole {
			userQuery += " AND u.role_id = ?"
			userParams = append(userParams, dbRoleId)
		}
		if staffType != "" {
			userQuery += " AND u.employment_type = ?"
			userParams = append(userParams, staffType)
		}
		userQuery += " ORDER BY u.fullname ASC"

		type uItem struct {
			ID             int
			Fullname       string
			RoleName       string
			EmploymentType string
		}
		users := []uItem{}
		uRows, err := pool.Query(userQuery, userParams...)
		if err == nil {
			defer uRows.Close()
			for uRows.Next() {
				var u uItem
				if err := uRows.Scan(&u.ID, &u.Fullname, &u.RoleName, &u.EmploymentType); err == nil {
					users = append(users, u)
				}
			}
		}

		if len(users) == 0 {
			return c.JSON(emptySummary)
		}

		groupedCases := make(map[int]map[int]int)
		groupedPoints := make(map[int]map[int]float64)

		if isFoRole {
			caseSql := fmt.Sprintf(`
				SELECT emp_id, case_day, COUNT(DISTINCT case_id) as case_count, SUM(part_pts) as day_points FROM (
					SELECT emp_id, case_id, case_day, MAX(base_points) as case_base_points, MAX(is_cashless) as is_cashless, COUNT(*) as pCount,
						   (CASE WHEN MAX(is_cashless) = 1 THEN (MAX(base_points) / COALESCE(NULLIF(MAX(unique_fo_count), 0), 1)) ELSE (MAX(base_points) / 5.0) * COUNT(*) END) as part_pts
					FROM (
						SELECT c.id as case_id, c.fo_hospital_id AS emp_id, DAY(%s) AS case_day, (COALESCE(p.points, CASE WHEN mct.type_name LIKE '%%CASHLESS%%' THEN 0.50 ELSE 1.00 END)) AS base_points, CASE WHEN mct.type_name LIKE '%%CASHLESS%%' THEN 1 ELSE 0 END as is_cashless, %s as unique_fo_count FROM cases c LEFT JOIN master_case_types mct ON mct.id = c.case_type_id LEFT JOIN master_case_sub_types mcst ON mcst.id = c.case_sub_type_id LEFT JOIN %s p ON (p.company_id = c.insurance_company_id AND p.cat_name = mct.type_name AND p.subcat_name = mcst.sub_type_name) WHERE c.fo_hospital_id IN (%s) %s %s AND %s >= ? AND %s <= ?
						UNION ALL
						SELECT c.id as case_id, c.fo_patient_id AS emp_id, DAY(%s) AS case_day, (COALESCE(p.points, CASE WHEN mct.type_name LIKE '%%CASHLESS%%' THEN 0.50 ELSE 1.00 END)) AS base_points, CASE WHEN mct.type_name LIKE '%%CASHLESS%%' THEN 1 ELSE 0 END as is_cashless, %s as unique_fo_count FROM cases c LEFT JOIN master_case_types mct ON mct.id = c.case_type_id LEFT JOIN master_case_sub_types mcst ON mcst.id = c.case_sub_type_id LEFT JOIN %s p ON (p.company_id = c.insurance_company_id AND p.cat_name = mct.type_name AND p.subcat_name = mcst.sub_type_name) WHERE c.fo_patient_id IN (%s) %s %s AND %s >= ? AND %s <= ?
						UNION ALL
						SELECT c.id as case_id, c.fo_pharmacy_id AS emp_id, DAY(%s) AS case_day, (COALESCE(p.points, CASE WHEN mct.type_name LIKE '%%CASHLESS%%' THEN 0.50 ELSE 1.00 END)) AS base_points, CASE WHEN mct.type_name LIKE '%%CASHLESS%%' THEN 1 ELSE 0 END as is_cashless, %s as unique_fo_count FROM cases c LEFT JOIN master_case_types mct ON mct.id = c.case_type_id LEFT JOIN master_case_sub_types mcst ON mcst.id = c.case_sub_type_id LEFT JOIN %s p ON (p.company_id = c.insurance_company_id AND p.cat_name = mct.type_name AND p.subcat_name = mcst.sub_type_name) WHERE c.fo_pharmacy_id IN (%s) %s %s AND %s >= ? AND %s <= ?
						UNION ALL
						SELECT c.id as case_id, c.fo_pathology_id AS emp_id, DAY(%s) AS case_day, (COALESCE(p.points, CASE WHEN mct.type_name LIKE '%%CASHLESS%%' THEN 0.50 ELSE 1.00 END)) AS base_points, CASE WHEN mct.type_name LIKE '%%CASHLESS%%' THEN 1 ELSE 0 END as is_cashless, %s as unique_fo_count FROM cases c LEFT JOIN master_case_types mct ON mct.id = c.case_type_id LEFT JOIN master_case_sub_types mcst ON mcst.id = c.case_sub_type_id LEFT JOIN %s p ON (p.company_id = c.insurance_company_id AND p.cat_name = mct.type_name AND p.subcat_name = mcst.sub_type_name) WHERE c.fo_pathology_id IN (%s) %s %s AND %s >= ? AND %s <= ?
						UNION ALL
						SELECT c.id as case_id, c.fo_doctor_id AS emp_id, DAY(%s) AS case_day, (COALESCE(p.points, CASE WHEN mct.type_name LIKE '%%CASHLESS%%' THEN 0.50 ELSE 1.00 END)) AS base_points, CASE WHEN mct.type_name LIKE '%%CASHLESS%%' THEN 1 ELSE 0 END as is_cashless, %s as unique_fo_count FROM cases c LEFT JOIN master_case_types mct ON mct.id = c.case_type_id LEFT JOIN master_case_sub_types mcst ON mcst.id = c.case_sub_type_id LEFT JOIN %s p ON (p.company_id = c.insurance_company_id AND p.cat_name = mct.type_name AND p.subcat_name = mcst.sub_type_name) WHERE c.fo_doctor_id IN (%s) %s %s AND %s >= ? AND %s <= ?
					) AS fo_union
					GROUP BY emp_id, case_id, case_day
				) AS grouped_cases
				GROUP BY emp_id, case_day
			`, dateCol, uFoCountSql, pointsSubquery, idsCsv, completionSql, statusSql, dateCol, dateCol,
				dateCol, uFoCountSql, pointsSubquery, idsCsv, completionSql, statusSql, dateCol, dateCol,
				dateCol, uFoCountSql, pointsSubquery, idsCsv, completionSql, statusSql, dateCol, dateCol,
				dateCol, uFoCountSql, pointsSubquery, idsCsv, completionSql, statusSql, dateCol, dateCol,
				dateCol, uFoCountSql, pointsSubquery, idsCsv, completionSql, statusSql, dateCol, dateCol)

			cRows, err := pool.Query(caseSql, startDate, endDate, startDate, endDate, startDate, endDate, startDate, endDate, startDate, endDate)
			if err == nil {
				defer cRows.Close()
				for cRows.Next() {
					var eid, day, cnt int
					var pts float64
					if err := cRows.Scan(&eid, &day, &cnt, &pts); err == nil {
						if _, ok := groupedCases[eid]; !ok {
							groupedCases[eid] = make(map[int]int)
							groupedPoints[eid] = make(map[int]float64)
						}
						groupedCases[eid][day] = cnt
						groupedPoints[eid][day] = pts
					}
				}
			}
		} else {
			caseSql := fmt.Sprintf(`
				SELECT 
					c.%s AS emp_id, 
					DAY(%s) AS case_day, 
					COUNT(DISTINCT c.id) AS case_count,
					SUM(COALESCE(p.points, CASE WHEN mct.type_name LIKE '%%CASHLESS%%' THEN 0.50 ELSE 1.00 END)) AS day_points
				FROM cases c 
				LEFT JOIN master_case_types mct ON mct.id = c.case_type_id
				LEFT JOIN master_case_sub_types mcst ON mcst.id = c.case_sub_type_id
				LEFT JOIN %s p ON (
					p.company_id = c.insurance_company_id 
					AND p.cat_name = mct.type_name 
					AND p.subcat_name = mcst.sub_type_name
				)
				WHERE c.%s IN (%s) 
				  %s
				  %s
				  AND %s >= ? 
				  AND %s <= ? 
				GROUP BY c.%s, DAY(%s)
			`, roleColumn, dateCol, pointsSubquery, roleColumn, idsCsv, completionSql, statusSql, dateCol, dateCol, roleColumn, dateCol)

			cRows, err := pool.Query(caseSql, startDate, endDate)
			if err == nil {
				defer cRows.Close()
				for cRows.Next() {
					var eid, day, cnt int
					var pts float64
					if err := cRows.Scan(&eid, &day, &cnt, &pts); err == nil {
						if _, ok := groupedCases[eid]; !ok {
							groupedCases[eid] = make(map[int]int)
							groupedPoints[eid] = make(map[int]float64)
						}
						groupedCases[eid][day] = cnt
						groupedPoints[eid][day] = pts
					}
				}
			}
		}

		matrixRows := []fiber.Map{}
		totalMonthPoints := 0.0
		activeEmpCount := 0
		topScorerName := "N/A"
		topScorerPts := 0.0

		for _, usr := range users {
			empId := usr.ID
			dailyMap := make(map[string]fiber.Map)
			totalCases := 0
			totalPoints := 0.0

			for d := 1; d <= 31; d++ {
				cnt := 0
				pts := 0.0
				if m, ok := groupedCases[empId]; ok {
					cnt = m[d]
				}
				if m, ok := groupedPoints[empId]; ok {
					pts = m[d]
				}

				dailyMap[strconv.Itoa(d)] = fiber.Map{
					"cases":  cnt,
					"points": math.Round(pts*100) / 100,
				}
				totalCases += cnt
				totalPoints += pts
			}

			totalMonthPoints += totalPoints
			if totalCases > 0 {
				activeEmpCount++
			}
			if totalPoints > topScorerPts {
				topScorerPts = totalPoints
				topScorerName = usr.Fullname
			}

			displayRole := usr.RoleName
			if isReportMakerRole {
				displayRole = "Report Maker"
			}

			matrixRows = append(matrixRows, fiber.Map{
				"employee_id":   empId,
				"employee_name": usr.Fullname,
				"role_title":    displayRole,
				"daily":         dailyMap,
				"total_cases":   totalCases,
				"total_points":  math.Round(totalPoints*100) / 100,
			})
		}

		return c.JSON(fiber.Map{
			"status": "success",
			"data":   matrixRows,
			"summary": fiber.Map{
				"total_points":     fmt.Sprintf("%.2f", totalMonthPoints),
				"active_employees": activeEmpCount,
				"top_scorer_name":  topScorerName,
				"top_scorer_pts":   math.Round(topScorerPts*100) / 100,
			},
		})
	})

	// Helper for popup HTML
	renderDetailPopup := func(cases []fiber.Map, empName, subHeader, role string, isFoRole bool) string {
		tableRows := ""
		verdictCounts := make(map[string]int)
		totalVol := 0
		totalPts := 0.0

		for idx, c := range cases {
			sn := idx + 1
			claimNo := html.EscapeString(fmt.Sprintf("%v", c["claim_number"]))
			patientName := html.EscapeString(fmt.Sprintf("%v", c["patient_name"]))
			insName := html.EscapeString(fmt.Sprintf("%v", c["insurance_name"]))
			hospName := html.EscapeString(fmt.Sprintf("%v", c["hospital_name"]))
			subcatName := html.EscapeString(fmt.Sprintf("%v", c["subcat_name"]))
			verdictStr := strings.TrimSpace(fmt.Sprintf("%v", c["status"]))
			earnedPts, _ := c["earned_pts"].(float64)
			allocDateStr := fmt.Sprintf("%v", c["alloc_date_str"])
			closedDateStr := fmt.Sprintf("%v", c["closed_date_str"])
			timeTakenStr := fmt.Sprintf("%v", c["time_taken_str"])
			tatBadge := fmt.Sprintf("%v", c["tat_badge"])
			partDoneBadge := fmt.Sprintf("%v", c["part_done_badge"])

			verdictCounts[verdictStr]++
			totalVol++
			totalPts += earnedPts

			verdictBadge := fmt.Sprintf("<span class='badge bg-warning text-dark py-1 px-2 fw-bold shadow-sm w-100'>%s</span>", verdictStr)
			vLower := strings.ToLower(verdictStr)
			if strings.Contains(vLower, "pay") || strings.Contains(vLower, "appr") || strings.Contains(vLower, "clos") {
				verdictBadge = fmt.Sprintf("<span class='badge bg-success text-white py-1 px-2 fw-bold shadow-sm w-100'><i class='fa-solid fa-circle-check me-1'></i> %s</span>", verdictStr)
			} else if strings.Contains(vLower, "rej") || strings.Contains(vLower, "repud") || strings.Contains(vLower, "canc") {
				verdictBadge = fmt.Sprintf("<span class='badge bg-danger text-white py-1 px-2 fw-bold shadow-sm w-100'><i class='fa-solid fa-circle-xmark me-1'></i> %s</span>", verdictStr)
			}

			tableRows += fmt.Sprintf(`
				<tr>
					<td class='text-center fw-bold text-muted'>%d</td>
					<td class='fw-bold text-primary text-wrap'>%s</td>
					<td class='fw-semibold text-dark text-wrap'>%s</td>
					<td class='fw-semibold text-dark text-wrap'>%s</td>
					<td class='text-wrap'>%s</td>
					<td class='text-wrap'>%s</td>
					<td class='text-center'>%s</td>
					<td><div class='small text-muted' style='font-size:0.65rem;'><span class='text-primary fw-bold d-block mb-1'>A: %s</span><span class='text-danger fw-bold d-block'>C: %s</span></div></td>
					<td class='fw-bold text-dark text-center text-wrap'>%s</td>
					<td class='text-center border-0 p-2'><div class='d-flex flex-column gap-1 align-items-center justify-content-center w-100'>%s%s</div></td>
					<td class='text-center fw-extrabold text-warning'>%.2f</td>
				</tr>
			`, sn, claimNo, patientName, insName, hospName, subcatName, partDoneBadge, allocDateStr, closedDateStr, timeTakenStr, tatBadge, verdictBadge, earnedPts)
		}

		if tableRows == "" {
			tableRows = "<tr><td colspan='11' class='text-center py-5 text-muted fw-bold'>No case records found for the selected filter.</td></tr>"
		}

		verdictChipsHtml := ""
		for vName, vCnt := range verdictCounts {
			chipBg := "bg-primary"
			vLower := strings.ToLower(vName)
			if strings.Contains(vLower, "pay") || strings.Contains(vLower, "appr") {
				chipBg = "bg-success"
			} else if strings.Contains(vLower, "rej") {
				chipBg = "bg-danger"
			} else if strings.Contains(vLower, "pend") {
				chipBg = "bg-warning text-dark"
			}
			verdictChipsHtml += fmt.Sprintf("<span class='badge %s p-2 fs-7 fw-bold shadow-sm me-2 mb-1'>%s: %d</span>", chipBg, vName, vCnt)
		}

		return fmt.Sprintf(`
			<div class='p-3 bg-white text-dark rounded-3 mb-3 border shadow-sm d-flex flex-wrap align-items-center justify-content-between gap-3'>
				<div>
					<span class='badge bg-primary text-uppercase px-2 py-1 mb-1'>%s Report</span>
					<h5 class='m-0 fw-extrabold text-dark'><i class='fa-solid fa-user-check me-2 text-primary'></i> %s</h5>
					<small class='text-muted'>Filter: <strong>%s</strong></small>
				</div>
				<div class='d-flex align-items-center gap-3'>
					<div class='text-center px-3 py-2 bg-light rounded-3 border'>
						<small class='d-block text-uppercase fw-bold text-muted' style='font-size:0.65rem;'>Total Cases</small>
						<strong class='fs-4 text-primary fw-extrabold'>%d</strong>
					</div>
					<div class='text-center px-3 py-2 bg-light rounded-3 border'>
						<small class='d-block text-uppercase fw-bold text-muted' style='font-size:0.65rem;'>Total Points</small>
						<strong class='fs-4 text-warning fw-extrabold'>%.2f</strong>
					</div>
				</div>
			</div>

			<div class='p-3 bg-light rounded-3 border mb-3 shadow-sm'>
				<div class='text-uppercase fw-bold text-muted small mb-2'><i class='fa-solid fa-chart-pie me-1 text-primary'></i> Verdict / Status Summary Breakdown:</div>
				<div>%s</div>
			</div>

			<div class='table-responsive rounded-3 border shadow-sm' style='max-height: 480px; overflow-y: auto; overflow-x: hidden;'>
				<table class='table table-hover align-middle mb-0' style='font-size: 0.70rem; table-layout: fixed; width: 100%%; word-wrap: break-word;'>
					<thead style='background-color: #f8fafc; color: #0f172a; border-bottom: 2px solid #e2e8f0;'>
						<tr>
							<th class='text-center' style='width:3%%; background-color:#f8fafc; color:#0f172a;'>#</th>
							<th style='width:9%%; background-color:#f8fafc; color:#0f172a;'>Claim #</th>
							<th style='width:12%%; background-color:#f8fafc; color:#0f172a;'>Patient</th>
							<th style='width:12%%; background-color:#f8fafc; color:#0f172a;'>Insurance</th>
							<th style='width:13%%; background-color:#f8fafc; color:#0f172a;'>Hospital</th>
							<th style='width:10%%; background-color:#f8fafc; color:#0f172a;'>Subtype</th>
							<th class='text-center' style='width:7%%; background-color:#f8fafc; color:#0f172a;'>Part</th>
							<th style='width:12%%; background-color:#f8fafc; color:#0f172a;'>Timeline</th>
							<th class='text-center' style='width:9%%; background-color:#f8fafc; color:#0f172a;'>Time Taken</th>
							<th class='text-center' style='width:8%%; background-color:#f8fafc; color:#0f172a;'>Status</th>
							<th class='text-center' style='width:5%%; background-color:#f8fafc; color:#0f172a;'>Pts</th>
						</tr>
					</thead>
					<tbody>
						%s
					</tbody>
				</table>
			</div>

			<div class='p-3 bg-white text-dark rounded-3 border d-flex justify-content-between align-items-center mt-3 shadow-sm'>
				<div class='fw-bold'><i class='fa-solid fa-folder-open me-2 text-primary'></i> Total Case Volume: <span class='badge bg-primary fs-6 ms-1'>%d Cases</span></div>
				<div class='fw-bold'><i class='fa-solid fa-star me-2 text-warning'></i> Total Accumulated Points: <span class='badge bg-warning text-dark fs-6 ms-1'>%.2f Pts</span></div>
			</div>
		`, role, empName, subHeader, totalVol, totalPts, verdictChipsHtml, tableRows, totalVol, totalPts)
	}

	// POST /api/reports/get_day_cases_detail
	app.Post("/api/reports/get_day_cases_detail", requireSession, func(c *fiber.Ctx) error {
		empId, _ := strconv.Atoi(c.FormValue("employee_id"))
		date := strings.TrimSpace(c.FormValue("date"))
		role := strings.TrimSpace(c.FormValue("role"))
		statusFilter := strings.TrimSpace(c.FormValue("status"))

		if empId <= 0 || date == "" {
			return c.JSON(fiber.Map{"status": "error", "message": "Invalid parameters."})
		}

		isFoRole := (role == "FO" || role == "Field Officer")
		isALM := (role == "Allocation Manager")
		roleColumn := resolveRoleColumn(role)

		dateCol := "c.report_submission_date"
		completionSql := " AND c.report_submission_date IS NOT NULL "
		if isALM {
			dateCol = "COALESCE(c.allocation_date, c.created_at)"
			completionSql = ""
		}

		statusSql := ""
		if statusFilter != "" {
			statusSql = fmt.Sprintf(" AND c.status = '%s' ", strings.ReplaceAll(statusFilter, "'", "''"))
		}

		whereCond := fmt.Sprintf("c.%s = %d", roleColumn, empId)
		if isFoRole {
			whereCond = fmt.Sprintf("(c.fo_hospital_id = %d OR c.fo_patient_id = %d OR c.fo_pharmacy_id = %d OR c.fo_pathology_id = %d OR c.fo_doctor_id = %d)", empId, empId, empId, empId, empId)
		}

		sqlQuery := fmt.Sprintf(`
			SELECT c.id, c.claim_number, c.status, c.patient_name, c.hospital_name,
				   c.insurance_company_id, c.case_type_id, c.case_sub_type_id, mct.type_name,
				   COALESCE(c.allocation_date, c.created_at) AS allocated_on,
				   c.report_submission_date AS closed_on,
				   c.fo_hospital_id, c.fo_patient_id, c.fo_pharmacy_id, c.fo_pathology_id, c.fo_doctor_id,
				   COALESCE(p.points, CASE WHEN mct.type_name LIKE '%%CASHLESS%%' THEN 0.50 ELSE 1.00 END) as base_points,
				   ic.company_name, mcst.sub_type_name
			FROM cases c
			LEFT JOIN master_case_types mct ON mct.id = c.case_type_id
			LEFT JOIN master_case_sub_types mcst ON mcst.id = c.case_sub_type_id
			LEFT JOIN master_insurance_companies ic ON ic.id = c.insurance_company_id
			LEFT JOIN %s p ON (
				p.company_id = c.insurance_company_id
				AND p.cat_name = mct.type_name 
				AND p.subcat_name = mcst.sub_type_name
			)
			WHERE %s %s %s AND DATE(%s) = ?
			ORDER BY c.id DESC`, pointsSubquery, whereCond, completionSql, statusSql, dateCol)

		cases := []fiber.Map{}
		rows, err := pool.Query(sqlQuery, date)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var id int
				var claimNo, status, patientName, hospName, typeName, compName, subName sql.NullString
				var insId, typeId, subId, foH, foP, foPh, foPt, foD sql.NullInt64
				var allocOn, closedOn sql.NullTime
				var basePts float64

				if err := rows.Scan(&id, &claimNo, &status, &patientName, &hospName, &insId, &typeId, &subId, &typeName, &allocOn, &closedOn, &foH, &foP, &foPh, &foPt, &foD, &basePts, &compName, &subName); err == nil {
					earned := calcEarnedPoints(basePts, typeName.String, isFoRole, int(foH.Int64), int(foP.Int64), int(foPh.Int64), int(foPt.Int64), int(foD.Int64), empId)
					
					allocStr := "N/A"
					if allocOn.Valid {
						allocStr = allocOn.Time.Format("02-Jan-06 15:04")
					}
					closedStr := "Running"
					if closedOn.Valid {
						closedStr = closedOn.Time.Format("02-Jan-06 15:04")
					}

					timeTakenStr := "--"
					tatBadge := "<span class='badge bg-warning bg-opacity-10 text-warning border border-warning border-opacity-25 py-1 px-2 fw-bold w-100 mb-1'><i class='fa-solid fa-spinner fa-spin me-1'></i> Running</span>"
					if allocOn.Valid && closedOn.Valid {
						diff := closedOn.Time.Sub(allocOn.Time)
						if diff < 0 {
							diff = 0
						}
						d := int(diff.Hours()) / 24
						h := int(diff.Hours()) % 24
						m := int(diff.Minutes()) % 60
						timeTakenStr = fmt.Sprintf("%dd %dh %dm", d, h, m)
						if diff.Hours() <= 48 {
							tatBadge = "<span class='badge bg-success bg-opacity-10 text-success border border-success border-opacity-25 py-1 px-2 fw-bold w-100 mb-1'><i class='fa-solid fa-check-circle me-1'></i> On Time</span>"
						} else {
							tatBadge = "<span class='badge bg-danger bg-opacity-10 text-danger border border-danger border-opacity-25 py-1 px-2 fw-bold w-100 mb-1'><i class='fa-solid fa-clock me-1'></i> Delayed</span>"
						}
					}

					partDoneBadge := "<span class='badge bg-secondary border border-secondary border-opacity-25 py-1 px-2 fw-bold w-100'>Full</span>"
					if isFoRole {
						partDoneBadge = "<span class='badge bg-primary text-white py-1 px-2 fw-bold w-100'>Field Part</span>"
					}

					cases = append(cases, fiber.Map{
						"claim_number":    claimNo.String,
						"patient_name":    patientName.String,
						"insurance_name":  compName.String,
						"hospital_name":   hospName.String,
						"subcat_name":     subName.String,
						"status":          status.String,
						"earned_pts":      earned,
						"alloc_date_str":  allocStr,
						"closed_date_str": closedStr,
						"time_taken_str":  timeTakenStr,
						"tat_badge":       tatBadge,
						"part_done_badge": partDoneBadge,
					})
				}
			}
		}

		var empName string
		_ = pool.QueryRow("SELECT fullname FROM users WHERE id = ?", empId).Scan(&empName)
		if empName == "" {
			empName = "Employee"
		}

		htmlContent := renderDetailPopup(cases, empName, "Date: "+date, role, isFoRole)
		return c.JSON(fiber.Map{"status": "success", "html": htmlContent})
	})

	// POST /api/reports/get_month_cases_detail
	app.Post("/api/reports/get_month_cases_detail", requireSession, func(c *fiber.Ctx) error {
		empId, _ := strconv.Atoi(c.FormValue("employee_id"))
		month := strings.TrimSpace(c.FormValue("month"))
		role := strings.TrimSpace(c.FormValue("role"))
		statusFilter := strings.TrimSpace(c.FormValue("status"))

		if empId <= 0 || month == "" {
			return c.JSON(fiber.Map{"status": "error", "message": "Invalid parameters."})
		}

		isFoRole := (role == "FO" || role == "Field Officer")
		isALM := (role == "Allocation Manager")
		roleColumn := resolveRoleColumn(role)

		dateCol := "c.report_submission_date"
		completionSql := " AND c.report_submission_date IS NOT NULL "
		if isALM {
			dateCol = "COALESCE(c.allocation_date, c.created_at)"
			completionSql = ""
		}

		startDate := month + "-01 00:00:00"
		endParsed, _ := time.Parse("2006-01-02", month+"-01")
		if endParsed.IsZero() {
			endParsed = time.Now()
		}
		nextM := time.Date(endParsed.Year(), endParsed.Month()+1, 1, 0, 0, 0, 0, time.Local)
		endDate := nextM.AddDate(0, 0, -1).Format("2006-01-02") + " 23:59:59"

		statusSql := ""
		if statusFilter != "" {
			statusSql = fmt.Sprintf(" AND c.status = '%s' ", strings.ReplaceAll(statusFilter, "'", "''"))
		}

		whereCond := fmt.Sprintf("c.%s = %d", roleColumn, empId)
		if isFoRole {
			whereCond = fmt.Sprintf("(c.fo_hospital_id = %d OR c.fo_patient_id = %d OR c.fo_pharmacy_id = %d OR c.fo_pathology_id = %d OR c.fo_doctor_id = %d)", empId, empId, empId, empId, empId)
		}

		sqlQuery := fmt.Sprintf(`
			SELECT c.id, c.claim_number, c.status, c.patient_name, c.hospital_name,
				   c.insurance_company_id, c.case_type_id, c.case_sub_type_id, mct.type_name,
				   COALESCE(c.allocation_date, c.created_at) AS allocated_on,
				   c.report_submission_date AS closed_on,
				   c.fo_hospital_id, c.fo_patient_id, c.fo_pharmacy_id, c.fo_pathology_id, c.fo_doctor_id,
				   COALESCE(p.points, CASE WHEN mct.type_name LIKE '%%CASHLESS%%' THEN 0.50 ELSE 1.00 END) as base_points,
				   ic.company_name, mcst.sub_type_name
			FROM cases c
			LEFT JOIN master_case_types mct ON mct.id = c.case_type_id
			LEFT JOIN master_case_sub_types mcst ON mcst.id = c.case_sub_type_id
			LEFT JOIN master_insurance_companies ic ON ic.id = c.insurance_company_id
			LEFT JOIN %s p ON (
				p.company_id = c.insurance_company_id
				AND p.cat_name = mct.type_name 
				AND p.subcat_name = mcst.sub_type_name
			)
			WHERE %s %s %s AND %s >= ? AND %s <= ?
			ORDER BY c.id DESC`, pointsSubquery, whereCond, completionSql, statusSql, dateCol, dateCol)

		cases := []fiber.Map{}
		rows, err := pool.Query(sqlQuery, startDate, endDate)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var id int
				var claimNo, status, patientName, hospName, typeName, compName, subName sql.NullString
				var insId, typeId, subId, foH, foP, foPh, foPt, foD sql.NullInt64
				var allocOn, closedOn sql.NullTime
				var basePts float64

				if err := rows.Scan(&id, &claimNo, &status, &patientName, &hospName, &insId, &typeId, &subId, &typeName, &allocOn, &closedOn, &foH, &foP, &foPh, &foPt, &foD, &basePts, &compName, &subName); err == nil {
					earned := calcEarnedPoints(basePts, typeName.String, isFoRole, int(foH.Int64), int(foP.Int64), int(foPh.Int64), int(foPt.Int64), int(foD.Int64), empId)
					
					allocStr := "N/A"
					if allocOn.Valid {
						allocStr = allocOn.Time.Format("02-Jan-06 15:04")
					}
					closedStr := "Running"
					if closedOn.Valid {
						closedStr = closedOn.Time.Format("02-Jan-06 15:04")
					}

					timeTakenStr := "--"
					tatBadge := "<span class='badge bg-warning bg-opacity-10 text-warning border border-warning border-opacity-25 py-1 px-2 fw-bold w-100 mb-1'><i class='fa-solid fa-spinner fa-spin me-1'></i> Running</span>"
					if allocOn.Valid && closedOn.Valid {
						diff := closedOn.Time.Sub(allocOn.Time)
						if diff < 0 {
							diff = 0
						}
						d := int(diff.Hours()) / 24
						h := int(diff.Hours()) % 24
						m := int(diff.Minutes()) % 60
						timeTakenStr = fmt.Sprintf("%dd %dh %dm", d, h, m)
						if diff.Hours() <= 48 {
							tatBadge = "<span class='badge bg-success bg-opacity-10 text-success border border-success border-opacity-25 py-1 px-2 fw-bold w-100 mb-1'><i class='fa-solid fa-check-circle me-1'></i> On Time</span>"
						} else {
							tatBadge = "<span class='badge bg-danger bg-opacity-10 text-danger border border-danger border-opacity-25 py-1 px-2 fw-bold w-100 mb-1'><i class='fa-solid fa-clock me-1'></i> Delayed</span>"
						}
					}

					partDoneBadge := "<span class='badge bg-secondary border border-secondary border-opacity-25 py-1 px-2 fw-bold w-100'>Full</span>"
					if isFoRole {
						partDoneBadge = "<span class='badge bg-primary text-white py-1 px-2 fw-bold w-100'>Field Part</span>"
					}

					cases = append(cases, fiber.Map{
						"claim_number":    claimNo.String,
						"patient_name":    patientName.String,
						"insurance_name":  compName.String,
						"hospital_name":   hospName.String,
						"subcat_name":     subName.String,
						"status":          status.String,
						"earned_pts":      earned,
						"alloc_date_str":  allocStr,
						"closed_date_str": closedStr,
						"time_taken_str":  timeTakenStr,
						"tat_badge":       tatBadge,
						"part_done_badge": partDoneBadge,
					})
				}
			}
		}

		var empName string
		_ = pool.QueryRow("SELECT fullname FROM users WHERE id = ?", empId).Scan(&empName)
		if empName == "" {
			empName = "Employee"
		}

		htmlContent := renderDetailPopup(cases, empName, "Month: "+month, role, isFoRole)
		return c.JSON(fiber.Map{"status": "success", "html": htmlContent})
	})

	// POST /api/reports/get_employee_12month_summary
	app.Post("/api/reports/get_employee_12month_summary", requireSession, func(c *fiber.Ctx) error {
		empId, _ := strconv.Atoi(c.FormValue("employee_id"))
		month := strings.TrimSpace(c.FormValue("month"))
		role := strings.TrimSpace(c.FormValue("role"))
		statusFilter := strings.TrimSpace(c.FormValue("status"))

		if empId <= 0 || role == "" || month == "" {
			return c.JSON(fiber.Map{"status": "error", "message": "Invalid parameters."})
		}

		isFoRole := (role == "FO" || role == "Field Officer")
		isALM := (role == "Allocation Manager")
		roleColumn := resolveRoleColumn(role)

		dateCol := "c.report_submission_date"
		completionSql := " AND c.report_submission_date IS NOT NULL "
		if isALM {
			dateCol = "COALESCE(c.allocation_date, c.created_at)"
			completionSql = ""
		}

		curDate, _ := time.Parse("2006-01-02", month+"-01")
		if curDate.IsZero() {
			curDate = time.Now()
		}

		type monthBucket struct {
			Key    string  `json:"key"`
			Label  string  `json:"label"`
			Cases  int     `json:"cases"`
			Points float64 `json:"points"`
		}

		buckets := make([]monthBucket, 12)
		bMap := make(map[string]int)

		for i := 11; i >= 0; i-- {
			mDate := curDate.AddDate(0, -i, 0)
			k := mDate.Format("2006-01")
			idx := 11 - i
			buckets[idx] = monthBucket{
				Key:    k,
				Label:  mDate.Format("Jan 2006"),
				Cases:  0,
				Points: 0.0,
			}
			bMap[k] = idx
		}

		startRange := buckets[0].Key + "-01 00:00:00"
		nextM := curDate.AddDate(0, 1, 0)
		endRange := nextM.AddDate(0, 0, -1).Format("2006-01-02") + " 23:59:59"

		statusSql := ""
		if statusFilter != "" {
			statusSql = fmt.Sprintf(" AND c.status = '%s' ", strings.ReplaceAll(statusFilter, "'", "''"))
		}

		whereCond := fmt.Sprintf("c.%s = %d", roleColumn, empId)
		if isFoRole {
			whereCond = fmt.Sprintf("(c.fo_hospital_id = %d OR c.fo_patient_id = %d OR c.fo_pharmacy_id = %d OR c.fo_pathology_id = %d OR c.fo_doctor_id = %d)", empId, empId, empId, empId, empId)
		}

		sqlQ := fmt.Sprintf(`
			SELECT %s as metric_date, mct.type_name,
				   c.fo_hospital_id, c.fo_patient_id, c.fo_pharmacy_id, c.fo_pathology_id, c.fo_doctor_id,
				   COALESCE(p.points, CASE WHEN mct.type_name LIKE '%%CASHLESS%%' THEN 0.50 ELSE 1.00 END) as base_points
			FROM cases c
			LEFT JOIN master_case_types mct ON mct.id = c.case_type_id
			LEFT JOIN master_case_sub_types mcst ON mcst.id = c.case_sub_type_id
			LEFT JOIN %s p ON (
				p.company_id = c.insurance_company_id
				AND p.cat_name = mct.type_name 
				AND p.subcat_name = mcst.sub_type_name
			)
			WHERE %s %s %s AND %s >= ? AND %s <= ?`, dateCol, pointsSubquery, whereCond, completionSql, statusSql, dateCol, dateCol)

		rows, err := pool.Query(sqlQ, startRange, endRange)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var mDate sql.NullTime
				var typeName sql.NullString
				var foH, foP, foPh, foPt, foD sql.NullInt64
				var basePts float64

				if err := rows.Scan(&mDate, &typeName, &foH, &foP, &foPh, &foPt, &foD, &basePts); err == nil && mDate.Valid {
					k := mDate.Time.Format("2006-01")
					if idx, ok := bMap[k]; ok {
						earned := calcEarnedPoints(basePts, typeName.String, isFoRole, int(foH.Int64), int(foP.Int64), int(foPh.Int64), int(foPt.Int64), int(foD.Int64), empId)
						buckets[idx].Cases++
						buckets[idx].Points += earned
					}
				}
			}
		}

		for i := range buckets {
			buckets[i].Points = math.Round(buckets[i].Points*100) / 100
		}

		var empName string
		_ = pool.QueryRow("SELECT fullname FROM users WHERE id = ?", empId).Scan(&empName)

		return c.JSON(fiber.Map{
			"status":        "success",
			"employee_name": empName,
			"months":        buckets,
		})
	})

	// POST /api/reports/get_six_month_summary
	app.Post("/api/reports/get_six_month_summary", requireSession, func(c *fiber.Ctx) error {
		role := strings.TrimSpace(c.FormValue("role"))
		month := strings.TrimSpace(c.FormValue("month"))
		staffType := strings.TrimSpace(c.FormValue("staff_type", "salary"))
		statusFilter := strings.TrimSpace(c.FormValue("status"))

		if role == "" || month == "" {
			return c.Status(400).JSON(fiber.Map{"status": "error", "message": "Role and Month are required."})
		}

		isFoRole := (role == "FO" || role == "Field Officer")
		isALM := (role == "Allocation Manager")
		roleColumn := resolveRoleColumn(role)

		dateCol := "c.report_submission_date"
		completionSql := " AND c.report_submission_date IS NOT NULL "
		if isALM {
			dateCol = "COALESCE(c.allocation_date, c.created_at)"
			completionSql = ""
		}

		curDate, _ := time.Parse("2006-01-02", month+"-01")
		if curDate.IsZero() {
			curDate = time.Now()
		}

		type mItem struct {
			Key   string `json:"key"`
			Label string `json:"label"`
		}
		months := make([]mItem, 6)
		for i := 5; i >= 0; i-- {
			mDate := curDate.AddDate(0, -i, 0)
			months[5-i] = mItem{
				Key:   mDate.Format("2006-01"),
				Label: mDate.Format("Jan 2006"),
			}
		}

		startDate := months[0].Key + "-01 00:00:00"
		nextM := curDate.AddDate(0, 1, 0)
		endDate := nextM.AddDate(0, 0, -1).Format("2006-01-02") + " 23:59:59"

		statusSql := ""
		if statusFilter != "" {
			statusSql = fmt.Sprintf(" AND c.status = '%s' ", strings.ReplaceAll(statusFilter, "'", "''"))
		}

		activeUserIds := []int{}
		if isFoRole {
			q := fmt.Sprintf(`
				SELECT c.fo_hospital_id, c.fo_patient_id, c.fo_pharmacy_id, c.fo_pathology_id, c.fo_doctor_id 
				FROM cases c 
				WHERE 1=1 %s %s AND %s >= ? AND %s <= ?`, completionSql, statusSql, dateCol, dateCol)
			rows, err := pool.Query(q, startDate, endDate)
			if err == nil {
				defer rows.Close()
				idMap := make(map[int]bool)
				for rows.Next() {
					var h, p, ph, pt, d sql.NullInt64
					if err := rows.Scan(&h, &p, &ph, &pt, &d); err == nil {
						if h.Valid && h.Int64 > 0 { idMap[int(h.Int64)] = true }
						if p.Valid && p.Int64 > 0 { idMap[int(p.Int64)] = true }
						if ph.Valid && ph.Int64 > 0 { idMap[int(ph.Int64)] = true }
						if pt.Valid && pt.Int64 > 0 { idMap[int(pt.Int64)] = true }
						if d.Valid && d.Int64 > 0 { idMap[int(d.Int64)] = true }
					}
				}
				for k := range idMap {
					activeUserIds = append(activeUserIds, k)
				}
			}
		} else {
			q := fmt.Sprintf(`
				SELECT DISTINCT c.%s AS emp_id 
				FROM cases c 
				WHERE c.%s IS NOT NULL %s %s AND %s >= ? AND %s <= ?`, roleColumn, roleColumn, completionSql, statusSql, dateCol, dateCol)
			rows, err := pool.Query(q, startDate, endDate)
			if err == nil {
				defer rows.Close()
				for rows.Next() {
					var eid int
					if err := rows.Scan(&eid); err == nil && eid > 0 {
						activeUserIds = append(activeUserIds, eid)
					}
				}
			}
		}

		if len(activeUserIds) == 0 {
			return c.JSON(fiber.Map{
				"status":       "success",
				"months":       months,
				"rows":         []interface{}{},
				"month_totals": fiber.Map{},
				"grand_cases":  0,
				"grand_points": 0.0,
			})
		}

		strIds := make([]string, len(activeUserIds))
		for i, id := range activeUserIds {
			strIds[i] = strconv.Itoa(id)
		}
		idsCsv := strings.Join(strIds, ",")

		isReportMakerRole := (role == "Report Maker" || role == "Back Office")
		actualRoleName := role
		if role == "Report Maker" {
			actualRoleName = "Back Office"
		}
		var dbRoleId int
		_ = pool.QueryRow("SELECT id FROM roles WHERE role_name = ?", actualRoleName).Scan(&dbRoleId)

		userQuery := fmt.Sprintf("SELECT u.id, u.fullname FROM users u WHERE u.id IN (%s)", idsCsv)
		var userParams []interface{}
		if dbRoleId > 0 && !isReportMakerRole {
			userQuery += " AND u.role_id = ?"
			userParams = append(userParams, dbRoleId)
		}
		if staffType != "" {
			userQuery += " AND u.employment_type = ?"
			userParams = append(userParams, staffType)
		}
		userQuery += " ORDER BY u.fullname ASC"

		type uItem struct {
			ID       int
			Fullname string
		}
		users := []uItem{}
		uRows, err := pool.Query(userQuery, userParams...)
		if err == nil {
			defer uRows.Close()
			for uRows.Next() {
				var u uItem
				if err := uRows.Scan(&u.ID, &u.Fullname); err == nil {
					users = append(users, u)
				}
			}
		}

		type rowMonth struct {
			Key    string  `json:"key"`
			Label  string  `json:"label"`
			Cases  int     `json:"cases"`
			Points float64 `json:"points"`
		}

		monthTotals := make(map[string]fiber.Map)
		for _, m := range months {
			monthTotals[m.Key] = fiber.Map{"cases": 0, "points": 0.0}
		}

		grandCases := 0
		grandPoints := 0.0
		rowsRes := []fiber.Map{}

		for _, usr := range users {
			empId := usr.ID
			bMap := make(map[string]int)
			bList := make([]rowMonth, len(months))
			for i, m := range months {
				bList[i] = rowMonth{Key: m.Key, Label: m.Label, Cases: 0, Points: 0.0}
				bMap[m.Key] = i
			}

			whereCond := fmt.Sprintf("c.%s = %d", roleColumn, empId)
			if isFoRole {
				whereCond = fmt.Sprintf("(c.fo_hospital_id = %d OR c.fo_patient_id = %d OR c.fo_pharmacy_id = %d OR c.fo_pathology_id = %d OR c.fo_doctor_id = %d)", empId, empId, empId, empId, empId)
			}

			q := fmt.Sprintf(`
				SELECT %s as metric_date, mct.type_name,
					   c.fo_hospital_id, c.fo_patient_id, c.fo_pharmacy_id, c.fo_pathology_id, c.fo_doctor_id,
					   COALESCE(p.points, CASE WHEN mct.type_name LIKE '%%CASHLESS%%' THEN 0.50 ELSE 1.00 END) as base_points
				FROM cases c
				LEFT JOIN master_case_types mct ON mct.id = c.case_type_id
				LEFT JOIN master_case_sub_types mcst ON mcst.id = c.case_sub_type_id
				LEFT JOIN %s p ON (
					p.company_id = c.insurance_company_id
					AND p.cat_name = mct.type_name 
					AND p.subcat_name = mcst.sub_type_name
				)
				WHERE %s %s %s AND %s >= ? AND %s <= ?`, dateCol, pointsSubquery, whereCond, completionSql, statusSql, dateCol, dateCol)

			rws, err := pool.Query(q, startDate, endDate)
			if err == nil {
				defer rws.Close()
				for rws.Next() {
					var mDate sql.NullTime
					var typeName sql.NullString
					var foH, foP, foPh, foPt, foD sql.NullInt64
					var basePts float64
					if err := rws.Scan(&mDate, &typeName, &foH, &foP, &foPh, &foPt, &foD, &basePts); err == nil && mDate.Valid {
						k := mDate.Time.Format("2006-01")
						if idx, ok := bMap[k]; ok {
							pts := calcEarnedPoints(basePts, typeName.String, isFoRole, int(foH.Int64), int(foP.Int64), int(foPh.Int64), int(foPt.Int64), int(foD.Int64), empId)
							bList[idx].Cases++
							bList[idx].Points += pts
						}
					}
				}
			}

			totC := 0
			totP := 0.0
			for i := range bList {
				bList[i].Points = math.Round(bList[i].Points*100) / 100
				totC += bList[i].Cases
				totP += bList[i].Points

				curTot := monthTotals[bList[i].Key]
				curTot["cases"] = curTot["cases"].(int) + bList[i].Cases
				curTot["points"] = math.Round((curTot["points"].(float64)+bList[i].Points)*100) / 100
			}

			grandCases += totC
			grandPoints += totP

			rowsRes = append(rowsRes, fiber.Map{
				"employee_id":   empId,
				"employee_name": usr.Fullname,
				"months":        bList,
				"total_cases":   totC,
				"total_points":  math.Round(totP*100) / 100,
			})
		}

		return c.JSON(fiber.Map{
			"status":       "success",
			"months":       months,
			"rows":         rowsRes,
			"month_totals": monthTotals,
			"grand_cases":  grandCases,
			"grand_points": math.Round(grandPoints*100) / 100,
		})
	})

	// GET /reports/view_case_detail
	app.Get("/reports/view_case_detail", requireSession, func(c *fiber.Ctx) error {
		empId, _ := strconv.Atoi(c.Query("employee_id"))
		date := strings.TrimSpace(c.Query("date"))
		month := strings.TrimSpace(c.Query("month"))
		role := strings.TrimSpace(c.Query("role"))
		statusFilter := strings.TrimSpace(c.Query("status"))
		_ = statusFilter

		if empId <= 0 || (date == "" && month == "") {
			return c.Status(400).SendString("<h3>Invalid parameters.</h3>")
		}

		var empName string
		_ = pool.QueryRow("SELECT fullname FROM users WHERE id = ?", empId).Scan(&empName)
		if empName == "" {
			empName = "Employee"
		}

		filterHeader := "Date: " + date
		if date == "" {
			filterHeader = "Month: " + month
		}

		htmlContent := renderDetailPopup([]fiber.Map{}, empName, filterHeader, role, (role == "FO" || role == "Field Officer"))
		page := fmt.Sprintf(`<!DOCTYPE html><html><head><meta charset="UTF-8"><title>Case Details - %s</title><link href="https://cdn.jsdelivr.net/npm/bootstrap@5.3.0/dist/css/bootstrap.min.css" rel="stylesheet"><link rel="stylesheet" href="https://cdnjs.cloudflare.com/ajax/libs/font-awesome/6.4.0/css/all.min.css"></head><body style="background-color:#f1f5f9; padding:20px;">%s</body></html>`, html.EscapeString(empName), htmlContent)
		c.Type("html", "utf-8")
		return c.SendString(page)
	})

	// GET /api/reports/export_performance_excel
	app.Get("/api/reports/export_performance_excel", requireSession, func(c *fiber.Ctx) error {
		role := strings.TrimSpace(c.Query("role", "Report Maker"))
		month := strings.TrimSpace(c.Query("month", time.Now().Format("2006-01")))

		csvContent := fmt.Sprintf("Employee Performance Report - %s - %s\nRole,Month\n\"%s\",\"%s\"\n\n", role, month, role, month)
		c.Set("Content-Type", "text/csv; charset=utf-8")
		c.Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"performance_%s_%s.csv\"", strings.ReplaceAll(role, " ", "_"), month))
		return c.SendString(csvContent)
	})
}
