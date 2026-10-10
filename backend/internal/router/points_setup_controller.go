package router

import (
	"database/sql"
	_ "embed"
	"fmt"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"
)

//go:embed templates/users/points_setup.html
var pointsSetupHTML []byte

func RegisterPointsSetupRoutes(app *fiber.App, pool *sql.DB, requireSession fiber.Handler) {
	app.Get("/users/points_setup", requireSession, func(c *fiber.Ctx) error {
		html := string(pointsSetupHTML)
		html = strings.ReplaceAll(html, "{{SIDEBAR_PARTIAL}}", string(dashboardSidebarPartial))
		html = strings.ReplaceAll(html, "{{HEADER_PARTIAL}}", string(dashboardHeaderPartial))
		html = strings.ReplaceAll(html, "{{FOOTER_PARTIAL}}", string(dashboardFooterPartial))
		c.Type("html", "utf-8")
		return c.SendString(html)
	})

	api := app.Group("/api/points_setup", requireSession)

	api.All("/get_list", func(c *fiber.Ctx) error {
		type pointRow struct {
			ID            int      `json:"id"`
			MappedIDs     []int    `json:"mapped_ids"`
			CompanyID     int      `json:"company_id"`
			CategoryID    int      `json:"category_id"`
			SubcategoryID int      `json:"subcategory_id"`
			Points        float64  `json:"points"`
			TATHours      int      `json:"tat_hours"`
			CreatedAt     string   `json:"created_at"`
			CompanyName   string   `json:"company_name"`
			CategoryName  string   `json:"category_name"`
			Subcategory   string   `json:"subcategory_name"`
		}

		insMap := make(map[int]string)
		if rows, err := pool.Query("SELECT id, company_name FROM master_insurance_companies ORDER BY company_name ASC"); err == nil {
			defer rows.Close()
			for rows.Next() {
				var id int
				var name string
				if err := rows.Scan(&id, &name); err == nil {
					insMap[id] = strings.TrimSpace(name)
				}
			}
		}

		catMap := make(map[int]string)
		if rows, err := pool.Query("SELECT id, type_name FROM master_case_types"); err == nil {
			defer rows.Close()
			for rows.Next() {
				var id int
				var name string
				if err := rows.Scan(&id, &name); err == nil {
					catMap[id] = strings.TrimSpace(name)
				}
			}
		}

		subCatMap := make(map[int]string)
		if rows, err := pool.Query("SELECT id, sub_type_name FROM master_case_sub_types"); err == nil {
			defer rows.Close()
			for rows.Next() {
				var id int
				var name string
				if err := rows.Scan(&id, &name); err == nil {
					subCatMap[id] = strings.TrimSpace(name)
				}
			}
		}

		cfgRows, err := pool.Query("SELECT id, company_id, category_id, subcategory_id, points, COALESCE(tat_hours, 48), COALESCE(created_at, '') FROM performance_points_config ORDER BY id DESC")
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"status": "error", "message": "Error: " + err.Error()})
		}
		defer cfgRows.Close()

		resultMap := make(map[string]*pointRow)
		var orderedKeys []string

		for cfgRows.Next() {
			var id, comp, cat, subcat, tat int
			var pts float64
			var created string
			if err := cfgRows.Scan(&id, &comp, &cat, &subcat, &pts, &tat, &created); err != nil {
				continue
			}

			cName := insMap[comp]
			if cName == "" {
				cName = fmt.Sprintf("Insurer #%d", comp)
			}
			catName := catMap[cat]
			if catName == "" {
				catName = fmt.Sprintf("Type #%d", cat)
			}
			subName := subCatMap[subcat]
			if subName == "" {
				subName = fmt.Sprintf("Subtype #%d", subcat)
			}

			uniqueKey := fmt.Sprintf("%d_%s_%s", comp, catName, subName)
			if item, exists := resultMap[uniqueKey]; !exists {
				p := &pointRow{
					ID:            id,
					MappedIDs:     []int{id},
					CompanyID:     comp,
					CategoryID:    cat,
					SubcategoryID: subcat,
					Points:        pts,
					TATHours:      tat,
					CreatedAt:     created,
					CompanyName:   cName,
					CategoryName:  catName,
					Subcategory:   subName,
				}
				resultMap[uniqueKey] = p
				orderedKeys = append(orderedKeys, uniqueKey)
			} else {
				item.MappedIDs = append(item.MappedIDs, id)
			}
		}

		dataList := make([]pointRow, 0, len(orderedKeys))
		for _, k := range orderedKeys {
			if item, ok := resultMap[k]; ok {
				dataList = append(dataList, *item)
			}
		}

		return c.JSON(fiber.Map{
			"status": "success",
			"data":   dataList,
		})
	})

	api.Post("/bulk_save", func(c *fiber.Ctx) error {
		rawIDs := c.FormValue("ids")
		pointsStr := c.FormValue("points", "0.00")
		tatStr := c.FormValue("tat_hours", "48")

		points, _ := strconv.ParseFloat(pointsStr, 64)
		tatHours, _ := strconv.Atoi(tatStr)

		var ids []int
		for _, part := range strings.Split(rawIDs, ",") {
			part = strings.TrimSpace(part)
			if id, err := strconv.Atoi(part); err == nil && id > 0 {
				ids = append(ids, id)
			}
		}

		if len(ids) == 0 {
			return c.Status(400).JSON(fiber.Map{"status": "error", "message": "No valid configuration records selected."})
		}

		placeholders := make([]string, len(ids))
		args := make([]interface{}, 0, len(ids)+2)
		args = append(args, points, tatHours)
		for i, id := range ids {
			placeholders[i] = "?"
			args = append(args, id)
		}

		query := fmt.Sprintf("UPDATE performance_points_config SET points = ?, tat_hours = ? WHERE id IN (%s)", strings.Join(placeholders, ","))
		_, err := pool.Exec(query, args...)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"status": "error", "message": "Database error: " + err.Error()})
		}

		return c.JSON(fiber.Map{
			"status":  "success",
			"message": fmt.Sprintf("%d database records updated successfully in one shot!", len(ids)),
		})
	})

	api.Post("/save_single_row", func(c *fiber.Ctx) error {
		rawIDs := c.FormValue("mapped_ids")
		if rawIDs == "" {
			rawIDs = c.FormValue("id")
		}
		pointsStr := c.FormValue("points", "0.00")
		tatStr := c.FormValue("tat_hours", "48")

		points, _ := strconv.ParseFloat(pointsStr, 64)
		tatHours, _ := strconv.Atoi(tatStr)

		var ids []int
		for _, part := range strings.Split(rawIDs, ",") {
			part = strings.TrimSpace(part)
			if id, err := strconv.Atoi(part); err == nil && id > 0 {
				ids = append(ids, id)
			}
		}

		if len(ids) == 0 {
			return c.Status(400).JSON(fiber.Map{"status": "error", "message": "Invalid Record ID."})
		}

		placeholders := make([]string, len(ids))
		args := make([]interface{}, 0, len(ids)+2)
		args = append(args, points, tatHours)
		for i, id := range ids {
			placeholders[i] = "?"
			args = append(args, id)
		}

		query := fmt.Sprintf("UPDATE performance_points_config SET points = ?, tat_hours = ? WHERE id IN (%s)", strings.Join(placeholders, ","))
		_, err := pool.Exec(query, args...)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"status": "error", "message": err.Error()})
		}

		return c.JSON(fiber.Map{
			"status":  "success",
			"message": "Updated successfully!",
		})
	})
}
