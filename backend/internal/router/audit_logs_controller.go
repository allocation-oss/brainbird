package router

import (
	"archive/zip"
	"bytes"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

//go:embed templates/settings/audit_logs.html
var auditLogsHTML []byte

func ensureAuditLogsTables(pool *sql.DB) {
	if pool == nil {
		return
	}
	_, _ = pool.Exec(`
		CREATE TABLE IF NOT EXISTS audit_logs (
			id INT AUTO_INCREMENT PRIMARY KEY,
			user_id INT DEFAULT NULL,
			user_name VARCHAR(100) DEFAULT NULL,
			user_role VARCHAR(50) DEFAULT NULL,
			module_name VARCHAR(50) NOT NULL,
			action_type VARCHAR(50) NOT NULL,
			action_performed VARCHAR(100) DEFAULT NULL,
			table_affected VARCHAR(100) DEFAULT NULL,
			record_id INT DEFAULT NULL,
			claim_number VARCHAR(100) DEFAULT NULL,
			description TEXT NOT NULL,
			details LONGTEXT DEFAULT NULL,
			old_values LONGTEXT DEFAULT NULL,
			new_values LONGTEXT DEFAULT NULL,
			ip_address VARCHAR(45) DEFAULT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			KEY idx_created_at (created_at),
			KEY idx_user_id (user_id),
			KEY idx_module_name (module_name),
			KEY idx_action_type (action_type)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;
	`)

	_, _ = pool.Exec(`
		CREATE TABLE IF NOT EXISTS audit_log_backups (
			id INT AUTO_INCREMENT PRIMARY KEY,
			file_name VARCHAR(255) NOT NULL,
			record_count INT NOT NULL DEFAULT 0,
			min_date DATETIME DEFAULT NULL,
			max_date DATETIME DEFAULT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;
	`)
}

type auditLogRow struct {
	ID               int    `json:"id"`
	UserID           int    `json:"user_id"`
	UserName         string `json:"user_name"`
	UserRole         string `json:"user_role"`
	ModuleName       string `json:"module_name"`
	ActionType       string `json:"action_type"`
	ActionPerformed  string `json:"action_performed"`
	TableAffected    string `json:"table_affected"`
	RecordID         int    `json:"record_id"`
	ClaimNumber      string `json:"claim_number"`
	Description      string `json:"description"`
	Details          string `json:"details"`
	OldValues        string `json:"old_values"`
	NewValues        string `json:"new_values"`
	IPAddress        string `json:"ip_address"`
	CreatedAt        string `json:"created_at"`
	ResolvedUserName string `json:"resolved_user_name"`
	ResolvedUserRole string `json:"resolved_user_role"`
	ActionLabel      string `json:"action_label"`
	ModuleLabel      string `json:"module_label"`
	Summary          string `json:"summary"`
}

func RegisterAuditLogsRoutes(app *fiber.App, pool *sql.DB, requireSession fiber.Handler) {
	ensureAuditLogsTables(pool)

	app.Get("/settings/audit_logs", requireSession, func(c *fiber.Ctx) error {
		exportExcel := c.QueryInt("export_excel", 0)

		if exportExcel == 1 {
			logs, err := queryAuditLogs(pool, c, false)
			if err != nil {
				return c.Status(500).SendString(err.Error())
			}

			var b strings.Builder
			b.WriteString(`<html xmlns:o="urn:schemas-microsoft-com:office:office" xmlns:x="urn:schemas-microsoft-com:office:excel" xmlns="http://www.w3.org/TR/REC-html40">`)
			b.WriteString(`<head><meta charset="utf-8"></head><body>`)
			b.WriteString(`<table border="1" cellpadding="5" cellspacing="0" style="border-collapse:collapse; font-family:Arial, sans-serif; font-size:12px;">`)
			b.WriteString(`<thead><tr style="background-color: #1e293b; color: #ffffff; font-weight: bold;">`)
			b.WriteString(`<th>Log ID</th><th>Date & Time</th><th>User Name</th><th>User Role</th><th>Module</th><th>Action Type</th><th>Claim Number</th><th>Description</th><th>IP Address</th>`)
			b.WriteString(`</tr></thead><tbody>`)

			for _, r := range logs {
				b.WriteString(`<tr>`)
				b.WriteString(fmt.Sprintf(`<td>#%d</td>`, r.ID))
				b.WriteString(fmt.Sprintf(`<td>%s</td>`, html.EscapeString(r.CreatedAt)))
				b.WriteString(fmt.Sprintf(`<td>%s</td>`, html.EscapeString(r.ResolvedUserName)))
				b.WriteString(fmt.Sprintf(`<td>%s</td>`, html.EscapeString(r.ResolvedUserRole)))
				b.WriteString(fmt.Sprintf(`<td>%s</td>`, html.EscapeString(r.ModuleLabel)))
				b.WriteString(fmt.Sprintf(`<td>%s</td>`, html.EscapeString(r.ActionLabel)))
				claim := r.ClaimNumber
				if claim == "" {
					claim = "N/A"
				}
				b.WriteString(fmt.Sprintf(`<td style="mso-number-format:'\@'">%s</td>`, html.EscapeString(claim)))
				b.WriteString(fmt.Sprintf(`<td>%s</td>`, html.EscapeString(r.Description)))
				b.WriteString(fmt.Sprintf(`<td>%s</td>`, html.EscapeString(r.IPAddress)))
				b.WriteString(`</tr>`)
			}

			b.WriteString(`</tbody></table></body></html>`)
			c.Type("application/vnd.ms-excel")
			c.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="Audit_Logs_%s.xls"`, time.Now().Format("20060102_150405")))
			return c.SendString(b.String())
		}

		htmlStr := string(auditLogsHTML)
		htmlStr = strings.ReplaceAll(htmlStr, "{{SIDEBAR_PARTIAL}}", string(dashboardSidebarPartial))
		htmlStr = strings.ReplaceAll(htmlStr, "{{HEADER_PARTIAL}}", string(dashboardHeaderPartial))
		htmlStr = strings.ReplaceAll(htmlStr, "{{FOOTER_PARTIAL}}", string(dashboardFooterPartial))
		c.Type("html", "utf-8")
		return c.SendString(htmlStr)
	})

	api := app.Group("/api", requireSession)

	api.Post("/audit_log", func(c *fiber.Ctx) error {
		session, ok := c.Locals(sessionContextKey).(dashboardSession)
		userId := 0
		userName := "System / Visitor"
		userRole := "Guest"

		if ok && session.UserID > 0 {
			userId = int(session.UserID)
			var fn, rn string
			_ = pool.QueryRow("SELECT u.fullname, COALESCE(r.role_name, 'User') FROM users u LEFT JOIN roles r ON u.role_id = r.id WHERE u.id = ? LIMIT 1", userId).Scan(&fn, &rn)
			if fn != "" {
				userName = fn
				userRole = rn
			}
		}

		moduleName := strings.TrimSpace(c.FormValue("module_name"))
		actionType := strings.TrimSpace(c.FormValue("action_type"))
		if actionType == "" {
			actionType = strings.TrimSpace(c.FormValue("action"))
		}
		description := strings.TrimSpace(c.FormValue("description"))
		claimInput := strings.TrimSpace(c.FormValue("claim_number"))
		caseIdInput, _ := strconv.Atoi(c.FormValue("case_id"))
		ipAddress := c.IP()

		var caseID int
		var foundClaim string
		if caseIdInput > 0 {
			_ = pool.QueryRow("SELECT id, COALESCE(claim_number, '') FROM cases WHERE id = ? LIMIT 1", caseIdInput).Scan(&caseID, &foundClaim)
		}
		if caseID == 0 && claimInput != "" {
			_ = pool.QueryRow("SELECT id, COALESCE(claim_number, '') FROM cases WHERE TRIM(claim_number) = ? ORDER BY id DESC LIMIT 1", claimInput).Scan(&caseID, &foundClaim)
		}

		referer := c.Get("Referer")
		targetUrl := referer
		if strings.Contains(description, "Visited URL:") {
			targetUrl = description
		}

		if moduleName == "" || moduleName == "General" {
			switch {
			case strings.Contains(targetUrl, "cases"):
				moduleName = "Cases"
			case strings.Contains(targetUrl, "dispatch"):
				moduleName = "Dispatch"
			case strings.Contains(targetUrl, "letters"):
				moduleName = "Letters"
			case strings.Contains(targetUrl, "users"), strings.Contains(targetUrl, "allocation"), strings.Contains(targetUrl, "staff_mapping"):
				moduleName = "Users"
			case strings.Contains(targetUrl, "settings"), strings.Contains(targetUrl, "masters"), strings.Contains(targetUrl, "menu"), strings.Contains(targetUrl, "permission"), strings.Contains(targetUrl, "audit"):
				moduleName = "Settings"
			case strings.Contains(targetUrl, "attendance"):
				moduleName = "Attendance"
			case strings.Contains(targetUrl, "dashboard"):
				moduleName = "Dashboard"
			case strings.Contains(targetUrl, "performance"):
				moduleName = "Performance"
			case strings.Contains(targetUrl, "tracking"):
				moduleName = "Location Tracking"
			default:
				moduleName = "Navigation"
			}
		}

		if actionType == "" || actionType == "LOG" {
			switch {
			case strings.Contains(targetUrl, "add"), strings.Contains(targetUrl, "create"), strings.Contains(targetUrl, "save"):
				actionType = "CREATE"
			case strings.Contains(targetUrl, "edit"), strings.Contains(targetUrl, "update"):
				actionType = "UPDATE"
			case strings.Contains(targetUrl, "delete"), strings.Contains(targetUrl, "remove"):
				actionType = "DELETE"
			case strings.Contains(targetUrl, "export"):
				actionType = "EXPORT"
			default:
				actionType = "PAGE_VIEW"
			}
		}

		if description == "" {
			description = "Visited: " + targetUrl
		}

		var actPerf, tblAff interface{} = nil, nil
		var recIDVal, claimVal interface{} = nil, nil

		if caseID > 0 {
			actPerf = "VIEWED CASE"
			tblAff = "cases"
			recIDVal = caseID
			claimVal = foundClaim
		}

		var uidVal interface{} = nil
		if userId > 0 {
			uidVal = userId
		}

		_, err := pool.Exec(`
			INSERT INTO audit_logs (user_id, user_name, user_role, module_name, action_type, action_performed, table_affected, record_id, claim_number, description, ip_address, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NOW())
		`, uidVal, userName, userRole, moduleName, actionType, actPerf, tblAff, recIDVal, claimVal, description, ipAddress)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"status": "error", "message": err.Error()})
		}

		return c.JSON(fiber.Map{"status": "success"})
	})

	api.All("/audit_logs/list", func(c *fiber.Ctx) error {
		logs, err := queryAuditLogs(pool, c, true)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"status": "error", "message": err.Error()})
		}

		totalCount, todayCount, err := countAuditLogs(pool, c)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"status": "error", "message": err.Error()})
		}

		var totalInDb int
		_ = pool.QueryRow("SELECT COUNT(*) FROM audit_logs").Scan(&totalInDb)

		limit := c.QueryInt("limit", 25)
		page := c.QueryInt("page", 1)
		totalPages := int(math.Ceil(float64(totalCount) / float64(limit)))
		if totalPages < 1 {
			totalPages = 1
		}

		return c.JSON(fiber.Map{
			"status": "success",
			"logs":   logs,
			"stats": fiber.Map{
				"total":    totalInDb,
				"filtered": totalCount,
				"today":    todayCount,
			},
			"pagination": fiber.Map{
				"current_page":  page,
				"total_pages":   totalPages,
				"total_records": totalCount,
				"limit":         limit,
			},
		})
	})

	api.All("/audit_logs/user_summary", func(c *fiber.Ctx) error {
		userIdStr := c.FormValue("user_id")
		if userIdStr == "" {
			userIdStr = c.Query("user_id")
		}
		userId, _ := strconv.Atoi(userIdStr)

		if userId <= 0 {
			return c.JSON(fiber.Map{"status": "error", "message": "Please select a valid user."})
		}

		var uFullName, uUsername, uRole string
		err := pool.QueryRow("SELECT u.fullname, u.username, COALESCE(r.role_name, 'User Role') FROM users u LEFT JOIN roles r ON u.role_id = r.id WHERE u.id = ? LIMIT 1", userId).Scan(&uFullName, &uUsername, &uRole)
		if err != nil {
			return c.JSON(fiber.Map{"status": "error", "message": "User record not found."})
		}

		actionCounts := make(map[string]int)
		totalUserActions := 0

		rows, err := pool.Query(`
			SELECT UPPER(COALESCE(NULLIF(action_type,''), 'LOG')), COUNT(*) 
			FROM audit_logs 
			WHERE user_id = ? OR user_name = ?
			GROUP BY 1
		`, userId, uFullName)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var aType string
				var cnt int
				if err := rows.Scan(&aType, &cnt); err == nil {
					actionCounts[aType] = cnt
					totalUserActions += cnt
				}
			}
		}

		userLogs := []fiber.Map{}
		lRows, err := pool.Query(`
			SELECT id, COALESCE(NULLIF(action_type,''), 'LOG'), COALESCE(NULLIF(module_name,''), 'General'), 
			       COALESCE(claim_number, ''), description, COALESCE(ip_address, ''), DATE_FORMAT(created_at, '%d-%b-%Y %h:%i %p')
			FROM audit_logs 
			WHERE user_id = ? OR user_name = ?
			ORDER BY id DESC LIMIT 50
		`, userId, uFullName)
		if err == nil {
			defer lRows.Close()
			for lRows.Next() {
				var id int
				var aType, mName, claim, desc, ip, createdFmt string
				if err := lRows.Scan(&id, &aType, &mName, &claim, &desc, &ip, &createdFmt); err == nil {
					userLogs = append(userLogs, fiber.Map{
						"id":             id,
						"action_label":   aType,
						"module_label":   mName,
						"claim_number":   claim,
						"description":    desc,
						"ip_address":     ip,
						"created_at_fmt": createdFmt,
					})
				}
			}
		}

		return c.JSON(fiber.Map{
			"status": "success",
			"user": fiber.Map{
				"id":            userId,
				"name":          uFullName,
				"username":      uUsername,
				"role":          uRole,
				"total_actions": totalUserActions,
			},
			"action_counts": actionCounts,
			"logs":          userLogs,
		})
	})

	api.Post("/audit_logs/clear", func(c *fiber.Ctx) error {
		days, _ := strconv.Atoi(c.FormValue("days", "90"))
		if days < 30 {
			days = 30
		}

		rows, err := pool.Query("SELECT id, COALESCE(user_id,0), COALESCE(user_name,''), COALESCE(module_name,''), COALESCE(action_type,''), COALESCE(description,''), created_at FROM audit_logs WHERE created_at < DATE_SUB(NOW(), INTERVAL ? DAY)", days)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"status": "error", "message": err.Error()})
		}
		defer rows.Close()

		var records []map[string]interface{}
		var minDate, maxDate string

		for rows.Next() {
			var id, uid int
			var uname, mname, atype, desc, created string
			if err := rows.Scan(&id, &uid, &uname, &mname, &atype, &desc, &created); err == nil {
				records = append(records, map[string]interface{}{
					"id": id, "user_id": uid, "user_name": uname, "module_name": mname, "action_type": atype, "description": desc, "created_at": created,
				})
				if minDate == "" || created < minDate {
					minDate = created
				}
				if maxDate == "" || created > maxDate {
					maxDate = created
				}
			}
		}

		if len(records) == 0 {
			return c.JSON(fiber.Map{"status": "error", "message": fmt.Sprintf("No logs found older than %d days to backup.", days)})
		}

		backupDir := filepath.Join("tmp", "audit_backups")
		_ = os.MkdirAll(backupDir, 0755)

		timestamp := time.Now().Format("060102150405")
		zipFileName := fmt.Sprintf("audit_backup_%s.zip", timestamp)
		jsonFileName := fmt.Sprintf("audit_backup_%s.json", timestamp)
		zipFilePath := filepath.Join(backupDir, zipFileName)

		buf := new(bytes.Buffer)
		w := zip.NewWriter(buf)
		f, err := w.Create(jsonFileName)
		if err == nil {
			data, _ := json.MarshalIndent(records, "", "  ")
			_, _ = f.Write(data)
			_ = w.Close()
			_ = os.WriteFile(zipFilePath, buf.Bytes(), 0644)
		}

		_, _ = pool.Exec("INSERT INTO audit_log_backups (file_name, record_count, min_date, max_date, created_at) VALUES (?, ?, ?, ?, NOW())", zipFileName, len(records), minDate, maxDate)
		res, _ := pool.Exec("DELETE FROM audit_logs WHERE created_at < DATE_SUB(NOW(), INTERVAL ? DAY)", days)
		deleted, _ := res.RowsAffected()

		return c.JSON(fiber.Map{
			"status":  "success",
			"message": fmt.Sprintf("%d logs backed up to ZIP and purged from active database successfully!", deleted),
		})
	})

	api.Get("/audit_logs/backups", func(c *fiber.Ctx) error {
		type backupItem struct {
			ID          int    `json:"id"`
			FileName    string `json:"file_name"`
			RecordCount int    `json:"record_count"`
			MinDate     string `json:"min_date"`
			MaxDate     string `json:"max_date"`
			CreatedAt   string `json:"created_at"`
		}

		backups := []backupItem{}
		rows, err := pool.Query("SELECT id, file_name, record_count, COALESCE(DATE_FORMAT(min_date, '%d-%b-%y'), ''), COALESCE(DATE_FORMAT(max_date, '%d-%b-%y'), ''), DATE_FORMAT(created_at, '%d-%b-%Y %h:%i %p') FROM audit_log_backups ORDER BY id DESC")
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var b backupItem
				if err := rows.Scan(&b.ID, &b.FileName, &b.RecordCount, &b.MinDate, &b.MaxDate, &b.CreatedAt); err == nil {
					backups = append(backups, b)
				}
			}
		}

		return c.JSON(fiber.Map{"status": "success", "data": backups})
	})

	api.Post("/audit_logs/restore", func(c *fiber.Ctx) error {
		id, _ := strconv.Atoi(c.FormValue("id"))
		if id <= 0 {
			return c.JSON(fiber.Map{"status": "error", "message": "Backup record not found."})
		}

		var fileName string
		err := pool.QueryRow("SELECT file_name FROM audit_log_backups WHERE id = ?", id).Scan(&fileName)
		if err != nil {
			return c.JSON(fiber.Map{"status": "error", "message": "Backup record not found in database."})
		}

		zipPath := filepath.Join("tmp", "audit_backups", fileName)
		r, err := zip.OpenReader(zipPath)
		if err != nil {
			return c.JSON(fiber.Map{"status": "error", "message": "Backup ZIP file missing or unreadable on server."})
		}
		defer r.Close()

		var jsonFile *zip.File
		for _, f := range r.File {
			if strings.HasSuffix(f.Name, ".json") {
				jsonFile = f
				break
			}
		}

		if jsonFile == nil {
			return c.JSON(fiber.Map{"status": "error", "message": "No JSON payload found inside backup ZIP."})
		}

		rc, err := jsonFile.Open()
		if err != nil {
			return c.JSON(fiber.Map{"status": "error", "message": err.Error()})
		}
		defer rc.Close()

		content, _ := io.ReadAll(rc)
		var records []map[string]interface{}
		if err := json.Unmarshal(content, &records); err != nil {
			return c.JSON(fiber.Map{"status": "error", "message": "Failed to parse JSON backup content."})
		}

		restoredCount := 0
		for _, rec := range records {
			_, err := pool.Exec(`
				INSERT IGNORE INTO audit_logs (id, user_id, user_name, module_name, action_type, description, created_at)
				VALUES (?, ?, ?, ?, ?, ?, ?)
			`, rec["id"], rec["user_id"], rec["user_name"], rec["module_name"], rec["action_type"], rec["description"], rec["created_at"])
			if err == nil {
				restoredCount++
			}
		}

		return c.JSON(fiber.Map{
			"status":  "success",
			"message": fmt.Sprintf("%d records restored successfully back to active logs!", restoredCount),
		})
	})

	api.Post("/audit_logs/delete_backup", func(c *fiber.Ctx) error {
		id, _ := strconv.Atoi(c.FormValue("id"))
		if id <= 0 {
			return c.JSON(fiber.Map{"status": "error", "message": "Invalid backup ID."})
		}

		var fileName string
		_ = pool.QueryRow("SELECT file_name FROM audit_log_backups WHERE id = ?", id).Scan(&fileName)
		if fileName != "" {
			_ = os.Remove(filepath.Join("tmp", "audit_backups", fileName))
		}
		_, _ = pool.Exec("DELETE FROM audit_log_backups WHERE id = ?", id)

		return c.JSON(fiber.Map{"status": "success", "message": "Backup permanently deleted."})
	})
}

func queryAuditLogs(pool *sql.DB, c *fiber.Ctx, paged bool) ([]auditLogRow, error) {
	where := []string{"1=1"}
	var args []interface{}

	userFilter := c.QueryInt("user_id", 0)
	if userFilter > 0 {
		where = append(where, "(a.user_id = ? OR (a.user_id IS NULL AND u.id = ?))")
		args = append(args, userFilter, userFilter)
	}

	moduleFilter := strings.TrimSpace(c.Query("module_name"))
	if moduleFilter != "" {
		where = append(where, "COALESCE(NULLIF(a.module_name,''), 'Navigation') = ?")
		args = append(args, moduleFilter)
	}

	actionFilter := strings.TrimSpace(c.Query("action_type"))
	if actionFilter != "" {
		where = append(where, "COALESCE(NULLIF(a.action_type,''), 'LOG') = ?")
		args = append(args, actionFilter)
	}

	search := strings.TrimSpace(c.Query("search"))
	if search != "" {
		where = append(where, "(a.claim_number LIKE ? OR a.description LIKE ? OR u.fullname LIKE ? OR a.user_name LIKE ?)")
		s := "%" + search + "%"
		args = append(args, s, s, s, s)
	}

	month := strings.TrimSpace(c.Query("month"))
	if month != "" {
		where = append(where, "DATE_FORMAT(a.created_at, '%Y-%m') = ?")
		args = append(args, month)
	}

	exactDate := strings.TrimSpace(c.Query("exact_date"))
	if exactDate != "" {
		where = append(where, "DATE(a.created_at) = ?")
		args = append(args, exactDate)
	}

	query := fmt.Sprintf(`
		SELECT a.id, COALESCE(a.user_id, 0), COALESCE(a.user_name, ''), COALESCE(a.user_role, ''),
		       COALESCE(a.module_name, 'General'), COALESCE(a.action_type, 'LOG'),
		       COALESCE(a.claim_number, ''), COALESCE(a.description, ''), COALESCE(a.ip_address, ''),
		       DATE_FORMAT(a.created_at, '%%d-%%b-%%Y %%h:%%i %%p'),
		       COALESCE(NULLIF(u.fullname, ''), NULLIF(a.user_name, ''), 'System'),
		       COALESCE(NULLIF(r.role_name, ''), NULLIF(a.user_role, ''), 'System'),
		       COALESCE(NULLIF(a.action_type,''), 'LOG'),
		       COALESCE(NULLIF(a.module_name,''), 'General')
		FROM audit_logs a
		LEFT JOIN users u ON a.user_id = u.id
		LEFT JOIN roles r ON u.role_id = r.id
		WHERE %s
		ORDER BY a.id DESC
	`, strings.Join(where, " AND "))

	if paged {
		limit := c.QueryInt("limit", 25)
		page := c.QueryInt("page", 1)
		if limit < 1 {
			limit = 25
		}
		if page < 1 {
			page = 1
		}
		offset := (page - 1) * limit
		query += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)
	}

	rows, err := pool.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []auditLogRow
	for rows.Next() {
		var r auditLogRow
		err := rows.Scan(
			&r.ID, &r.UserID, &r.UserName, &r.UserRole,
			&r.ModuleName, &r.ActionType,
			&r.ClaimNumber, &r.Description, &r.IPAddress,
			&r.CreatedAt,
			&r.ResolvedUserName, &r.ResolvedUserRole,
			&r.ActionLabel, &r.ModuleLabel,
		)
		if err == nil {
			list = append(list, r)
		}
	}

	return list, nil
}

func countAuditLogs(pool *sql.DB, c *fiber.Ctx) (int, int, error) {
	where := []string{"1=1"}
	var args []interface{}

	userFilter := c.QueryInt("user_id", 0)
	if userFilter > 0 {
		where = append(where, "(a.user_id = ? OR (a.user_id IS NULL AND u.id = ?))")
		args = append(args, userFilter, userFilter)
	}

	moduleFilter := strings.TrimSpace(c.Query("module_name"))
	if moduleFilter != "" {
		where = append(where, "COALESCE(NULLIF(a.module_name,''), 'Navigation') = ?")
		args = append(args, moduleFilter)
	}

	actionFilter := strings.TrimSpace(c.Query("action_type"))
	if actionFilter != "" {
		where = append(where, "COALESCE(NULLIF(a.action_type,''), 'LOG') = ?")
		args = append(args, actionFilter)
	}

	search := strings.TrimSpace(c.Query("search"))
	if search != "" {
		where = append(where, "(a.claim_number LIKE ? OR a.description LIKE ? OR u.fullname LIKE ? OR a.user_name LIKE ?)")
		s := "%" + search + "%"
		args = append(args, s, s, s, s)
	}

	month := strings.TrimSpace(c.Query("month"))
	if month != "" {
		where = append(where, "DATE_FORMAT(a.created_at, '%Y-%m') = ?")
		args = append(args, month)
	}

	exactDate := strings.TrimSpace(c.Query("exact_date"))
	if exactDate != "" {
		where = append(where, "DATE(a.created_at) = ?")
		args = append(args, exactDate)
	}

	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM audit_logs a LEFT JOIN users u ON a.user_id = u.id WHERE %s", strings.Join(where, " AND "))
	var totalFiltered int
	_ = pool.QueryRow(countQuery, args...).Scan(&totalFiltered)

	todayQuery := fmt.Sprintf("SELECT COUNT(*) FROM audit_logs a LEFT JOIN users u ON a.user_id = u.id WHERE %s AND DATE(a.created_at) = CURRENT_DATE()", strings.Join(where, " AND "))
	var todayCount int
	_ = pool.QueryRow(todayQuery, args...).Scan(&todayCount)

	return totalFiltered, todayCount, nil
}
