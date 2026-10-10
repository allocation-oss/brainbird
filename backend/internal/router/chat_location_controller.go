package router

import (
	"database/sql"
	_ "embed"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"
)

//go:embed templates/masters/locations.html
var locationsHTML []byte

func ensureChatLocationTables(pool *sql.DB) {
	if pool == nil {
		return
	}
	pool.Exec(`CREATE TABLE IF NOT EXISTS master_states (
		id INT AUTO_INCREMENT PRIMARY KEY,
		state_name VARCHAR(100) NOT NULL,
		area_manager_id INT DEFAULT NULL
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`)

	pool.Exec(`CREATE TABLE IF NOT EXISTS master_districts (
		id INT AUTO_INCREMENT PRIMARY KEY,
		state_id INT NOT NULL,
		district_name VARCHAR(100) NOT NULL,
		area_manager_id INT DEFAULT NULL
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`)

	pool.Exec(`CREATE TABLE IF NOT EXISTS intel_chat_threads (
		id INT AUTO_INCREMENT PRIMARY KEY,
		type ENUM('individual','group','channel') DEFAULT 'individual',
		name VARCHAR(255) DEFAULT '',
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`)

	pool.Exec(`CREATE TABLE IF NOT EXISTS intel_chat_participants (
		id INT AUTO_INCREMENT PRIMARY KEY,
		thread_id INT NOT NULL,
		user_id INT NOT NULL,
		joined_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`)

	pool.Exec(`CREATE TABLE IF NOT EXISTS intel_chat_messages (
		id INT AUTO_INCREMENT PRIMARY KEY,
		thread_id INT DEFAULT NULL,
		sender_id INT DEFAULT NULL,
		receiver_id INT DEFAULT NULL,
		message LONGTEXT,
		status ENUM('failed','sent','delivered','seen') DEFAULT 'delivered',
		is_encrypted TINYINT(1) DEFAULT 1,
		ai_summary TEXT,
		translated_text LONGTEXT,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`)

	pool.Exec(`CREATE TABLE IF NOT EXISTS intel_call_signals (
		id INT AUTO_INCREMENT PRIMARY KEY,
		caller_id INT DEFAULT NULL,
		receiver_id INT DEFAULT NULL,
		thread_id INT DEFAULT NULL,
		call_type ENUM('audio','video') DEFAULT 'video',
		peer_id VARCHAR(255) DEFAULT '',
		status ENUM('ringing','accepted','rejected','ended') DEFAULT 'ringing',
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`)
}

// 25 excluded names matching Symfony LocationMasterController.php filterReportMakers exactly
var excludedReportMakerNames = []string{
	"adarsh saini",
	"adarsh",
	"arshpreet",
	"avinash",
	"brainbird",
	"dr amit",
	"dr. amit",
	"dr.amit",
	"ritik mohan",
	"hritik mohan",
	"hritik",
	"ritik",
	"indra kumar pandey",
	"indra pandey",
	"indra",
	"nitin singh",
	"prashant mishra",
	"priyanshi mishra",
	"sayli",
	"shweta sukla",
	"shweta shukla",
	"shwetta bhavesh shah",
	"shwetta",
	"bhavesh shah",
	"sweta bhawsshah",
	"sweta",
	"suraj singh",
	"vinya yadav",
	"vinaya yadav",
	"vinay yadav",
}

func filterReportMakers(users []fiber.Map) []fiber.Map {
	filtered := make([]fiber.Map, 0, len(users))
	for _, u := range users {
		name := ""
		if fn, ok := u["fullname"].(string); ok {
			name = strings.ToLower(strings.TrimSpace(fn))
		} else if nm, ok := u["name"].(string); ok {
			name = strings.ToLower(strings.TrimSpace(nm))
		}
		isExcluded := false
		for _, ex := range excludedReportMakerNames {
			if ex != "" && strings.Contains(name, ex) {
				isExcluded = true
				break
			}
		}
		if !isExcluded {
			filtered = append(filtered, u)
		}
	}
	return filtered
}

