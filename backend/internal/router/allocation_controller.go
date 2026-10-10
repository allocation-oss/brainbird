package router

import (
	"database/sql"
	_ "embed"
	"strings"

	"github.com/gofiber/fiber/v2"
)

//go:embed templates/users/allocation_manager.html
var allocationManagerHTML []byte

func RegisterAllocationRoutes(app *fiber.App, pool *sql.DB, requireSession fiber.Handler) {
	app.Get("/users/allocation_manager", requireSession, func(c *fiber.Ctx) error {
		html := string(allocationManagerHTML)
		html = strings.ReplaceAll(html, "{{SIDEBAR_PARTIAL}}", string(dashboardSidebarPartial))
		html = strings.ReplaceAll(html, "{{HEADER_PARTIAL}}", string(dashboardHeaderPartial))
		html = strings.ReplaceAll(html, "{{FOOTER_PARTIAL}}", string(dashboardFooterPartial))
		c.Type("html", "utf-8")
		return c.SendString(html)
	})

	api := app.Group("/api/alloc_mgr", requireSession)

	api.All("/get_initial_data", func(c *fiber.Ctx) error {
		type simpleItem struct {
			ID   int    `json:"id"`
			Name string `json:"fullname,omitempty"`
			StateName string `json:"state_name,omitempty"`
			CompanyName string `json:"company_name,omitempty"`
			TypeName string `json:"type_name,omitempty"`
		}

		type amItem struct {
			ID       int    `json:"id"`
			FullName string `json:"fullname"`
		}
		allocMgrs := []amItem{}
		if rows, err := pool.Query("SELECT id, fullname FROM users WHERE role_id = 4 AND status = 'active' AND deleted_at IS NULL ORDER BY fullname ASC"); err == nil {
			defer rows.Close()
			for rows.Next() {
				var a amItem
				if err := rows.Scan(&a.ID, &a.FullName); err == nil {
					allocMgrs = append(allocMgrs, a)
				}
			}
		}

		type stItem struct {
			ID   int    `json:"id"`
			Name string `json:"state_name"`
		}
		states := []stItem{}
		if rows, err := pool.Query("SELECT id, state_name FROM master_states ORDER BY state_name ASC"); err == nil {
			defer rows.Close()
			for rows.Next() {
				var s stItem
				if err := rows.Scan(&s.ID, &s.Name); err == nil {
					states = append(states, s)
				}
			}
		}

		type cpItem struct {
			ID   int    `json:"id"`
			Name string `json:"company_name"`
		}
		companies := []cpItem{}
		if rows, err := pool.Query("SELECT id, company_name FROM master_insurance_companies WHERE status = 'active' ORDER BY company_name ASC"); err == nil {
			defer rows.Close()
			for rows.Next() {
				var cp cpItem
				if err := rows.Scan(&cp.ID, &cp.Name); err == nil {
					companies = append(companies, cp)
				}
			}
		}

		type catItem struct {
			ID   int    `json:"id"`
			Name string `json:"type_name"`
		}
		categories := []catItem{}
		if rows, err := pool.Query("SELECT id, type_name FROM master_case_types WHERE status = 'active' ORDER BY type_name ASC"); err == nil {
			defer rows.Close()
			for rows.Next() {
				var ct catItem
				if err := rows.Scan(&ct.ID, &ct.Name); err == nil {
					categories = append(categories, ct)
				}
			}
		}

		return c.JSON(fiber.Map{
			"status":        "success",
			"allocManagers": allocMgrs,
			"states":        states,
			"companies":     companies,
			"categories":    categories,
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
		return c.JSON(fiber.Map{"status": "success", "data": districts})
	})

	api.Post("/get_subcategories", func(c *fiber.Ctx) error {
		catID := c.FormValue("category_id")
		type subItem struct {
			ID   int    `json:"id"`
			Name string `json:"sub_type_name"`
		}
		subcats := []subItem{}
		if rows, err := pool.Query("SELECT id, sub_type_name FROM master_case_sub_types WHERE case_type_id = ? AND status = 'active' ORDER BY sub_type_name ASC", catID); err == nil {
			defer rows.Close()
			for rows.Next() {
				var sc subItem
				if err := rows.Scan(&sc.ID, &sc.Name); err == nil {
					subcats = append(subcats, sc)
				}
			}
		}
		return c.JSON(fiber.Map{"status": "success", "data": subcats})
	})

	api.Post("/save", func(c *fiber.Ctx) error {
		allocMgrID := c.FormValue("allocation_manager_id")
		stateID := c.FormValue("state_id")
		districtID := c.FormValue("district_id")
		companyID := c.FormValue("insurance_company_id")
		categoryID := c.FormValue("category_id")
		subcategoryID := c.FormValue("subcategory_id")

		if allocMgrID == "" || allocMgrID == "0" {
			return c.JSON(fiber.Map{"status": "error", "message": "Allocation Manager is required."})
		}

		var sVal, dVal, compVal, catVal, subVal any
		if stateID != "" && stateID != "0" { sVal = stateID }
		if districtID != "" && districtID != "0" { dVal = districtID }
		if companyID != "" && companyID != "0" { compVal = companyID }
		if categoryID != "" && categoryID != "0" { catVal = categoryID }
		if subcategoryID != "" && subcategoryID != "0" { subVal = subcategoryID }

		_, err := pool.Exec(`INSERT INTO master_geo_alloc_mapping
			(allocation_manager_id, state_id, district_id, insurance_company_id, category_id, subcategory_id)
			VALUES (?, ?, ?, ?, ?, ?)`,
			allocMgrID, sVal, dVal, compVal, catVal, subVal,
		)
		if err != nil {
			return c.JSON(fiber.Map{"status": "error", "message": "Database Error: " + err.Error()})
		}

		return c.JSON(fiber.Map{"status": "success", "message": "Mapping saved successfully."})
	})

	api.Get("/list", func(c *fiber.Ctx) error {
		rows, err := pool.Query(`SELECT m.id, u.fullname as alloc_manager,
			COALESCE(s.state_name, 'All States') as state_name,
			COALESCE(d.district_name, 'All Districts') as district_name,
			COALESCE(ic.company_name, 'All Companies') as company_name,
			COALESCE(c.type_name, 'All Categories') as category,
			COALESCE(sc.sub_type_name, 'All Subcategories') as subcategory
			FROM master_geo_alloc_mapping m
			JOIN users u ON m.allocation_manager_id = u.id
			LEFT JOIN master_states s ON m.state_id = s.id
			LEFT JOIN master_districts d ON m.district_id = d.id
			LEFT JOIN master_insurance_companies ic ON m.insurance_company_id = ic.id
			LEFT JOIN master_case_types c ON m.category_id = c.id
			LEFT JOIN master_case_sub_types sc ON m.subcategory_id = sc.id
			ORDER BY m.id DESC`)
		if err != nil {
			return c.JSON(fiber.Map{"status": "error", "message": "Query Failed: " + err.Error()})
		}
		defer rows.Close()

		type mapItem struct {
			ID           int64  `json:"id"`
			AllocManager string `json:"alloc_manager"`
			StateName    string `json:"state_name"`
			DistrictName string `json:"district_name"`
			CompanyName  string `json:"company_name"`
			Category     string `json:"category"`
			Subcategory  string `json:"subcategory"`
		}
		list := []mapItem{}
		for rows.Next() {
			var it mapItem
			if err := rows.Scan(&it.ID, &it.AllocManager, &it.StateName, &it.DistrictName, &it.CompanyName, &it.Category, &it.Subcategory); err == nil {
				list = append(list, it)
			}
		}

		return c.JSON(fiber.Map{"status": "success", "data": list})
	})

	api.Post("/delete", func(c *fiber.Ctx) error {
		id := c.FormValue("id")
		if id == "" || id == "0" {
			return c.JSON(fiber.Map{"status": "error", "message": "Invalid ID."})
		}
		_, err := pool.Exec("DELETE FROM master_geo_alloc_mapping WHERE id = ?", id)
		if err != nil {
			return c.JSON(fiber.Map{"status": "error", "message": err.Error()})
		}
		return c.JSON(fiber.Map{"status": "success", "message": "Mapping deleted successfully."})
	})
}
