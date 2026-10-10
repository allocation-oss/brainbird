package router

import (
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"
)

//go:embed templates/menu_manager/index.html
var menuManagerHTML []byte

//go:embed templates/permissions/index.html
var permissionsHTML []byte

func ensureMenuPermissionTables(pool *sql.DB) {
	if pool == nil {
		return
	}
	_, _ = pool.Exec(`
		CREATE TABLE IF NOT EXISTS app_sidebar_menus (
			id INT AUTO_INCREMENT PRIMARY KEY,
			parent_id INT DEFAULT NULL,
			menu_name VARCHAR(100) NOT NULL,
			menu_slug VARCHAR(100) NOT NULL,
			icon VARCHAR(50) DEFAULT 'fa-solid fa-circle',
			sort_order INT DEFAULT 0,
			status ENUM('active','inactive') DEFAULT 'active',
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
	`)

	_, _ = pool.Exec(`
		CREATE TABLE IF NOT EXISTS role_module_permissions (
			id INT AUTO_INCREMENT PRIMARY KEY,
			role_id INT NOT NULL,
			module_id INT NOT NULL,
			can_view TINYINT(1) DEFAULT 0,
			can_add TINYINT(1) DEFAULT 0,
			can_edit TINYINT(1) DEFAULT 0,
			can_delete TINYINT(1) DEFAULT 0,
			UNIQUE KEY uq_role_module (role_id, module_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
	`)

	_, _ = pool.Exec(`
		CREATE TABLE IF NOT EXISTS user_module_permissions (
			id INT AUTO_INCREMENT PRIMARY KEY,
			user_id INT NOT NULL,
			module_id INT NOT NULL,
			can_view TINYINT(1) DEFAULT 0,
			can_add TINYINT(1) DEFAULT 0,
			can_edit TINYINT(1) DEFAULT 0,
			can_delete TINYINT(1) DEFAULT 0,
			UNIQUE KEY uq_user_module (user_id, module_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
	`)
}