func getReportMakersData(pool *sql.DB) []fiber.Map {
	if pool == nil {
		return []fiber.Map{}
	}
	query := `
		SELECT id, fullname, IFNULL(employment_type, 'salary') as employment_type 
		FROM users 
		WHERE role_id = 3 AND status = 'active' 
		  AND LOWER(fullname) NOT LIKE '%arshpreet%'
		  AND LOWER(fullname) NOT LIKE '%avinash%'
		  AND LOWER(fullname) NOT LIKE '%brainbird%'
		  AND LOWER(fullname) NOT LIKE '%amit%'
		  AND LOWER(fullname) NOT LIKE '%hritik%'
		  AND LOWER(fullname) NOT LIKE '%ritik%'
		  AND LOWER(fullname) NOT LIKE '%indra%'
		  AND LOWER(fullname) NOT LIKE '%nitin%'
		  AND LOWER(fullname) NOT LIKE '%prashant%'
		  AND LOWER(fullname) NOT LIKE '%priyanshi%'
		  AND LOWER(fullname) NOT LIKE '%sayli%'
		  AND LOWER(fullname) NOT LIKE '%shweta%'
		  AND LOWER(fullname) NOT LIKE '%shwetta%'
		  AND LOWER(fullname) NOT LIKE '%sweta%'
		  AND LOWER(fullname) NOT LIKE '%suraj%'
		  AND LOWER(fullname) NOT LIKE '%vinay%'
		  AND LOWER(fullname) NOT LIKE '%vinya%'
		  AND LOWER(fullname) NOT LIKE '%adarsh%'
		ORDER BY fullname ASC
	`
	rows, err := pool.Query(query)
	if err != nil {
		return []fiber.Map{}
	}
	defer rows.Close()

	list := make([]fiber.Map, 0)
	for rows.Next() {
		var id int64
		var fullname, empType string
		if err := rows.Scan(&id, &fullname, &empType); err == nil {
			list = append(list, fiber.Map{
				"id":              id,
				"fullname":        fullname,
				"employment_type": empType,
			})
		}
	}
	return filterReportMakers(list)
}

