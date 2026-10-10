package router

import (
	"database/sql"
	_ "embed"
	"fmt"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"
)

//go:embed templates/staff_mapping/index.html
var staffMappingHTML []byte

func ensureStaffMappingTables(pool *sql.DB) {
	if pool == nil {
		return
	}
	_, _ = pool.Exec(`
		CREATE TABLE IF NOT EXISTS icm_insurance_map (
			id INT AUTO_INCREMENT PRIMARY KEY,
			icm_id INT NOT NULL,
			insurance_company_id INT NOT NULL,
			status ENUM('active','inactive') DEFAULT 'active',
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
	`)

	_, _ = pool.Exec(`
		CREATE TABLE IF NOT EXISTS rm_insurance_map (
			id INT AUTO_INCREMENT PRIMARY KEY,
			rm_id INT NOT NULL,
			insurance_company_id INT NOT NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
	`)

	_, _ = pool.Exec(`
		CREATE TABLE IF NOT EXISTS master_geo_area_mapping (
			id INT AUTO_INCREMENT PRIMARY KEY,
			area_manager_id INT NOT NULL,
			state_id INT NOT NULL DEFAULT 0,
			district_id INT NOT NULL DEFAULT 0,
			status ENUM('active','inactive') DEFAULT 'active',
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
	`)
}