func RegisterMenuPermissionRoutes(app *fiber.App, pool *sql.DB, requireSession fiber.Handler) {
	ensureMenuPermissionTables(pool)

	// Menu Manager Page
	app.Get("/menu_manager", requireSession, func(c *fiber.Ctx) error {
		htmlStr := string(menuManagerHTML)
		htmlStr = strings.ReplaceAll(htmlStr, "{{SIDEBAR_PARTIAL}}", string(dashboardSidebarPartial))
		htmlStr = strings.ReplaceAll(htmlStr, "{{HEADER_PARTIAL}}", string(dashboardHeaderPartial))
		htmlStr = strings.ReplaceAll(htmlStr, "{{FOOTER_PARTIAL}}", string(dashboardFooterPartial))
		c.Type("html", "utf-8")
		return c.SendString(htmlStr)
	})

	// Permission Manager Page
	app.Get("/permission_manager", requireSession, func(c *fiber.Ctx) error {
		htmlStr := string(permissionsHTML)
		htmlStr = strings.ReplaceAll(htmlStr, "{{SIDEBAR_PARTIAL}}", string(dashboardSidebarPartial))
		htmlStr = strings.ReplaceAll(htmlStr, "{{HEADER_PARTIAL}}", string(dashboardHeaderPartial))
		htmlStr = strings.ReplaceAll(htmlStr, "{{FOOTER_PARTIAL}}", string(dashboardFooterPartial))
		c.Type("html", "utf-8")
		return c.SendString(htmlStr)
	})

	// Menu Manager APIs
	apiMenu := app.Group("/api/menu_manager", requireSession)

	apiMenu.All("/list", func(c *fiber.Ctx) error {
		type menuItem struct {
			ID        int    `json:"id"`
			ParentID  *int   `json:"parent_id"`
			MenuName  string `json:"menu_name"`
			MenuSlug  string `json:"menu_slug"`
			Icon      string `json:"icon"`
			SortOrder int    `json:"sort_order"`
			Status    string `json:"status"`
		}

		menus := []menuItem{}
		rows, err := pool.Query("SELECT id, parent_id, menu_name, menu_slug, COALESCE(icon, 'fa-solid fa-circle'), COALESCE(sort_order, 0), status FROM app_sidebar_menus ORDER BY COALESCE(sort_order, 0) ASC, id ASC")
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var m menuItem
				var pID sql.NullInt64
				if err := rows.Scan(&m.ID, &pID, &m.MenuName, &m.MenuSlug, &m.Icon, &m.SortOrder, &m.Status); err == nil {
					if pID.Valid {
						val := int(pID.Int64)
						m.ParentID = &val
					}
					menus = append(menus, m)
				}
			}
		}

		return c.JSON(fiber.Map{"status": "success", "data": menus})
	})

	apiMenu.Post("/save", func(c *fiber.Ctx) error {
		id, _ := strconv.Atoi(c.FormValue("menu_id"))
		name := strings.TrimSpace(c.FormValue("module_name"))
		if name == "" {
			name = strings.TrimSpace(c.FormValue("menu_name"))
		}
		slug := strings.TrimSpace(c.FormValue("module_slug"))
		if slug == "" {
			slug = strings.TrimSpace(c.FormValue("menu_slug"))
		}
		iconClass := strings.TrimSpace(c.FormValue("icon_class"))
		if iconClass == "" {
			iconClass = strings.TrimSpace(c.FormValue("icon"))
		}
		parentId, _ := strconv.Atoi(c.FormValue("parent_id"))
		sortOrder, _ := strconv.Atoi(c.FormValue("sort_order"))
		status := strings.TrimSpace(c.FormValue("status"))
		if status == "" {
			status = "active"
		}

		if name == "" {
			return c.JSON(fiber.Map{"status": "error", "message": "Module Name is required!"})
		}
		if parentId > 0 && parentId == id {
			return c.JSON(fiber.Map{"status": "error", "message": "A module cannot be its own parent!"})
		}

		if slug == "" {
			slug = "#"
		}
		if iconClass == "" {
			iconClass = "fa-solid fa-circle"
		}

		var pVal interface{} = nil
		if parentId > 0 {
			pVal = parentId
		}

		if id > 0 {
			_, err := pool.Exec(`
				UPDATE app_sidebar_menus 
				SET menu_name = ?, menu_slug = ?, icon = ?, parent_id = ?, sort_order = ?, status = ?
				WHERE id = ?`, name, slug, iconClass, pVal, sortOrder, status, id)
			if err != nil {
				return c.Status(500).JSON(fiber.Map{"status": "error", "message": err.Error()})
			}
			return c.JSON(fiber.Map{"status": "success", "message": "Module updated successfully!"})
		} else {
			_, err := pool.Exec(`
				INSERT INTO app_sidebar_menus (menu_name, menu_slug, icon, parent_id, sort_order, status)
				VALUES (?, ?, ?, ?, ?, ?)`, name, slug, iconClass, pVal, sortOrder, status)
			if err != nil {
				return c.Status(500).JSON(fiber.Map{"status": "error", "message": err.Error()})
			}
			return c.JSON(fiber.Map{"status": "success", "message": "New Module created successfully!"})
		}
	})

	apiMenu.Post("/delete", func(c *fiber.Ctx) error {
		id, _ := strconv.Atoi(c.FormValue("id"))
		if id <= 0 {
			return c.JSON(fiber.Map{"status": "error", "message": "Invalid ID"})
		}

		// Also delete children
		childIDs := []int{}
		cRows, err := pool.Query("SELECT id FROM app_sidebar_menus WHERE parent_id = ?", id)
		if err == nil {
			defer cRows.Close()
			for cRows.Next() {
				var cid int
				if err := cRows.Scan(&cid); err == nil {
					childIDs = append(childIDs, cid)
				}
			}
		}

		allIDs := append([]int{id}, childIDs...)
		placeholders := make([]string, len(allIDs))
		args := make([]interface{}, len(allIDs))
		for i, v := range allIDs {
			placeholders[i] = "?"
			args[i] = v
		}

		inClause := strings.Join(placeholders, ",")
		_, _ = pool.Exec(fmt.Sprintf("DELETE FROM role_module_permissions WHERE module_id IN (%s)", inClause), args...)
		_, _ = pool.Exec(fmt.Sprintf("DELETE FROM user_module_permissions WHERE module_id IN (%s)", inClause), args...)
		_, _ = pool.Exec("DELETE FROM app_sidebar_menus WHERE parent_id = ?", id)
		_, _ = pool.Exec("DELETE FROM app_sidebar_menus WHERE id = ?", id)

		return c.JSON(fiber.Map{"status": "success", "message": "Module and its submodules deleted successfully!"})
	})

	// Permission Manager APIs
	apiPerm := app.Group("/api/permissions", requireSession)

	apiPerm.All("/initial_data", func(c *fiber.Ctx) error {
		type roleItem struct {
			ID   int    `json:"id"`
			Name string `json:"role_name"`
		}
		type userItem struct {
			ID       int    `json:"id"`
			FullName string `json:"fullname"`
		}
		type modItem struct {
			ID       int       `json:"id"`
			ParentID *int      `json:"parent_id"`
			Name     string    `json:"module_name"`
			Icon     string    `json:"icon"`
			Submenus []modItem `json:"submenus,omitempty"`
		}

		roles := []roleItem{}
		rRows, err := pool.Query("SELECT id, role_name FROM roles WHERE id != 1 ORDER BY role_name ASC")
		if err == nil {
			defer rRows.Close()
			for rRows.Next() {
				var r roleItem
				if err := rRows.Scan(&r.ID, &r.Name); err == nil {
					roles = append(roles, r)
				}
			}
		}

		users := []userItem{}
		uRows, err := pool.Query("SELECT id, fullname FROM users WHERE status = 'active' AND id != 1 ORDER BY fullname ASC")
		if err == nil {
			defer uRows.Close()
			for uRows.Next() {
				var u userItem
				if err := uRows.Scan(&u.ID, &u.FullName); err == nil {
					users = append(users, u)
				}
			}
		}

		rawMods := []modItem{}
		mRows, err := pool.Query("SELECT id, parent_id, menu_name, COALESCE(icon, 'fa-solid fa-circle') FROM app_sidebar_menus WHERE status = 'active' ORDER BY sort_order ASC, id ASC")
		if err == nil {
			defer mRows.Close()
			for mRows.Next() {
				var m modItem
				var pID sql.NullInt64
				if err := mRows.Scan(&m.ID, &pID, &m.Name, &m.Icon); err == nil {
					if pID.Valid {
						val := int(pID.Int64)
						m.ParentID = &val
					}
					rawMods = append(rawMods, m)
				}
			}
		}

		// Build tree
		parentMap := make(map[int][]modItem)
		for _, m := range rawMods {
			if m.ParentID != nil {
				parentMap[*m.ParentID] = append(parentMap[*m.ParentID], m)
			}
		}

		tree := []modItem{}
		for _, m := range rawMods {
			if m.ParentID == nil {
				if subs, ok := parentMap[m.ID]; ok {
					m.Submenus = subs
				}
				tree = append(tree, m)
			}
		}

		return c.JSON(fiber.Map{
			"status":  "success",
			"roles":   roles,
			"users":   users,
			"modules": tree,
		})
	})

	apiPerm.Post("/fetch", func(c *fiber.Ctx) error {
		targetType := c.FormValue("target_type", "role")
		targetId, _ := strconv.Atoi(c.FormValue("target_id"))

		if targetId <= 0 {
			return c.JSON(fiber.Map{"status": "error", "message": "Invalid ID"})
		}

		type permItem struct {
			ModuleID  int `json:"module_id"`
			CanView   int `json:"can_view"`
			CanAdd    int `json:"can_add"`
			CanEdit   int `json:"can_edit"`
			CanDelete int `json:"can_delete"`
		}

		mappedPerms := make(map[string]permItem)
		if pool == nil {
			return c.JSON(fiber.Map{"status": "success", "data": mappedPerms})
		}

		var query string
		if targetType == "user" {
			query = "SELECT module_id, can_view, can_add, can_edit, can_delete FROM user_module_permissions WHERE user_id = ?"
		} else {
			query = "SELECT module_id, can_view, can_add, can_edit, can_delete FROM role_module_permissions WHERE role_id = ?"
		}

		rows, err := pool.Query(query, targetId)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var p permItem
				if err := rows.Scan(&p.ModuleID, &p.CanView, &p.CanAdd, &p.CanEdit, &p.CanDelete); err == nil {
					mappedPerms[strconv.Itoa(p.ModuleID)] = p
				}
			}
		}

		return c.JSON(fiber.Map{"status": "success", "data": mappedPerms})
	})

	apiPerm.Post("/save", func(c *fiber.Ctx) error {
		targetType := c.FormValue("target_type", "role")
		targetId, _ := strconv.Atoi(c.FormValue("target_id"))
		permsJSON := c.FormValue("perms_json")

		if targetId <= 0 {
			return c.JSON(fiber.Map{"status": "error", "message": "Please select a Role or User first."})
		}

		var permsMap map[string]map[string]int
		if permsJSON != "" {
			_ = json.Unmarshal([]byte(permsJSON), &permsMap)
		}

		if targetType == "user" {
			_, _ = pool.Exec("DELETE FROM user_module_permissions WHERE user_id = ?", targetId)
		} else {
			_, _ = pool.Exec("DELETE FROM role_module_permissions WHERE role_id = ?", targetId)
		}

		for modStr, flags := range permsMap {
			modID, _ := strconv.Atoi(modStr)
			if modID <= 0 {
				continue
			}
			canView := flags["can_view"]
			canAdd := flags["can_add"]
			canEdit := flags["can_edit"]
			canDelete := flags["can_delete"]

			if canView > 0 || canAdd > 0 || canEdit > 0 || canDelete > 0 {
				if targetType == "user" {
					_, _ = pool.Exec(`
						INSERT INTO user_module_permissions (user_id, module_id, can_view, can_add, can_edit, can_delete)
						VALUES (?, ?, ?, ?, ?, ?)`, targetId, modID, canView, canAdd, canEdit, canDelete)
				} else {
					_, _ = pool.Exec(`
						INSERT INTO role_module_permissions (role_id, module_id, can_view, can_add, can_edit, can_delete)
						VALUES (?, ?, ?, ?, ?, ?)`, targetId, modID, canView, canAdd, canEdit, canDelete)
				}
			}
		}

		return c.JSON(fiber.Map{
			"status":  "success",
			"message": fmt.Sprintf("%s permissions updated successfully!", strings.Title(targetType)),
		})
	})
}