func RegisterChatLocationRoutes(app *fiber.App, pool *sql.DB, requireSession fiber.Handler) {
	ensureChatLocationTables(pool)

	// ==========================================
	// 1. Location Master View & APIs
	// ==========================================
	app.Get("/masters/locations", requireSession, func(c *fiber.Ctx) error {
		states := make([]fiber.Map, 0)
		districts := make([]fiber.Map, 0)

		if pool != nil {
			sRows, err := pool.Query(`
				SELECT s.id, s.state_name, COUNT(d.id) as total_districts
				FROM master_states s
				LEFT JOIN master_districts d ON s.id = d.state_id
				GROUP BY s.id, s.state_name
				ORDER BY s.state_name ASC
			`)
			if err == nil {
				defer sRows.Close()
				for sRows.Next() {
					var sid int64
					var sname string
					var distCount int
					if err := sRows.Scan(&sid, &sname, &distCount); err == nil {
						states = append(states, fiber.Map{
							"id":              sid,
							"state_name":      sname,
							"total_districts": distCount,
						})
					}
				}
			}

			dRows, err := pool.Query(`
				SELECT d.id, d.state_id, d.district_name, COALESCE(s.state_name, '-') as state_name
				FROM master_districts d
				LEFT JOIN master_states s ON d.state_id = s.id
				ORDER BY s.state_name ASC, d.district_name ASC
			`)
			if err == nil {
				defer dRows.Close()
				for dRows.Next() {
					var did, dsid int64
					var dname, sname string
					if err := dRows.Scan(&did, &dsid, &dname, &sname); err == nil {
						districts = append(districts, fiber.Map{
							"id":            did,
							"state_id":      dsid,
							"district_name": dname,
							"state_name":    sname,
						})
					}
				}
			}
		}

		sJSON, _ := json.Marshal(states)
		dJSON, _ := json.Marshal(districts)

		html := string(locationsHTML)
		html = strings.ReplaceAll(html, "{{SIDEBAR_PARTIAL}}", string(dashboardSidebarPartial))
		html = strings.ReplaceAll(html, "{{HEADER_PARTIAL}}", string(dashboardHeaderPartial))
		html = strings.ReplaceAll(html, "{{FOOTER_PARTIAL}}", string(dashboardFooterPartial))
		html = strings.ReplaceAll(html, "{{STATES_JSON}}", string(sJSON))
		html = strings.ReplaceAll(html, "{{DISTRICTS_JSON}}", string(dJSON))

		c.Type("html", "utf-8")
		return c.SendString(html)
	})

	// Add State
	app.Post("/api/locations/state/add", requireSession, func(c *fiber.Ctx) error {
		name := strings.TrimSpace(c.FormValue("state_name"))
		if name == "" {
			return c.JSON(fiber.Map{"status": "error", "message": "State name is required"})
		}
		if pool != nil {
			_, err := pool.Exec("INSERT INTO master_states (state_name) VALUES (?)", name)
			if err != nil {
				return c.JSON(fiber.Map{"status": "error", "message": err.Error()})
			}
		}
		return c.JSON(fiber.Map{"status": "success", "message": "State added successfully"})
	})

	// Edit State
	app.Post("/api/locations/state/edit", requireSession, func(c *fiber.Ctx) error {
		id, _ := strconv.ParseInt(c.FormValue("id"), 10, 64)
		name := strings.TrimSpace(c.FormValue("state_name"))
		if id <= 0 || name == "" {
			return c.JSON(fiber.Map{"status": "error", "message": "Invalid state ID or name"})
		}
		if pool != nil {
			_, err := pool.Exec("UPDATE master_states SET state_name = ? WHERE id = ?", name, id)
			if err != nil {
				return c.JSON(fiber.Map{"status": "error", "message": err.Error()})
			}
		}
		return c.JSON(fiber.Map{"status": "success", "message": "State updated successfully"})
	})

	// Delete State
	app.Post("/api/locations/state/delete", requireSession, func(c *fiber.Ctx) error {
		id, _ := strconv.ParseInt(c.FormValue("id"), 10, 64)
		if id <= 0 {
			return c.JSON(fiber.Map{"status": "error", "message": "Invalid state ID"})
		}
		if pool != nil {
			pool.Exec("DELETE FROM master_districts WHERE state_id = ?", id)
			_, err := pool.Exec("DELETE FROM master_states WHERE id = ?", id)
			if err != nil {
				return c.JSON(fiber.Map{"status": "error", "message": err.Error()})
			}
		}
		return c.JSON(fiber.Map{"status": "success", "message": "State and related districts deleted successfully"})
	})

	// Add District
	app.Post("/api/locations/district/add", requireSession, func(c *fiber.Ctx) error {
		stateID, _ := strconv.ParseInt(c.FormValue("state_id"), 10, 64)
		name := strings.TrimSpace(c.FormValue("district_name"))
		if stateID <= 0 || name == "" {
			return c.JSON(fiber.Map{"status": "error", "message": "State and District name are required"})
		}
		if pool != nil {
			_, err := pool.Exec("INSERT INTO master_districts (state_id, district_name) VALUES (?, ?)", stateID, name)
			if err != nil {
				return c.JSON(fiber.Map{"status": "error", "message": err.Error()})
			}
		}
		return c.JSON(fiber.Map{"status": "success", "message": "District added successfully"})
	})

	// Edit District
	app.Post("/api/locations/district/edit", requireSession, func(c *fiber.Ctx) error {
		id, _ := strconv.ParseInt(c.FormValue("id"), 10, 64)
		stateID, _ := strconv.ParseInt(c.FormValue("state_id"), 10, 64)
		name := strings.TrimSpace(c.FormValue("district_name"))
		if id <= 0 || stateID <= 0 || name == "" {
			return c.JSON(fiber.Map{"status": "error", "message": "Invalid district parameters"})
		}
		if pool != nil {
			_, err := pool.Exec("UPDATE master_districts SET state_id = ?, district_name = ? WHERE id = ?", stateID, name, id)
			if err != nil {
				return c.JSON(fiber.Map{"status": "error", "message": err.Error()})
			}
		}
		return c.JSON(fiber.Map{"status": "success", "message": "District updated successfully"})
	})

	// Delete District
	app.Post("/api/locations/district/delete", requireSession, func(c *fiber.Ctx) error {
		id, _ := strconv.ParseInt(c.FormValue("id"), 10, 64)
		if id <= 0 {
			return c.JSON(fiber.Map{"status": "error", "message": "Invalid district ID"})
		}
		if pool != nil {
			_, err := pool.Exec("DELETE FROM master_districts WHERE id = ?", id)
			if err != nil {
				return c.JSON(fiber.Map{"status": "error", "message": err.Error()})
			}
		}
		return c.JSON(fiber.Map{"status": "success", "message": "District deleted successfully"})
	})

	// ==========================================
	// 2. Case Edit & Report Makers
	// ==========================================
	app.Get("/case/edit", requireSession, func(c *fiber.Ctx) error {
		return renderMasterEdit(c, pool)
	})
	app.Post("/case/edit", requireSession, func(c *fiber.Ctx) error {
		return renderMasterEdit(c, pool)
	})

	reportMakersHandler := func(c *fiber.Ctx) error {
		rms := getReportMakersData(pool)
		return c.JSON(fiber.Map{
			"status":       "success",
			"data":         rms,
			"reportMakers": rms,
		})
	}
	app.Get("/api/get_report_makers", requireSession, reportMakersHandler)
	app.Post("/api/get_report_makers", requireSession, reportMakersHandler)
	app.Get("/api/case/report_makers", requireSession, reportMakersHandler)
	app.Post("/api/case/report_makers", requireSession, reportMakersHandler)

	// ==========================================
	// 3. Intel AI Chat & Calling APIs
	// ==========================================
	// /api/intel-chat/get-users
	intelGetUsersHandler := func(c *fiber.Ctx) error {
		var uid int64 = 0
		if sess, ok := c.Locals(sessionContextKey).(dashboardSession); ok {
			uid = sess.UserID
		}
		if uid <= 0 {
			uid, _ = strconv.ParseInt(c.FormValue("user_id", c.Query("user_id")), 10, 64)
		}
		if uid <= 0 {
			return c.JSON(fiber.Map{"status": "error", "message": "Unauthorized"})
		}

		search := strings.TrimSpace(c.FormValue("search", c.Query("search")))
		filter := strings.TrimSpace(c.FormValue("filter", "all"))
		mode := strings.TrimSpace(c.FormValue("mode", "conversations"))

		if pool == nil {
			return c.JSON(fiber.Map{"status": "success", "users": []fiber.Map{}, "current_user_id": uid})
		}

		users := make([]fiber.Map, 0)

		if mode == "directory" {
			// ALL active users for + New Chat search
			query := `
				SELECT 
					u.id as user_id, 
					u.fullname, 
					u.username, 
					0 as is_group,
					IF(u.updated_at >= NOW() - INTERVAL 5 MINUTE, 1, 0) as is_online,
					NULL as last_msg,
					NULL as last_msg_time,
					'delivered' as msg_status
				FROM users u
				WHERE u.id != ? AND u.status = 'active'
			`
			args := []any{uid}
			if search != "" {
				query += " AND (u.fullname LIKE ? OR u.username LIKE ?) "
				args = append(args, "%"+search+"%", "%"+search+"%")
			}
			query += " ORDER BY is_online DESC, u.fullname ASC"

			rows, err := pool.Query(query, args...)
			if err == nil {
				defer rows.Close()
				for rows.Next() {
					var uID int64
					var fname, uname, msgStatus string
					var isGrp, isOnline int
					var lastMsg, lastTime sql.NullString
					if err := rows.Scan(&uID, &fname, &uname, &isGrp, &isOnline, &lastMsg, &lastTime, &msgStatus); err == nil {
						users = append(users, fiber.Map{
							"user_id":       uID,
							"fullname":      fname,
							"username":      uname,
							"is_group":      isGrp,
							"is_online":     isOnline,
							"last_msg":      lastMsg.String,
							"last_msg_time": lastTime.String,
							"msg_status":    msgStatus,
						})
					}
				}
			}
		} else if filter == "groups" {
			// Groups with existing messages
			query := `
				SELECT 
					t.id as user_id, 
					t.name as fullname, 
					'group' as username, 
					0 as is_online, 
					1 as is_group,
					(SELECT message FROM intel_chat_messages WHERE thread_id = t.id ORDER BY id DESC LIMIT 1) as last_msg,
					(SELECT created_at FROM intel_chat_messages WHERE thread_id = t.id ORDER BY id DESC LIMIT 1) as last_msg_time,
					'delivered' as msg_status
				FROM intel_chat_threads t
				JOIN intel_chat_participants p ON t.id = p.thread_id
				WHERE p.user_id = ? AND t.type = 'group'
			`
			args := []any{uid}
			if search != "" {
				query += " AND t.name LIKE ? "
				args = append(args, "%"+search+"%")
			}
			query += " HAVING last_msg IS NOT NULL ORDER BY last_msg_time DESC"

			rows, err := pool.Query(query, args...)
			if err == nil {
				defer rows.Close()
				for rows.Next() {
					var tID int64
					var fname, uname, msgStatus string
					var isOnline, isGrp int
					var lastMsg, lastTime sql.NullString
					if err := rows.Scan(&tID, &fname, &uname, &isOnline, &isGrp, &lastMsg, &lastTime, &msgStatus); err == nil {
						users = append(users, fiber.Map{
							"user_id":       tID,
							"fullname":      fname,
							"username":      uname,
							"is_online":     isOnline,
							"is_group":      isGrp,
							"last_msg":      lastMsg.String,
							"last_msg_time": lastTime.String,
							"msg_status":    msgStatus,
						})
					}
				}
			}
		} else {
			// Main Conversations List: ONLY users with whom chat has actually happened
			query := `
				SELECT 
					u.id as user_id, 
					u.fullname, 
					u.username, 
					0 as is_group,
					IF(u.updated_at >= NOW() - INTERVAL 5 MINUTE, 1, 0) as is_online,
					m.message as last_msg,
					m.created_at as last_msg_time,
					m.sender_id as last_msg_sender,
					COALESCE(m.status, 'delivered') as msg_status
				FROM users u
				INNER JOIN (
					SELECT m1.*
					FROM intel_chat_messages m1
					INNER JOIN (
						SELECT MAX(id) as max_id
						FROM intel_chat_messages
						WHERE thread_id IS NULL OR thread_id = 0
						GROUP BY IF(sender_id < receiver_id, CONCAT(sender_id, '_', receiver_id), CONCAT(receiver_id, '_', sender_id))
					) m2 ON m1.id = m2.max_id
				) m ON (m.sender_id = u.id AND m.receiver_id = ?) OR (m.receiver_id = u.id AND m.sender_id = ?)
				WHERE u.id != ? AND u.status = 'active'
			`
			args := []any{uid, uid, uid}
			if search != "" {
				query += " AND (u.fullname LIKE ? OR u.username LIKE ?) "
				args = append(args, "%"+search+"%", "%"+search+"%")
			}
			if filter == "unread" {
				query += " AND m.status != 'seen' AND m.sender_id != ? "
				args = append(args, uid)
			}
			query += " ORDER BY last_msg_time DESC, is_online DESC, u.fullname ASC"

			rows, err := pool.Query(query, args...)
			if err == nil {
				defer rows.Close()
				for rows.Next() {
					var uID int64
					var fname, uname, msgStatus string
					var isGrp, isOnline int
					var lastSender sql.NullInt64
					var lastMsg, lastTime sql.NullString
					if err := rows.Scan(&uID, &fname, &uname, &isGrp, &isOnline, &lastMsg, &lastTime, &lastSender, &msgStatus); err == nil {
						users = append(users, fiber.Map{
							"user_id":       uID,
							"fullname":      fname,
							"username":      uname,
							"is_group":      isGrp,
							"is_online":     isOnline,
							"last_msg":      lastMsg.String,
							"last_msg_time": lastTime.String,
							"msg_status":    msgStatus,
						})
					}
				}
			}
		}

		return c.JSON(fiber.Map{
			"status":          "success",
			"users":           users,
			"current_user_id": uid,
		})
	}
	app.Get("/api/intel-chat/get-users", requireSession, intelGetUsersHandler)
	app.Post("/api/intel-chat/get-users", requireSession, intelGetUsersHandler)

	// /api/intel-chat/get-messages
	app.Post("/api/intel-chat/get-messages", requireSession, func(c *fiber.Ctx) error {
		var uid int64 = 0
		if sess, ok := c.Locals(sessionContextKey).(dashboardSession); ok {
			uid = sess.UserID
		}
		if uid <= 0 {
			uid, _ = strconv.ParseInt(c.FormValue("user_id"), 10, 64)
		}
		if uid <= 0 {
			return c.JSON(fiber.Map{"status": "error", "message": "Unauthorized"})
		}

		partnerID, _ := strconv.ParseInt(c.FormValue("partner_id"), 10, 64)
		isGroup, _ := strconv.Atoi(c.FormValue("is_group", "0"))

		if pool == nil {
			return c.JSON(fiber.Map{"status": "success", "messages": []fiber.Map{}})
		}

		messages := make([]fiber.Map, 0)
		if isGroup == 1 {
			query := `
				SELECT m.id, m.thread_id, m.sender_id, m.receiver_id, m.message, m.status, m.created_at, u.fullname as sender_name 
				FROM intel_chat_messages m
				LEFT JOIN users u ON m.sender_id = u.id
				WHERE m.thread_id = ?
				ORDER BY m.id ASC
			`
			rows, err := pool.Query(query, partnerID)
			if err == nil {
				defer rows.Close()
				for rows.Next() {
					var mid int64
					var tid, sid, rid sql.NullInt64
					var msg, st, cat string
					var sname sql.NullString
					if err := rows.Scan(&mid, &tid, &sid, &rid, &msg, &st, &cat, &sname); err == nil {
						messages = append(messages, fiber.Map{
							"id":          mid,
							"thread_id":   tid.Int64,
							"sender_id":   sid.Int64,
							"receiver_id": rid.Int64,
							"message":     msg,
							"status":      st,
							"created_at":  cat,
							"sender_name": sname.String,
						})
					}
				}
			}
		} else {
			query := `
				SELECT m.id, m.thread_id, m.sender_id, m.receiver_id, m.message, m.status, m.created_at, u.fullname as sender_name 
				FROM intel_chat_messages m
				LEFT JOIN users u ON m.sender_id = u.id
				WHERE (m.sender_id = ? AND m.receiver_id = ?) OR (m.sender_id = ? AND m.receiver_id = ?)
				ORDER BY m.id ASC
			`
			rows, err := pool.Query(query, uid, partnerID, partnerID, uid)
			if err == nil {
				defer rows.Close()
				for rows.Next() {
					var mid int64
					var tid, sid, rid sql.NullInt64
					var msg, st, cat string
					var sname sql.NullString
					if err := rows.Scan(&mid, &tid, &sid, &rid, &msg, &st, &cat, &sname); err == nil {
						messages = append(messages, fiber.Map{
							"id":          mid,
							"thread_id":   tid.Int64,
							"sender_id":   sid.Int64,
							"receiver_id": rid.Int64,
							"message":     msg,
							"status":      st,
							"created_at":  cat,
							"sender_name": sname.String,
						})
					}
				}
			}
			pool.Exec("UPDATE intel_chat_messages SET status = 'seen' WHERE sender_id = ? AND receiver_id = ?", partnerID, uid)
		}

		return c.JSON(fiber.Map{
			"status":   "success",
			"messages": messages,
		})
	})

	// /api/intel-chat/send
	app.Post("/api/intel-chat/send", requireSession, func(c *fiber.Ctx) error {
		var uid int64 = 0
		if sess, ok := c.Locals(sessionContextKey).(dashboardSession); ok {
			uid = sess.UserID
		}
		if uid <= 0 {
			uid, _ = strconv.ParseInt(c.FormValue("user_id"), 10, 64)
		}
		receiverID, _ := strconv.ParseInt(c.FormValue("receiver_id"), 10, 64)
		isGroup, _ := strconv.Atoi(c.FormValue("is_group", "0"))
		message := strings.TrimSpace(c.FormValue("message"))

		if uid <= 0 || message == "" {
			return c.JSON(fiber.Map{"status": "error", "message": "Message cannot be empty"})
		}

		if pool != nil {
			if isGroup == 1 {
				_, err := pool.Exec(
					"INSERT INTO intel_chat_messages (thread_id, sender_id, message, is_encrypted, status) VALUES (?, ?, ?, 1, 'delivered')",
					receiverID, uid, message,
				)
				if err != nil {
					return c.JSON(fiber.Map{"status": "error", "message": err.Error()})
				}
			} else {
				_, err := pool.Exec(
					"INSERT INTO intel_chat_messages (sender_id, receiver_id, message, is_encrypted, status) VALUES (?, ?, ?, 1, 'delivered')",
					uid, receiverID, message,
				)
				if err != nil {
					return c.JSON(fiber.Map{"status": "error", "message": err.Error()})
				}
			}
		}

		return c.JSON(fiber.Map{"status": "success"})
	})

	// /api/intel-chat/create-group
	app.Post("/api/intel-chat/create-group", requireSession, func(c *fiber.Ctx) error {
		var uid int64 = 0
		if sess, ok := c.Locals(sessionContextKey).(dashboardSession); ok {
			uid = sess.UserID
		}
		groupName := strings.TrimSpace(c.FormValue("group_name"))

		// Member IDs can be array from form or comma-separated
		var memberIDs []int64
		if mStr := c.FormValue("member_ids"); mStr != "" {
			parts := strings.Split(mStr, ",")
			for _, p := range parts {
				if id, err := strconv.ParseInt(strings.TrimSpace(p), 10, 64); err == nil && id > 0 {
					memberIDs = append(memberIDs, id)
				}
			}
		}
		// Also parse individual member_ids[] from multipart form
		if reqForm, err := c.MultipartForm(); err == nil && reqForm != nil {
			if vals, exists := reqForm.Value["member_ids[]"]; exists {
				for _, val := range vals {
					if id, err := strconv.ParseInt(strings.TrimSpace(val), 10, 64); err == nil && id > 0 {
						memberIDs = append(memberIDs, id)
					}
				}
			}
		}

		if uid <= 0 || groupName == "" || len(memberIDs) == 0 {
			return c.JSON(fiber.Map{"status": "error", "message": "Group name and members are required."})
		}

		if pool != nil {
			tx, err := pool.Begin()
			if err != nil {
				return c.JSON(fiber.Map{"status": "error", "message": err.Error()})
			}
			res, err := tx.Exec("INSERT INTO intel_chat_threads (type, name, created_at) VALUES ('group', ?, NOW())", groupName)
			if err != nil {
				tx.Rollback()
				return c.JSON(fiber.Map{"status": "error", "message": err.Error()})
			}
			threadID, _ := res.LastInsertId()
			tx.Exec("INSERT INTO intel_chat_participants (thread_id, user_id) VALUES (?, ?)", threadID, uid)
			for _, mID := range memberIDs {
				tx.Exec("INSERT INTO intel_chat_participants (thread_id, user_id) VALUES (?, ?)", threadID, mID)
			}
			if err := tx.Commit(); err != nil {
				return c.JSON(fiber.Map{"status": "error", "message": err.Error()})
			}
		}

		return c.JSON(fiber.Map{"status": "success", "message": "Group created successfully!"})
	})

	// /api/intel-chat/call-signal
	app.Post("/api/intel-chat/call-signal", requireSession, func(c *fiber.Ctx) error {
		var uid int64 = 0
		if sess, ok := c.Locals(sessionContextKey).(dashboardSession); ok {
			uid = sess.UserID
		}
		action := c.FormValue("signal_action")

		if action == "initiate" && pool != nil {
			receiverID, _ := strconv.ParseInt(c.FormValue("receiver_id"), 10, 64)
			callType := c.FormValue("call_type", "video")
			peerID := c.FormValue("peer_id")

			res, err := pool.Exec(
				"INSERT INTO intel_call_signals (caller_id, receiver_id, call_type, peer_id, status) VALUES (?, ?, ?, ?, 'ringing')",
				uid, receiverID, callType, peerID,
			)
			if err != nil {
				return c.JSON(fiber.Map{"status": "error", "message": err.Error()})
			}
			callID, _ := res.LastInsertId()
			return c.JSON(fiber.Map{"status": "success", "call_id": callID})
		}

		return c.JSON(fiber.Map{"status": "error", "message": "Invalid signal action"})
	})

	// /api/intel-chat/ai-tools
	app.Post("/api/intel-chat/ai-tools", requireSession, func(c *fiber.Ctx) error {
		action := c.FormValue("ai_action")
		aiResponse := ""
		if action == "compose" {
			aiResponse = "Hello, I have reviewed the case details and everything looks good."
		}
		return c.JSON(fiber.Map{"status": "success", "data": aiResponse})
	})
}