func RegisterStaffMappingRoutes(app *fiber.App, pool *sql.DB, requireSession fiber.Handler) {
	ensureStaffMappingTables(pool)

	app.Get("/staff_mapping", requireSession, func(c *fiber.Ctx) error {
		html := string(staffMappingHTML)
		html = strings.ReplaceAll(html, "{{SIDEBAR_PARTIAL}}", string(dashboardSidebarPartial))
		html = strings.ReplaceAll(html, "{{HEADER_PARTIAL}}", string(dashboardHeaderPartial))
		html = strings.ReplaceAll(html, "{{FOOTER_PARTIAL}}", string(dashboardFooterPartial))
		c.Type("html", "utf-8")
		return c.SendString(html)
	})

	api := app.Group("/api/staff_mapping", requireSession)

	api.All("/get_initial_data", func(c *fiber.Ctx) error {
		type userItem struct {
			ID       int    `json:"id"`
			FullName string `json:"fullname"`
		}
		type stateItem struct {
			ID        int    `json:"id"`
			StateName string `json:"state_name"`
		}
		type compItem struct {
			ID          int    `json:"id"`
			CompanyName string `json:"company_name"`
		}

		amUsers := []userItem{}
		if rows, err := pool.Query("SELECT id, fullname FROM users WHERE role_id IN (3, 5, 6, 7) AND status = 'active' AND deleted_at IS NULL ORDER BY fullname ASC"); err == nil {
			defer rows.Close()
			for rows.Next() {
				var u userItem
				if err := rows.Scan(&u.ID, &u.FullName); err == nil {
					amUsers = append(amUsers, u)
				}
			}
		}

		cmUsers := []userItem{}
		if rows, err := pool.Query("SELECT id, fullname FROM users WHERE role_id IN (3, 5, 7) AND status = 'active' AND deleted_at IS NULL ORDER BY fullname ASC"); err == nil {
			defer rows.Close()
			for rows.Next() {
				var u userItem
				if err := rows.Scan(&u.ID, &u.FullName); err == nil {
					cmUsers = append(cmUsers, u)
				}
			}
		}

		rmUsers := []userItem{}
		if rows, err := pool.Query("SELECT id, fullname FROM users WHERE role_id IN (3, 5, 7) AND status = 'active' AND deleted_at IS NULL ORDER BY fullname ASC"); err == nil {
			defer rows.Close()
			for rows.Next() {
				var u userItem
				if err := rows.Scan(&u.ID, &u.FullName); err == nil {
					rmUsers = append(rmUsers, u)
				}
			}
		}

		states := []stateItem{}
		if rows, err := pool.Query("SELECT id, state_name FROM master_states ORDER BY state_name ASC"); err == nil {
			defer rows.Close()
			for rows.Next() {
				var s stateItem
				if err := rows.Scan(&s.ID, &s.StateName); err == nil {
					states = append(states, s)
				}
			}
		}

		companies := []compItem{}
		if rows, err := pool.Query("SELECT id, company_name FROM master_insurance_companies WHERE status = 'active' ORDER BY company_name ASC"); err == nil {
			defer rows.Close()
			for rows.Next() {
				var cp compItem
				if err := rows.Scan(&cp.ID, &cp.CompanyName); err == nil {
					companies = append(companies, cp)
				}
			}
		}

		return c.JSON(fiber.Map{
			"status":    "success",
			"amUsers":   amUsers,
			"cmUsers":   cmUsers,
			"rmUsers":   rmUsers,
			"states":    states,
			"companies": companies,
		})
	})

	api.Post("/save", func(c *fiber.Ctx) error {
		mappingType := strings.TrimSpace(c.FormValue("mapping_type", "am"))

		switch mappingType {
		case "am":
			amID, _ := strconv.Atoi(c.FormValue("area_manager_id"))
			stateID, _ := strconv.Atoi(c.FormValue("state_id"))
			districtID, _ := strconv.Atoi(c.FormValue("district_id"))

			if amID <= 0 {
				return c.JSON(fiber.Map{"status": "error", "message": "Area Manager is required."})
			}

			_, err := pool.Exec("INSERT INTO master_geo_area_mapping (area_manager_id, state_id, district_id, status) VALUES (?, ?, ?, 'active')", amID, stateID, districtID)
			if err != nil {
				return c.JSON(fiber.Map{"status": "error", "message": "Database Error: " + err.Error()})
			}

		case "cm":
			cmID, _ := strconv.Atoi(c.FormValue("case_manager_id"))
			compID, _ := strconv.Atoi(c.FormValue("insurance_company_id"))

			if cmID <= 0 {
				return c.JSON(fiber.Map{"status": "error", "message": "Case Manager is required."})
			}

			if compID == 0 {
				rows, err := pool.Query("SELECT id FROM master_insurance_companies WHERE status = 'active'")
				if err == nil {
					defer rows.Close()
					for rows.Next() {
						var cid int
						if err := rows.Scan(&cid); err == nil {
							_, _ = pool.Exec("INSERT IGNORE INTO icm_insurance_map (icm_id, insurance_company_id, status) VALUES (?, ?, 'active')", cmID, cid)
						}
					}
				}
			} else {
				_, err := pool.Exec("INSERT IGNORE INTO icm_insurance_map (icm_id, insurance_company_id, status) VALUES (?, ?, 'active')", cmID, compID)
				if err != nil {
					return c.JSON(fiber.Map{"status": "error", "message": "Database Error: " + err.Error()})
				}
			}

		case "rm":
			rmID, _ := strconv.Atoi(c.FormValue("report_maker_id"))
			compID, _ := strconv.Atoi(c.FormValue("insurance_company_id"))

			if rmID <= 0 {
				return c.JSON(fiber.Map{"status": "error", "message": "Report Maker is required."})
			}

			if compID == 0 {
				rows, err := pool.Query("SELECT id FROM master_insurance_companies WHERE status = 'active'")
				if err == nil {
					defer rows.Close()
					for rows.Next() {
						var cid int
						if err := rows.Scan(&cid); err == nil {
							_, _ = pool.Exec("INSERT IGNORE INTO rm_insurance_map (rm_id, insurance_company_id) VALUES (?, ?)", rmID, cid)
						}
					}
				}
			} else {
				_, err := pool.Exec("INSERT IGNORE INTO rm_insurance_map (rm_id, insurance_company_id) VALUES (?, ?)", rmID, compID)
				if err != nil {
					return c.JSON(fiber.Map{"status": "error", "message": "Database Error: " + err.Error()})
				}
			}
		}

		return c.JSON(fiber.Map{
			"status":  "success",
			"message": "Mapping saved successfully!",
		})
	})

	api.All("/list", func(c *fiber.Ctx) error {
		filterType := strings.TrimSpace(c.FormValue("filter_type"))
		if filterType == "" {
			filterType = strings.TrimSpace(c.Query("filter_type"))
		}
		filterStaffIDStr := strings.TrimSpace(c.FormValue("filter_staff_id"))
		if filterStaffIDStr == "" {
			filterStaffIDStr = strings.TrimSpace(c.Query("filter_staff_id"))
		}
		filterStaffID, _ := strconv.Atoi(filterStaffIDStr)

		if filterType == "" {
			return c.JSON(fiber.Map{"status": "success", "data": []interface{}{}})
		}

		type ruleItem struct {
			ID   int    `json:"id"`
			Rule string `json:"rule"`
		}
		type groupedItem struct {
			StaffID     int        `json:"staff_id"`
			StaffName   string     `json:"staff_name"`
			MappingType string     `json:"mapping_type"`
			TypeCode    string     `json:"type_code"`
			Mappings    []ruleItem `json:"mappings"`
		}

		grouped := make(map[string]*groupedItem)
		var orderedKeys []string

		if filterType == "am" || filterType == "all" {
			query := `
				SELECT m.id, m.area_manager_id, 'Area Manager', u.fullname,
				       COALESCE(s.state_name, 'All States'),
				       COALESCE(d.district_name, 'All Districts')
				FROM master_geo_area_mapping m
				JOIN users u ON m.area_manager_id = u.id
				LEFT JOIN master_states s ON m.state_id = s.id AND m.state_id > 0
				LEFT JOIN master_districts d ON m.district_id = d.id AND m.district_id > 0
				WHERE 1=1
			`
			var args []interface{}
			if filterStaffID > 0 {
				query += " AND m.area_manager_id = ?"
				args = append(args, filterStaffID)
			}
			query += " ORDER BY u.fullname ASC, m.id DESC"

			if rows, err := pool.Query(query, args...); err == nil {
				defer rows.Close()
				for rows.Next() {
					var id, staffID int
					var mType, staffName, sName, dName string
					if err := rows.Scan(&id, &staffID, &mType, &staffName, &sName, &dName); err == nil {
						key := fmt.Sprintf("am_%d", staffID)
						if _, exists := grouped[key]; !exists {
							grouped[key] = &groupedItem{
								StaffID:     staffID,
								StaffName:   staffName,
								MappingType: mType,
								TypeCode:    "am",
								Mappings:    []ruleItem{},
							}
							orderedKeys = append(orderedKeys, key)
						}
						ruleText := sName
						if dName != "All Districts" {
							ruleText += " (" + dName + ")"
						}
						grouped[key].Mappings = append(grouped[key].Mappings, ruleItem{ID: id, Rule: ruleText})
					}
				}
			}
		}

		if filterType == "cm" || filterType == "all" {
			query := `
				SELECT m.id, m.icm_id, 'Case Manager', u.fullname, ic.company_name
				FROM icm_insurance_map m
				JOIN users u ON m.icm_id = u.id
				JOIN master_insurance_companies ic ON m.insurance_company_id = ic.id
				WHERE 1=1
			`
			var args []interface{}
			if filterStaffID > 0 {
				query += " AND m.icm_id = ?"
				args = append(args, filterStaffID)
			}
			query += " ORDER BY u.fullname ASC, m.id DESC"

			if rows, err := pool.Query(query, args...); err == nil {
				defer rows.Close()
				for rows.Next() {
					var id, staffID int
					var mType, staffName, compName string
					if err := rows.Scan(&id, &staffID, &mType, &staffName, &compName); err == nil {
						key := fmt.Sprintf("cm_%d", staffID)
						if _, exists := grouped[key]; !exists {
							grouped[key] = &groupedItem{
								StaffID:     staffID,
								StaffName:   staffName,
								MappingType: mType,
								TypeCode:    "cm",
								Mappings:    []ruleItem{},
							}
							orderedKeys = append(orderedKeys, key)
						}
						grouped[key].Mappings = append(grouped[key].Mappings, ruleItem{ID: id, Rule: compName})
					}
				}
			}
		}

		if filterType == "rm" || filterType == "all" {
			query := `
				SELECT m.id, m.rm_id, 'Report Maker', u.fullname, ic.company_name
				FROM rm_insurance_map m
				JOIN users u ON m.rm_id = u.id
				JOIN master_insurance_companies ic ON m.insurance_company_id = ic.id
				WHERE 1=1
			`
			var args []interface{}
			if filterStaffID > 0 {
				query += " AND m.rm_id = ?"
				args = append(args, filterStaffID)
			}
			query += " ORDER BY u.fullname ASC, m.id DESC"

			if rows, err := pool.Query(query, args...); err == nil {
				defer rows.Close()
				for rows.Next() {
					var id, staffID int
					var mType, staffName, compName string
					if err := rows.Scan(&id, &staffID, &mType, &staffName, &compName); err == nil {
						key := fmt.Sprintf("rm_%d", staffID)
						if _, exists := grouped[key]; !exists {
							grouped[key] = &groupedItem{
								StaffID:     staffID,
								StaffName:   staffName,
								MappingType: mType,
								TypeCode:    "rm",
								Mappings:    []ruleItem{},
							}
							orderedKeys = append(orderedKeys, key)
						}
						grouped[key].Mappings = append(grouped[key].Mappings, ruleItem{ID: id, Rule: compName})
					}
				}
			}
		}

		result := make([]groupedItem, 0, len(orderedKeys))
		for _, k := range orderedKeys {
			if item, ok := grouped[k]; ok {
				result = append(result, *item)
			}
		}

		return c.JSON(fiber.Map{
			"status": "success",
			"data":   result,
		})
	})

	api.Post("/get_staff_details", func(c *fiber.Ctx) error {
		staffID, _ := strconv.Atoi(c.FormValue("staff_id"))
		typeCode := strings.TrimSpace(c.FormValue("type_code"))

		if staffID <= 0 || typeCode == "" {
			return c.JSON(fiber.Map{"status": "error", "message": "Invalid parameters."})
		}

		var staffName string
		_ = pool.QueryRow("SELECT fullname FROM users WHERE id = ?", staffID).Scan(&staffName)
		if staffName == "" {
			staffName = "Staff Member"
		}

		type detailItem struct {
			ID           int    `json:"id"`
			StateName    string `json:"state_name,omitempty"`
			DistrictName string `json:"district_name,omitempty"`
			CompanyName  string `json:"company_name,omitempty"`
		}
		data := []detailItem{}

		switch typeCode {
		case "am":
			query := `
				SELECT m.id, COALESCE(s.state_name, 'All States'), COALESCE(d.district_name, 'All Districts')
				FROM master_geo_area_mapping m
				LEFT JOIN master_states s ON m.state_id = s.id AND m.state_id > 0
				LEFT JOIN master_districts d ON m.district_id = d.id AND m.district_id > 0
				WHERE m.area_manager_id = ?
				ORDER BY m.id DESC
			`
			if rows, err := pool.Query(query, staffID); err == nil {
				defer rows.Close()
				for rows.Next() {
					var item detailItem
					if err := rows.Scan(&item.ID, &item.StateName, &item.DistrictName); err == nil {
						data = append(data, item)
					}
				}
			}
		case "cm":
			query := `
				SELECT m.id, ic.company_name
				FROM icm_insurance_map m
				JOIN master_insurance_companies ic ON m.insurance_company_id = ic.id
				WHERE m.icm_id = ?
				ORDER BY m.id DESC
			`
			if rows, err := pool.Query(query, staffID); err == nil {
				defer rows.Close()
				for rows.Next() {
					var item detailItem
					if err := rows.Scan(&item.ID, &item.CompanyName); err == nil {
						data = append(data, item)
					}
				}
			}
		case "rm":
			query := `
				SELECT m.id, ic.company_name
				FROM rm_insurance_map m
				JOIN master_insurance_companies ic ON m.insurance_company_id = ic.id
				WHERE m.rm_id = ?
				ORDER BY m.id DESC
			`
			if rows, err := pool.Query(query, staffID); err == nil {
				defer rows.Close()
				for rows.Next() {
					var item detailItem
					if err := rows.Scan(&item.ID, &item.CompanyName); err == nil {
						data = append(data, item)
					}
				}
			}
		}

		return c.JSON(fiber.Map{
			"status":     "success",
			"staff_name": staffName,
			"type_code":  typeCode,
			"data":       data,
		})
	})

	api.Post("/delete", func(c *fiber.Ctx) error {
		id, _ := strconv.Atoi(c.FormValue("id"))
		mType := strings.TrimSpace(c.FormValue("mapping_type"))

		if id > 0 {
			if mType == "Area Manager" || mType == "am" {
				_, _ = pool.Exec("DELETE FROM master_geo_area_mapping WHERE id = ?", id)
			} else if mType == "Case Manager" || mType == "cm" {
				_, _ = pool.Exec("DELETE FROM icm_insurance_map WHERE id = ?", id)
			} else if mType == "Report Maker" || mType == "rm" {
				_, _ = pool.Exec("DELETE FROM rm_insurance_map WHERE id = ?", id)
			}
			return c.JSON(fiber.Map{"status": "success", "message": "Mapping removed successfully."})
		}
		return c.JSON(fiber.Map{"status": "error", "message": "Invalid ID."})
	})

	api.Post("/delete_all", func(c *fiber.Ctx) error {
		staffID, _ := strconv.Atoi(c.FormValue("staff_id"))
		typeCode := strings.TrimSpace(c.FormValue("type_code"))

		if staffID > 0 && typeCode != "" {
			if typeCode == "am" {
				_, _ = pool.Exec("DELETE FROM master_geo_area_mapping WHERE area_manager_id = ?", staffID)
			} else if typeCode == "cm" {
				_, _ = pool.Exec("DELETE FROM icm_insurance_map WHERE icm_id = ?", staffID)
			} else if typeCode == "rm" {
				_, _ = pool.Exec("DELETE FROM rm_insurance_map WHERE rm_id = ?", staffID)
			}
			return c.JSON(fiber.Map{"status": "success", "message": "All mappings for staff removed successfully."})
		}
		return c.JSON(fiber.Map{"status": "error", "message": "Invalid parameters."})
	})
}
