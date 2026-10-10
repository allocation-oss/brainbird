package router

import (
	"database/sql"
	_ "embed"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

//go:embed templates/attendance/manual_matrix.html
var attendanceManualMatrixHTML []byte

func ensureAttendanceTables(pool *sql.DB) {
	if pool == nil {
		return
	}
	_, _ = pool.Exec(`
		CREATE TABLE IF NOT EXISTS attendance_staff (
			id INT PRIMARY KEY,
			fullname VARCHAR(255) NOT NULL,
			role_id INT DEFAULT 0,
			employment_type VARCHAR(50) DEFAULT 'salary',
			working_location VARCHAR(255) DEFAULT '',
			mobile_no VARCHAR(50) DEFAULT '',
			email_id VARCHAR(255) DEFAULT '',
			status ENUM('active','inactive') DEFAULT 'active',
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
	`)

	_, _ = pool.Exec(`
		CREATE TABLE IF NOT EXISTS attendance_settings (
			id INT AUTO_INCREMENT PRIMARY KEY,
			reporting_time VARCHAR(20) DEFAULT '09:30:00',
			grace_period_mins INT DEFAULT 15,
			late_threshold INT DEFAULT 3,
			penalty_type VARCHAR(50) DEFAULT 'Half-Day',
			week_offs TEXT,
			holidays TEXT,
			leave_quotas TEXT,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
	`)

	_, _ = pool.Exec(`
		CREATE TABLE IF NOT EXISTS attendance_manual_records (
			id INT AUTO_INCREMENT PRIMARY KEY,
			user_id INT NOT NULL,
			record_month VARCHAR(10) NOT NULL,
			source_tab VARCHAR(50) DEFAULT 'work',
			daily_data JSON,
			extra_data JSON,
			updated_by INT DEFAULT 0,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			UNIQUE KEY uq_user_month (user_id, record_month)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
	`)
}

func RegisterAttendanceRoutes(app *fiber.App, pool *sql.DB, requireSession fiber.Handler) {
	ensureAttendanceTables(pool)

	app.Get("/attendance/manual_matrix", requireSession, func(c *fiber.Ctx) error {
		html := string(attendanceManualMatrixHTML)
		html = strings.ReplaceAll(html, "{{SIDEBAR_PARTIAL}}", string(dashboardSidebarPartial))
		html = strings.ReplaceAll(html, "{{HEADER_PARTIAL}}", string(dashboardHeaderPartial))
		html = strings.ReplaceAll(html, "{{FOOTER_PARTIAL}}", string(dashboardFooterPartial))
		c.Type("html", "utf-8")
		return c.SendString(html)
	})

	api := app.Group("/api/attendance", requireSession)

	api.All("/data", func(c *fiber.Ctx) error {
		month := strings.TrimSpace(c.FormValue("month"))
		if month == "" {
			month = strings.TrimSpace(c.Query("month"))
		}
		if month == "" {
			month = time.Now().Format("2006-01")
		}

		if pool == nil {
			return c.JSON(fiber.Map{
				"status":        "success",
				"days_in_month": 31,
				"records":       []any{},
				"settings":      fiber.Map{},
			})
		}

		type settingsObj struct {
			ReportingTime   string `json:"reporting_time"`
			GracePeriodMins int    `json:"grace_period_mins"`
			LateThreshold   int    `json:"late_threshold"`
			PenaltyType     string `json:"penalty_type"`
			WeekOffs        string `json:"week_offs"`
			Holidays        string `json:"holidays"`
			LeaveQuotas     string `json:"leave_quotas"`
		}

		var set settingsObj
		row := pool.QueryRow("SELECT COALESCE(reporting_time, '09:30:00'), COALESCE(grace_period_mins, 15), COALESCE(late_threshold, 3), COALESCE(penalty_type, 'Half-Day'), COALESCE(week_offs, '[\"Sunday\"]'), COALESCE(holidays, '{}'), COALESCE(leave_quotas, '{\"CL\":12,\"ML\":12,\"EL\":15}') FROM attendance_settings LIMIT 1")
		if err := row.Scan(&set.ReportingTime, &set.GracePeriodMins, &set.LateThreshold, &set.PenaltyType, &set.WeekOffs, &set.Holidays, &set.LeaveQuotas); err != nil {
			set = settingsObj{
				ReportingTime:   "09:30:00",
				GracePeriodMins: 15,
				LateThreshold:   3,
				PenaltyType:     "Half-Day",
				WeekOffs:        `["Sunday"]`,
				Holidays:        `{}`,
				LeaveQuotas:     `{"CL":12,"ML":12,"EL":15}`,
			}
		}

		type staffItem struct {
			ID              int    `json:"id"`
			FullName        string `json:"fullname"`
			RoleID          int    `json:"role_id"`
			WorkingLocation string `json:"working_location"`
			Status          string `json:"status"`
		}

		allStaff := []staffItem{}
		staffNamesMap := make(map[string]bool)
		staffIDsList := []int{}

		sRows, err := pool.Query("SELECT id, fullname, role_id, COALESCE(working_location, ''), status FROM attendance_staff ORDER BY fullname ASC")
		if err == nil {
			defer sRows.Close()
			for sRows.Next() {
				var s staffItem
				if err := sRows.Scan(&s.ID, &s.FullName, &s.RoleID, &s.WorkingLocation, &s.Status); err == nil {
					allStaff = append(allStaff, s)
					staffNamesMap[strings.ToUpper(strings.TrimSpace(s.FullName))] = true
					staffIDsList = append(staffIDsList, s.ID)
				}
			}
		}

		mappedRecords := make(map[int]map[string]interface{})
		mappedRemarks := make(map[int]map[string]interface{})

		rRows, err := pool.Query("SELECT user_id, COALESCE(daily_data, '{}'), COALESCE(extra_data, '{}') FROM attendance_manual_records WHERE record_month = ?", month)
		if err == nil {
			defer rRows.Close()
			for rRows.Next() {
				var uid int
				var dJSON, eJSON string
				if err := rRows.Scan(&uid, &dJSON, &eJSON); err == nil {
					var dMap, eMap map[string]interface{}
					_ = json.Unmarshal([]byte(dJSON), &dMap)
					_ = json.Unmarshal([]byte(eJSON), &eMap)
					if dMap == nil {
						dMap = make(map[string]interface{})
					}
					if eMap == nil {
						eMap = make(map[string]interface{})
					}
					mappedRecords[uid] = dMap
					mappedRemarks[uid] = eMap
				}
			}
		}

		activeStaffList := []staffItem{}
		for _, s := range allStaff {
			if s.Status == "active" {
				activeStaffList = append(activeStaffList, s)
			} else {
				userRecs := mappedRecords[s.ID]
				hasMarks := false
				for _, v := range userRecs {
					vStr, ok := v.(string)
					if ok && vStr != "" && vStr != "WO" && vStr != "HO" {
						hasMarks = true
						break
					}
				}
				if hasMarks {
					activeStaffList = append(activeStaffList, s)
				}
			}
		}

		type availUser struct {
			ID             int    `json:"id"`
			FullName       string `json:"fullname"`
			RoleID         int    `json:"role_id"`
			EmploymentType string `json:"employment_type"`
		}
		availableUsers := []availUser{}

		uQuery := "SELECT id, fullname, role_id, COALESCE(employment_type, 'salary') FROM users WHERE status = 'active' AND role_id != 4"
		if len(staffIDsList) > 0 {
			placeholders := make([]string, len(staffIDsList))
			for i, sid := range staffIDsList {
				placeholders[i] = strconv.Itoa(sid)
			}
			uQuery += fmt.Sprintf(" AND id NOT IN (%s)", strings.Join(placeholders, ","))
		}
		uQuery += " ORDER BY fullname ASC"

		if uRows, err := pool.Query(uQuery); err == nil {
			defer uRows.Close()
			for uRows.Next() {
				var u availUser
				if err := uRows.Scan(&u.ID, &u.FullName, &u.RoleID, &u.EmploymentType); err == nil {
					norm := strings.ToUpper(strings.TrimSpace(u.FullName))
					if !staffNamesMap[norm] {
						availableUsers = append(availableUsers, u)
					}
				}
			}
		}

		return c.JSON(fiber.Map{
			"status":             "success",
			"settings":           set,
			"staff":              activeStaffList,
			"allStaffStatusList": allStaff,
			"records":            mappedRecords,
			"remarks":            mappedRemarks,
			"availableUsers":     availableUsers,
		})
	})

	api.Post("/mark", func(c *fiber.Ctx) error {
		staffID, _ := strconv.Atoi(c.FormValue("staff_id"))
		month := strings.TrimSpace(c.FormValue("month"))
		day := strings.TrimSpace(c.FormValue("day"))
		mark := strings.ToUpper(strings.TrimSpace(c.FormValue("mark")))
		remark := strings.TrimSpace(c.FormValue("remark"))

		if staffID <= 0 || month == "" || day == "" {
			return c.JSON(fiber.Map{"status": "error", "message": "Invalid parameters"})
		}

		var recordID int
		var dJSON, eJSON string
		err := pool.QueryRow("SELECT id, COALESCE(daily_data, '{}'), COALESCE(extra_data, '{}') FROM attendance_manual_records WHERE user_id = ? AND record_month = ?", staffID, month).Scan(&recordID, &dJSON, &eJSON)

		dailyData := make(map[string]interface{})
		extraData := make(map[string]interface{})

		if err == nil {
			_ = json.Unmarshal([]byte(dJSON), &dailyData)
			_ = json.Unmarshal([]byte(eJSON), &extraData)

			if mark == "" {
				delete(dailyData, day)
				delete(extraData, day)
			} else {
				dailyData[day] = mark
				if remark != "" {
					extraData[day] = remark
				}
			}

			newD, _ := json.Marshal(dailyData)
			newE, _ := json.Marshal(extraData)
			_, _ = pool.Exec("UPDATE attendance_manual_records SET daily_data = ?, extra_data = ?, updated_at = NOW() WHERE id = ?", string(newD), string(newE), recordID)
		} else {
			if mark != "" {
				dailyData[day] = mark
				if remark != "" {
					extraData[day] = remark
				}
				newD, _ := json.Marshal(dailyData)
				newE, _ := json.Marshal(extraData)
				_, _ = pool.Exec("INSERT INTO attendance_manual_records (user_id, record_month, source_tab, daily_data, extra_data) VALUES (?, ?, 'work', ?, ?)", staffID, month, string(newD), string(newE))
			}
		}

		return c.JSON(fiber.Map{"status": "success"})
	})

	api.Post("/manual-entry", func(c *fiber.Ctx) error {
		staffID, _ := strconv.Atoi(c.FormValue("staff_id"))
		leaveType := strings.TrimSpace(c.FormValue("leave_type"))
		startStr := strings.TrimSpace(c.FormValue("start_date"))
		endStr := strings.TrimSpace(c.FormValue("end_date"))
		remark := strings.TrimSpace(c.FormValue("reason"))

		if staffID <= 0 || leaveType == "" || startStr == "" || endStr == "" {
			return c.JSON(fiber.Map{"status": "error", "message": "All fields are required."})
		}

		mark := "P"
		switch leaveType {
		case "CL", "ML", "EL", "A", "H", "L":
			mark = leaveType
		case "Late Waiver", "Absent Waiver":
			mark = "P"
		}

		formattedRemark := leaveType
		if remark != "" {
			formattedRemark += fmt.Sprintf(" (%s)", remark)
		}

		startDate, err1 := time.Parse("2006-01-02", startStr)
		endDate, err2 := time.Parse("2006-01-02", endStr)
		if err1 != nil || err2 != nil {
			return c.JSON(fiber.Map{"status": "error", "message": "Invalid dates."})
		}

		for cur := startDate; !cur.After(endDate); cur = cur.AddDate(0, 0, 1) {
			mStr := cur.Format("2006-01")
			dStr := strconv.Itoa(cur.Day())

			var recordID int
			var dJSON, eJSON string
			err := pool.QueryRow("SELECT id, COALESCE(daily_data, '{}'), COALESCE(extra_data, '{}') FROM attendance_manual_records WHERE user_id = ? AND record_month = ?", staffID, mStr).Scan(&recordID, &dJSON, &eJSON)

			dailyData := make(map[string]interface{})
			extraData := make(map[string]interface{})

			if err == nil {
				_ = json.Unmarshal([]byte(dJSON), &dailyData)
				_ = json.Unmarshal([]byte(eJSON), &extraData)

				dailyData[dStr] = mark
				extraData[dStr] = formattedRemark

				newD, _ := json.Marshal(dailyData)
				newE, _ := json.Marshal(extraData)
				_, _ = pool.Exec("UPDATE attendance_manual_records SET daily_data = ?, extra_data = ?, updated_at = NOW() WHERE id = ?", string(newD), string(newE), recordID)
			} else {
				dailyData[dStr] = mark
				extraData[dStr] = formattedRemark

				newD, _ := json.Marshal(dailyData)
				newE, _ := json.Marshal(extraData)
				_, _ = pool.Exec("INSERT INTO attendance_manual_records (user_id, record_month, source_tab, daily_data, extra_data) VALUES (?, ?, 'work', ?, ?)", staffID, mStr, string(newD), string(newE))
			}
		}

		return c.JSON(fiber.Map{"status": "success", "message": "Matrix & Remark updated successfully!"})
	})

	api.Post("/toggle-staff-status", func(c *fiber.Ctx) error {
		staffID, _ := strconv.Atoi(c.FormValue("staff_id"))
		status := strings.TrimSpace(c.FormValue("status"))

		if staffID <= 0 || (status != "active" && status != "inactive") {
			return c.JSON(fiber.Map{"status": "error", "message": "Invalid parameters"})
		}

		_, err := pool.Exec("UPDATE attendance_staff SET status = ? WHERE id = ?", status, staffID)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"status": "error", "message": err.Error()})
		}

		return c.JSON(fiber.Map{"status": "success", "message": fmt.Sprintf("Staff status updated to %s", strings.Title(status))})
	})

	api.Post("/edit-staff-name", func(c *fiber.Ctx) error {
		staffID, _ := strconv.Atoi(c.FormValue("staff_id"))
		newName := strings.TrimSpace(c.FormValue("new_name"))

		if staffID <= 0 || newName == "" {
			return c.JSON(fiber.Map{"status": "error", "message": "Invalid parameters"})
		}

		_, _ = pool.Exec("UPDATE attendance_staff SET fullname = ? WHERE id = ?", newName, staffID)
		_, _ = pool.Exec("UPDATE users SET fullname = ? WHERE id = ?", newName, staffID)

		return c.JSON(fiber.Map{"status": "success", "message": "Staff name updated successfully."})
	})

	api.Post("/add-staff", func(c *fiber.Ctx) error {
		userIDsRaw := c.FormValue("user_ids")
		var userIDs []int
		for _, part := range strings.Split(userIDsRaw, ",") {
			part = strings.TrimSpace(part)
			if id, err := strconv.Atoi(part); err == nil && id > 0 {
				userIDs = append(userIDs, id)
			}
		}

		if len(userIDs) == 0 {
			return c.JSON(fiber.Map{"status": "error", "message": "No users selected."})
		}

		for _, uid := range userIDs {
			var uName, empType, mobile, email string
			var roleID int
			err := pool.QueryRow("SELECT fullname, role_id, COALESCE(employment_type, 'salary'), COALESCE(personal_mobile, ''), COALESCE(email, '') FROM users WHERE id = ?", uid).Scan(&uName, &roleID, &empType, &mobile, &email)
			if err == nil {
				_, _ = pool.Exec(`
					INSERT INTO attendance_staff (id, fullname, role_id, employment_type, mobile_no, email_id, status)
					VALUES (?, ?, ?, ?, ?, ?, 'active')
					ON DUPLICATE KEY UPDATE status = 'active', fullname = VALUES(fullname)
				`, uid, uName, roleID, empType, mobile, email)
			}
		}

		return c.JSON(fiber.Map{"status": "success"})
	})

	api.Post("/save-settings", func(c *fiber.Ctx) error {
		reportingTime := c.FormValue("reporting_time", "09:30:00")
		grace, _ := strconv.Atoi(c.FormValue("grace_period_mins", "15"))
		threshold, _ := strconv.Atoi(c.FormValue("late_threshold", "3"))
		penaltyType := c.FormValue("penalty_type", "Half-Day")

		weekOffsRaw := c.FormValue("week_offs")
		if weekOffsRaw == "" {
			weekOffsRaw = `["Sunday"]`
		}

		holidaysJSON := c.FormValue("holidays_json", "{}")
		qCL, _ := strconv.Atoi(c.FormValue("q_cl", "12"))
		qML, _ := strconv.Atoi(c.FormValue("q_ml", "12"))
		qEL, _ := strconv.Atoi(c.FormValue("q_el", "15"))

		leaveQuotas, _ := json.Marshal(map[string]int{"CL": qCL, "ML": qML, "EL": qEL})

		var existsID int
		_ = pool.QueryRow("SELECT id FROM attendance_settings LIMIT 1").Scan(&existsID)

		if existsID > 0 {
			_, err := pool.Exec(`
				UPDATE attendance_settings 
				SET reporting_time = ?, grace_period_mins = ?, late_threshold = ?, penalty_type = ?, week_offs = ?, holidays = ?, leave_quotas = ?
				WHERE id = ?`, reportingTime, grace, threshold, penaltyType, weekOffsRaw, holidaysJSON, string(leaveQuotas), existsID)
			if err != nil {
				return c.Status(500).JSON(fiber.Map{"status": "error", "message": err.Error()})
			}
		} else {
			_, err := pool.Exec(`
				INSERT INTO attendance_settings (reporting_time, grace_period_mins, late_threshold, penalty_type, week_offs, holidays, leave_quotas)
				VALUES (?, ?, ?, ?, ?, ?, ?)`, reportingTime, grace, threshold, penaltyType, weekOffsRaw, holidaysJSON, string(leaveQuotas))
			if err != nil {
				return c.Status(500).JSON(fiber.Map{"status": "error", "message": err.Error()})
			}
		}

		return c.JSON(fiber.Map{"status": "success", "message": "Configuration Saved."})
	})

	app.Get("/attendance/export", requireSession, func(c *fiber.Ctx) error {
		month := strings.TrimSpace(c.Query("month"))
		if month == "" {
			month = time.Now().Format("2006-01")
		}

		type staffRecord struct {
			ID       int
			FullName string
		}
		var staffList []staffRecord
		sRows, err := pool.Query("SELECT id, fullname FROM attendance_staff WHERE status = 'active' ORDER BY fullname ASC")
		if err == nil {
			defer sRows.Close()
			for sRows.Next() {
				var s staffRecord
				if err := sRows.Scan(&s.ID, &s.FullName); err == nil {
					staffList = append(staffList, s)
				}
			}
		}

		mappedRecords := make(map[int]map[string]string)
		rRows, err := pool.Query("SELECT user_id, COALESCE(daily_data, '{}') FROM attendance_manual_records WHERE record_month = ?", month)
		if err == nil {
			defer rRows.Close()
			for rRows.Next() {
				var uid int
				var dJSON string
				if err := rRows.Scan(&uid, &dJSON); err == nil {
					var rawMap map[string]interface{}
					_ = json.Unmarshal([]byte(dJSON), &rawMap)
					dMap := make(map[string]string)
					for k, v := range rawMap {
						dMap[k] = fmt.Sprintf("%v", v)
					}
					mappedRecords[uid] = dMap
				}
			}
		}

		threshold := 3
		penaltyType := "Half-Day"
		var tRow = pool.QueryRow("SELECT COALESCE(late_threshold, 3), COALESCE(penalty_type, 'Half-Day') FROM attendance_settings LIMIT 1")
		_ = tRow.Scan(&threshold, &penaltyType)

		penMult := 0.5
		if penaltyType != "Half-Day" {
			penMult = 1.0
		}

		t, err := time.Parse("2006-01", month)
		if err != nil {
			t = time.Now()
		}
		nextMonth := t.AddDate(0, 1, 0)
		daysInMonth := nextMonth.AddDate(0, 0, -1).Day()

		c.Type("text/csv")
		c.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="Attendance_Export_%s.csv"`, month))

		var b strings.Builder
		w := csv.NewWriter(&b)

		header := []string{"Emp ID", "Staff Name"}
		for i := 1; i <= daysInMonth; i++ {
			header = append(header, fmt.Sprintf("%d-%s", i, t.Format("Jan")))
		}
		header = append(header, "Total Present", "Total Absent", "Total Leaves", "Late Count", "Penalty Deducted")
		_ = w.Write(header)

		for _, staff := range staffList {
			dData := mappedRecords[staff.ID]
			row := []string{strconv.Itoa(staff.ID), staff.FullName}

			var present, absent, half, leave, lateCount float64
			for i := 1; i <= daysInMonth; i++ {
				mark := dData[strconv.Itoa(i)]
				if mark == "" {
					mark = "-"
				}

				if strings.HasPrefix(mark, "L") && mark != "CL" && mark != "ML" && mark != "EL" {
					lateCount++
					present++
				} else if mark == "P" {
					present++
				} else if mark == "H" {
					half++
				} else if mark == "A" {
					absent++
				} else if mark == "CL" || mark == "ML" || mark == "EL" {
					leave++
				}
				row = append(row, mark)
			}

			penaltyCount := math.Floor(lateCount / float64(threshold))
			deduction := penaltyCount * penMult
			finalPresent := present + (half * 0.5) - deduction
			finalAbsent := absent + deduction

			row = append(row,
				fmt.Sprintf("%.1f", math.Max(0, finalPresent)),
				fmt.Sprintf("%.1f", math.Max(0, finalAbsent)),
				fmt.Sprintf("%.0f", leave),
				fmt.Sprintf("%.0f", lateCount),
				fmt.Sprintf("%.1f", deduction),
			)
			_ = w.Write(row)
		}

		w.Flush()
		return c.SendString(b.String())
	})
}
