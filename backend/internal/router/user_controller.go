package router

import (
	"database/sql"
	_ "embed"
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"golang.org/x/crypto/bcrypt"
)

//go:embed templates/users/manage.html
var usersManageHTML []byte

func RegisterUserRoutes(app *fiber.App, pool *sql.DB, requireSession fiber.Handler) {
	app.Get("/users/manage", requireSession, func(c *fiber.Ctx) error {
		html := string(usersManageHTML)
		html = strings.ReplaceAll(html, "{{SIDEBAR_PARTIAL}}", string(dashboardSidebarPartial))
		html = strings.ReplaceAll(html, "{{HEADER_PARTIAL}}", string(dashboardHeaderPartial))
		html = strings.ReplaceAll(html, "{{FOOTER_PARTIAL}}", string(dashboardFooterPartial))
		c.Type("html", "utf-8")
		return c.SendString(html)
	})

	api := app.Group("/api/users", requireSession)

	api.All("/get_initial_data", func(c *fiber.Ctx) error {
		session := c.Locals(sessionContextKey).(dashboardSession)
		roleQuery := "SELECT id, role_name FROM roles WHERE status = 'active'"
		if session.RoleID != 1 {
			roleQuery += " AND id NOT IN (1, 2)"
		}
		roleQuery += " ORDER BY role_name ASC"

		type roleItem struct {
			ID   int    `json:"id"`
			Name string `json:"role_name"`
		}
		roles := []roleItem{}
		if rows, err := pool.Query(roleQuery); err == nil {
			defer rows.Close()
			for rows.Next() {
				var r roleItem
				if err := rows.Scan(&r.ID, &r.Name); err == nil {
					roles = append(roles, r)
				}
			}
		}

		type stateItem struct {
			ID   int    `json:"id"`
			Name string `json:"state_name"`
		}
		states := []stateItem{}
		if rows, err := pool.Query("SELECT id, state_name FROM master_states ORDER BY state_name ASC"); err == nil {
			defer rows.Close()
			for rows.Next() {
				var s stateItem
				if err := rows.Scan(&s.ID, &s.Name); err == nil {
					states = append(states, s)
				}
			}
		}

		type rmItem struct {
			ID       int    `json:"id"`
			FullName string `json:"fullname"`
			RoleName string `json:"role_name"`
		}
		rms := []rmItem{}
		if rows, err := pool.Query(`SELECT u.id, u.fullname, COALESCE(r.role_name, 'Staff')
			FROM users u LEFT JOIN roles r ON u.role_id = r.id
			WHERE u.role_id != 4 AND u.status = 'active' AND u.deleted_at IS NULL
			ORDER BY u.fullname ASC`); err == nil {
			defer rows.Close()
			for rows.Next() {
				var rm rmItem
				if err := rows.Scan(&rm.ID, &rm.FullName, &rm.RoleName); err == nil {
					rms = append(rms, rm)
				}
			}
		}

		return c.JSON(fiber.Map{
			"status":            "success",
			"roles":             roles,
			"states":            states,
			"reportingManagers": rms,
		})
	})

	api.Post("/get_districts", func(c *fiber.Ctx) error {
		stateID := c.FormValue("state_id")
		type distItem struct {
			ID   int    `json:"id"`
			Name string `json:"district_name"`
		}
		districts := []distItem{}
		if rows, err := pool.Query("SELECT id, district_name FROM master_districts WHERE state_id = ? ORDER BY district_name ASC", stateID); err == nil {
			defer rows.Close()
			for rows.Next() {
				var d distItem
				if err := rows.Scan(&d.ID, &d.Name); err == nil {
					districts = append(districts, d)
				}
			}
		}
		return c.JSON(fiber.Map{
			"status": "success",
			"data":   districts,
		})
	})

	api.All("/list", func(c *fiber.Ctx) error {
		session := c.Locals(sessionContextKey).(dashboardSession)
		search := strings.TrimSpace(c.FormValue("search", c.Query("search", "")))
		roleID := strings.TrimSpace(c.FormValue("role_id", c.Query("role_id", "0")))
		status := strings.TrimSpace(c.FormValue("status", c.Query("status", "")))
		employmentType := strings.TrimSpace(c.FormValue("employment_type", c.Query("employment_type", "")))

		query := `SELECT u.id, u.fullname, u.username, COALESCE(u.email, ''), COALESCE(u.official_mobile, ''), COALESCE(u.personal_mobile, ''),
			COALESCE(u.employment_type, 'salary'), u.status, u.role_id, COALESCE(r.role_name, 'No Role'),
			COALESCE(s.state_name, '--'), COALESCE(d.district_name, '--'),
			COALESCE(u.working_location, ''), DATE_FORMAT(COALESCE(u.created_at, NOW()), '%d %b %Y'),
			COALESCE(u.failed_login_count, 0), COALESCE(u.last_failed_login, '')
			FROM users u
			LEFT JOIN roles r ON u.role_id = r.id
			LEFT JOIN master_states s ON u.state_id = s.id
			LEFT JOIN master_districts d ON u.district_id = d.id
			WHERE u.deleted_at IS NULL`

		args := []any{}
		if session.RoleID != 1 {
			query += " AND u.role_id NOT IN (1, 2)"
		}
		if search != "" {
			query += " AND (u.fullname LIKE ? OR u.username LIKE ? OR u.email LIKE ? OR u.official_mobile LIKE ? OR u.personal_mobile LIKE ?)"
			likePattern := "%" + search + "%"
			args = append(args, likePattern, likePattern, likePattern, likePattern, likePattern)
		}
		if roleID != "" && roleID != "0" {
			query += " AND u.role_id = ?"
			args = append(args, roleID)
		}
		if status == "active" || status == "inactive" {
			query += " AND u.status = ?"
			args = append(args, status)
		}
		if employmentType == "salary" || employmentType == "freelancer" {
			query += " AND u.employment_type = ?"
			args = append(args, employmentType)
		}
		query += " ORDER BY u.id DESC"

		rows, err := pool.Query(query, args...)
		if err != nil {
			return c.JSON(fiber.Map{"status": "error", "message": err.Error()})
		}
		defer rows.Close()

		type userRow struct {
			ID               int64  `json:"id"`
			FullName         string `json:"fullname"`
			Username         string `json:"username"`
			Email            string `json:"email"`
			OfficialMobile   string `json:"official_mobile"`
			PersonalMobile   string `json:"personal_mobile"`
			EmploymentType   string `json:"employment_type"`
			Status           string `json:"status"`
			RoleID           int    `json:"role_id"`
			RoleName         string `json:"role_name"`
			StateName        string `json:"state_name"`
			DistrictName     string `json:"district_name"`
			WorkingLocation  string `json:"working_location"`
			CreatedDate      string `json:"created_date"`
			FailedLoginCount int    `json:"failed_login_count"`
			LastFailedLogin  string `json:"last_failed_login"`
		}

		users := []userRow{}
		for rows.Next() {
			var u userRow
			if err := rows.Scan(
				&u.ID, &u.FullName, &u.Username, &u.Email, &u.OfficialMobile, &u.PersonalMobile,
				&u.EmploymentType, &u.Status, &u.RoleID, &u.RoleName,
				&u.StateName, &u.DistrictName,
				&u.WorkingLocation, &u.CreatedDate,
				&u.FailedLoginCount, &u.LastFailedLogin,
			); err == nil {
				users = append(users, u)
			}
		}

		return c.JSON(fiber.Map{
			"status": "success",
			"data":   users,
		})
	})

	api.Post("/get_details", func(c *fiber.Ctx) error {
		id := c.FormValue("id")
		if id == "" || id == "0" {
			return c.JSON(fiber.Map{"status": "error", "message": "Invalid User ID."})
		}

		var u struct {
			ID               int64          `json:"id"`
			RoleID           int            `json:"role_id"`
			AreaManagerID    sql.NullInt64  `json:"area_manager_id"`
			EmploymentType   string         `json:"employment_type"`
			FullName         string         `json:"fullname"`
			Username         string         `json:"username"`
			Email            sql.NullString `json:"email"`
			PersonalMobile   sql.NullString `json:"personal_mobile"`
			OfficialMobile   sql.NullString `json:"official_mobile"`
			PermanentAddress sql.NullString `json:"permanent_address"`
			StateID          sql.NullInt64  `json:"state_id"`
			DistrictID       sql.NullInt64  `json:"district_id"`
			WorkingLocation  sql.NullString `json:"working_location"`
			AllowedIPAddress sql.NullString `json:"allowed_ip_address"`
			Status           string         `json:"status"`
		}

		err := pool.QueryRow(`SELECT id, role_id, area_manager_id, COALESCE(employment_type, 'salary'),
			fullname, username, email, personal_mobile, official_mobile, permanent_address,
			state_id, district_id, working_location, allowed_ip_address, status
			FROM users WHERE id = ? AND deleted_at IS NULL LIMIT 1`, id).Scan(
			&u.ID, &u.RoleID, &u.AreaManagerID, &u.EmploymentType,
			&u.FullName, &u.Username, &u.Email, &u.PersonalMobile, &u.OfficialMobile, &u.PermanentAddress,
			&u.StateID, &u.DistrictID, &u.WorkingLocation, &u.AllowedIPAddress, &u.Status,
		)
		if err != nil {
			return c.JSON(fiber.Map{"status": "error", "message": "User not found."})
		}

		res := fiber.Map{
			"id":                 u.ID,
			"role_id":            u.RoleID,
			"area_manager_id":    0,
			"employment_type":    u.EmploymentType,
			"fullname":           u.FullName,
			"username":           u.Username,
			"email":              "",
			"personal_mobile":    "",
			"official_mobile":    "",
			"permanent_address":  "",
			"state_id":           0,
			"district_id":        0,
			"working_location":   "",
			"allowed_ip_address": "",
			"status":             u.Status,
		}
		if u.AreaManagerID.Valid {
			res["area_manager_id"] = u.AreaManagerID.Int64
		}
		if u.Email.Valid {
			res["email"] = u.Email.String
		}
		if u.PersonalMobile.Valid {
			res["personal_mobile"] = u.PersonalMobile.String
		}
		if u.OfficialMobile.Valid {
			res["official_mobile"] = u.OfficialMobile.String
		}
		if u.PermanentAddress.Valid {
			res["permanent_address"] = u.PermanentAddress.String
		}
		if u.StateID.Valid {
			res["state_id"] = u.StateID.Int64
		}
		if u.DistrictID.Valid {
			res["district_id"] = u.DistrictID.Int64
		}
		if u.WorkingLocation.Valid {
			res["working_location"] = u.WorkingLocation.String
		}
		if u.AllowedIPAddress.Valid {
			res["allowed_ip_address"] = u.AllowedIPAddress.String
		}

		return c.JSON(fiber.Map{"status": "success", "data": res})
	})

	api.Post("/save", func(c *fiber.Ctx) error {
		session := c.Locals(sessionContextKey).(dashboardSession)
		id := strings.TrimSpace(c.FormValue("id", "0"))
		fullname := strings.TrimSpace(c.FormValue("fullname", ""))
		username := strings.TrimSpace(c.FormValue("username", ""))
		email := strings.TrimSpace(c.FormValue("email", ""))
		officialMobile := strings.TrimSpace(c.FormValue("official_mobile", ""))
		personalMobile := strings.TrimSpace(c.FormValue("personal_mobile", ""))
		roleID := strings.TrimSpace(c.FormValue("role_id", "0"))
		areaManagerID := strings.TrimSpace(c.FormValue("area_manager_id", "0"))
		employmentType := strings.TrimSpace(c.FormValue("employment_type", "salary"))
		stateID := strings.TrimSpace(c.FormValue("state_id", "0"))
		districtID := strings.TrimSpace(c.FormValue("district_id", "0"))
		workingLocation := strings.TrimSpace(c.FormValue("working_location", ""))
		permanentAddress := strings.TrimSpace(c.FormValue("permanent_address", ""))
		allowedIP := strings.TrimSpace(c.FormValue("allowed_ip_address", ""))
		status := strings.TrimSpace(c.FormValue("status", "active"))
		password := strings.TrimSpace(c.FormValue("password", ""))

		if fullname == "" || username == "" || roleID == "0" {
			return c.JSON(fiber.Map{"status": "error", "message": "Full Name, Username and Role are required."})
		}
		if session.RoleID != 1 && (roleID == "1" || roleID == "2") {
			return c.JSON(fiber.Map{"status": "error", "message": "Permission Denied: Only Super Admin can assign Admin/Super Admin roles."})
		}

		var count int
		if id != "" && id != "0" {
			_ = pool.QueryRow("SELECT COUNT(id) FROM users WHERE username = ? AND id != ? AND deleted_at IS NULL", username, id).Scan(&count)
		} else {
			_ = pool.QueryRow("SELECT COUNT(id) FROM users WHERE username = ? AND deleted_at IS NULL", username).Scan(&count)
		}
		if count > 0 {
			return c.JSON(fiber.Map{"status": "error", "message": "Username already exists! Choose another."})
		}

		var amVal any = nil
		if areaManagerID != "" && areaManagerID != "0" {
			amVal = areaManagerID
		}
		var stateVal any = nil
		if stateID != "" && stateID != "0" {
			stateVal = stateID
		}
		var distVal any = nil
		if districtID != "" && districtID != "0" {
			distVal = districtID
		}

		now := time.Now().Format("2006-01-02 15:04:05")

		if id != "" && id != "0" {
			query := `UPDATE users SET fullname=?, username=?, email=?, official_mobile=?, personal_mobile=?,
				role_id=?, area_manager_id=?, employment_type=?, state_id=?, district_id=?,
				working_location=?, permanent_address=?, allowed_ip_address=?, status=?, updated_at=?`
			params := []any{fullname, username, email, officialMobile, personalMobile,
				roleID, amVal, employmentType, stateVal, distVal,
				workingLocation, permanentAddress, allowedIP, status, now}

			if password != "" {
				hashed, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
				if err == nil {
					query += ", password=?"
					params = append(params, string(hashed))
				}
			}
			if status == "active" || password != "" {
				query += ", failed_login_count=0, last_failed_login=NULL"
			}
			query += " WHERE id=?"
			params = append(params, id)

			_, err := pool.Exec(query, params...)
			if err != nil {
				return c.JSON(fiber.Map{"status": "error", "message": "Database Error: " + err.Error()})
			}
			return c.JSON(fiber.Map{"status": "success", "message": "User updated successfully!"})
		} else {
			if password == "" {
				return c.JSON(fiber.Map{"status": "error", "message": "Password is required for new users."})
			}
			hashed, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
			if err != nil {
				return c.JSON(fiber.Map{"status": "error", "message": "Password hashing failed."})
			}

			_, err = pool.Exec(`INSERT INTO users (fullname, username, email, official_mobile, personal_mobile,
				role_id, area_manager_id, employment_type, state_id, district_id,
				working_location, permanent_address, allowed_ip_address, status, password, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				fullname, username, email, officialMobile, personalMobile,
				roleID, amVal, employmentType, stateVal, distVal,
				workingLocation, permanentAddress, allowedIP, status, string(hashed), now, now,
			)
			if err != nil {
				return c.JSON(fiber.Map{"status": "error", "message": "Database Error: " + err.Error()})
			}
			return c.JSON(fiber.Map{"status": "success", "message": "New user added successfully!"})
		}
	})

	api.Post("/toggle_status", func(c *fiber.Ctx) error {
		id := c.FormValue("id")
		if id == "" || id == "0" {
			return c.JSON(fiber.Map{"status": "error", "message": "Invalid User ID."})
		}

		var curStatus string
		if err := pool.QueryRow("SELECT status FROM users WHERE id = ?", id).Scan(&curStatus); err != nil {
			return c.JSON(fiber.Map{"status": "error", "message": "User not found."})
		}

		newStatus := "active"
		if curStatus == "active" {
			newStatus = "inactive"
		}

		now := time.Now().Format("2006-01-02 15:04:05")
		if newStatus == "active" {
			_, _ = pool.Exec("UPDATE users SET status=?, failed_login_count=0, last_failed_login=NULL, updated_at=? WHERE id=?", newStatus, now, id)
		} else {
			_, _ = pool.Exec("UPDATE users SET status=?, updated_at=? WHERE id=?", newStatus, now, id)
		}

		return c.JSON(fiber.Map{
			"status":     "success",
			"message":    fmt.Sprintf("User status changed to %s", strings.Title(newStatus)),
			"new_status": newStatus,
		})
	})

	api.Post("/reset_failed_logins", func(c *fiber.Ctx) error {
		id := c.FormValue("id")
		if id == "" || id == "0" {
			return c.JSON(fiber.Map{"status": "error", "message": "Invalid User ID."})
		}
		now := time.Now().Format("2006-01-02 15:04:05")
		_, err := pool.Exec("UPDATE users SET failed_login_count=0, last_failed_login=NULL, status='active', updated_at=? WHERE id=?", now, id)
		if err != nil {
			return c.JSON(fiber.Map{"status": "error", "message": err.Error()})
		}
		return c.JSON(fiber.Map{"status": "success", "message": "User unlocked and reset to Active!"})
	})

	api.Post("/delete", func(c *fiber.Ctx) error {
		id := c.FormValue("id")
		if id == "" || id == "0" {
			return c.JSON(fiber.Map{"status": "error", "message": "Invalid User ID."})
		}
		now := time.Now().Format("2006-01-02 15:04:05")
		_, err := pool.Exec("UPDATE users SET deleted_at=?, status='inactive' WHERE id=?", now, id)
		if err != nil {
			return c.JSON(fiber.Map{"status": "error", "message": err.Error()})
		}
		return c.JSON(fiber.Map{"status": "success", "message": "User soft deleted successfully."})
	})
}
