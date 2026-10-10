package router

import (
	"database/sql"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

//go:embed templates/letters/manage.html
var lettersManageHTML []byte

//go:embed templates/letters/generate.html
var lettersGenerateHTML []byte

func ensureLettersTables(pool *sql.DB) {
	if pool == nil {
		return
	}
	_, _ = pool.Exec(`
		CREATE TABLE IF NOT EXISTS master_insurance_companies (
			id INT AUTO_INCREMENT PRIMARY KEY,
			company_name VARCHAR(255) DEFAULT NULL,
			insurance_company_name VARCHAR(255) DEFAULT NULL,
			status VARCHAR(50) DEFAULT 'active'
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
	`)

	_, _ = pool.Exec(`
		CREATE TABLE IF NOT EXISTS letter_templates (
			id INT AUTO_INCREMENT PRIMARY KEY,
			insurer_id INT NOT NULL,
			file_name VARCHAR(255) DEFAULT NULL,
			updated_at DATETIME DEFAULT NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
	`)

	_, _ = pool.Exec(`
		CREATE TABLE IF NOT EXISTS report_templates (
			id INT AUTO_INCREMENT PRIMARY KEY,
			insurer_id INT NOT NULL,
			file_name VARCHAR(255) DEFAULT NULL,
			updated_at DATETIME DEFAULT NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
	`)
}

func RegisterLettersRoutes(app *fiber.App, pool *sql.DB, requireSession fiber.Handler) {
	ensureLettersTables(pool)

	// GET /letters-manager/manage
	app.Get("/letters-manager/manage", requireSession, func(c *fiber.Ctx) error {
		htmlStr := string(lettersManageHTML)
		htmlStr = strings.ReplaceAll(htmlStr, "{{SIDEBAR_PARTIAL}}", string(dashboardSidebarPartial))
		htmlStr = strings.ReplaceAll(htmlStr, "{{HEADER_PARTIAL}}", string(dashboardHeaderPartial))
		htmlStr = strings.ReplaceAll(htmlStr, "{{FOOTER_PARTIAL}}", string(dashboardFooterPartial))
		c.Type("html", "utf-8")
		return c.SendString(htmlStr)
	})

	// GET /letters-manager/generate
	app.Get("/letters-manager/generate", requireSession, func(c *fiber.Ctx) error {
		htmlStr := string(lettersGenerateHTML)
		htmlStr = strings.ReplaceAll(htmlStr, "{{SIDEBAR_PARTIAL}}", string(dashboardSidebarPartial))
		htmlStr = strings.ReplaceAll(htmlStr, "{{HEADER_PARTIAL}}", string(dashboardHeaderPartial))
		htmlStr = strings.ReplaceAll(htmlStr, "{{FOOTER_PARTIAL}}", string(dashboardFooterPartial))
		c.Type("html", "utf-8")
		return c.SendString(htmlStr)
	})

	// GET /api/letters/insurers
	app.Get("/api/letters/insurers", requireSession, func(c *fiber.Ctx) error {
		type insItem struct {
			ID                 int    `json:"id"`
			CompanyName        string `json:"company_name"`
			IsConfigured       int    `json:"is_configured"`
			IsReportConfigured int    `json:"is_report_configured"`
			LetterFile         string `json:"letter_file,omitempty"`
			ReportFile         string `json:"report_file,omitempty"`
		}

		insurers := []insItem{}
		rows, err := pool.Query(`
			SELECT i.id, i.company_name, 
				   COALESCE(t.id, 0) as is_configured, 
				   COALESCE(r.id, 0) as is_report_configured,
				   COALESCE(t.file_name, ''),
				   COALESCE(r.file_name, '')
			FROM master_insurance_companies i 
			LEFT JOIN letter_templates t ON i.id = t.insurer_id 
			LEFT JOIN report_templates r ON i.id = r.insurer_id
			WHERE i.status = 'active' OR i.status IS NULL
			ORDER BY i.company_name ASC`)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var it insItem
				if err := rows.Scan(&it.ID, &it.CompanyName, &it.IsConfigured, &it.IsReportConfigured, &it.LetterFile, &it.ReportFile); err == nil {
					insurers = append(insurers, it)
				}
			}
		}

		return c.JSON(fiber.Map{"status": "success", "insurers": insurers})
	})

	// POST /api/letters/save
	app.Post("/api/letters/save", requireSession, func(c *fiber.Ctx) error {
		insurerId, _ := strconv.Atoi(c.FormValue("insurer_id"))
		if insurerId <= 0 {
			return c.JSON(fiber.Map{"status": "error", "message": "Invalid Insurer Selected"})
		}

		fileHeader, err := c.FormFile("template_file")
		if err != nil {
			return c.JSON(fiber.Map{"status": "error", "message": "Please select a MS Word (.docx) file to upload."})
		}

		if !strings.HasSuffix(strings.ToLower(fileHeader.Filename), ".docx") {
			return c.JSON(fiber.Map{"status": "error", "message": "Invalid Format! Only .docx MS Word files are allowed."})
		}

		uploadDir := "./public/uploads/templates"
		_ = os.MkdirAll(uploadDir, 0755)

		fileName := fmt.Sprintf("template_ins_%d_%d.docx", insurerId, time.Now().Unix())
		dest := filepath.Join(uploadDir, fileName)
		if err := c.SaveFile(fileHeader, dest); err != nil {
			return c.JSON(fiber.Map{"status": "error", "message": "Failed to save file: " + err.Error()})
		}

		var exists int
		_ = pool.QueryRow("SELECT id FROM letter_templates WHERE insurer_id = ?", insurerId).Scan(&exists)
		if exists > 0 {
			_, _ = pool.Exec("UPDATE letter_templates SET file_name = ?, updated_at = NOW() WHERE id = ?", fileName, exists)
		} else {
			_, _ = pool.Exec("INSERT INTO letter_templates (insurer_id, file_name, updated_at) VALUES (?, ?, NOW())", insurerId, fileName)
		}

		return c.JSON(fiber.Map{"status": "success", "message": "Letter Template uploaded and linked successfully!"})
	})

	// POST /api/reports_manager/save
	app.Post("/api/reports_manager/save", requireSession, func(c *fiber.Ctx) error {
		insurerId, _ := strconv.Atoi(c.FormValue("insurer_id"))
		if insurerId <= 0 {
			return c.JSON(fiber.Map{"status": "error", "message": "Invalid Insurer Selected"})
		}

		fileHeader, err := c.FormFile("template_file")
		if err != nil {
			return c.JSON(fiber.Map{"status": "error", "message": "Please select a MS Word (.docx) file to upload."})
		}

		if !strings.HasSuffix(strings.ToLower(fileHeader.Filename), ".docx") {
			return c.JSON(fiber.Map{"status": "error", "message": "Invalid Format! Only .docx MS Word files are allowed."})
		}

		uploadDir := "./public/uploads/report_templates"
		_ = os.MkdirAll(uploadDir, 0755)

		fileName := fmt.Sprintf("report_tmpl_ins_%d_%d.docx", insurerId, time.Now().Unix())
		dest := filepath.Join(uploadDir, fileName)
		if err := c.SaveFile(fileHeader, dest); err != nil {
			return c.JSON(fiber.Map{"status": "error", "message": "Failed to save file: " + err.Error()})
		}

		var exists int
		_ = pool.QueryRow("SELECT id FROM report_templates WHERE insurer_id = ?", insurerId).Scan(&exists)
		if exists > 0 {
			_, _ = pool.Exec("UPDATE report_templates SET file_name = ?, updated_at = NOW() WHERE id = ?", fileName, exists)
		} else {
			_, _ = pool.Exec("INSERT INTO report_templates (insurer_id, file_name, updated_at) VALUES (?, ?, NOW())", insurerId, fileName)
		}

		return c.JSON(fiber.Map{"status": "success", "message": "Report Template uploaded and linked successfully!"})
	})

	// GET/POST /api/letters/fetch-case-details
	fetchCaseDetails := func(c *fiber.Ctx) error {
		claimNo := strings.TrimSpace(c.Query("claim_no"))
		if claimNo == "" {
			claimNo = strings.TrimSpace(c.FormValue("claim_no"))
		}
		caseId, _ := strconv.Atoi(c.Query("case_id"))
		if caseId <= 0 {
			caseId, _ = strconv.Atoi(c.FormValue("case_id"))
		}
		insurerId, _ := strconv.Atoi(c.Query("insurer_id"))
		if insurerId <= 0 {
			insurerId, _ = strconv.Atoi(c.FormValue("insurer_id"))
		}

		if caseId <= 0 && (claimNo == "" || insurerId <= 0) {
			return c.JSON(fiber.Map{"status": "error", "message": "Please select Insurance Company and enter Claim Number"})
		}

		var query string
		var param interface{}
		if caseId > 0 {
			query = `
				SELECT c.id, c.claim_number, COALESCE(c.policy_no, ''), COALESCE(c.patient_name, ''),
					   COALESCE(c.patient_age, 0), COALESCE(c.hospital_name, ''), COALESCE(c.hospital_address, ''),
					   COALESCE(d.district_name, ''), COALESCE(s.state_name, ''),
					   COALESCE(c.insurance_company_id, 0),
					   COALESCE(DATE_FORMAT(c.doa, '%Y-%m-%d'), ''),
					   COALESCE(DATE_FORMAT(c.dod, '%Y-%m-%d'), ''),
					   COALESCE(fo.fullname, '')
				FROM cases c
				LEFT JOIN master_states s ON c.state_id = s.id
				LEFT JOIN master_districts d ON c.city_id = d.id
				LEFT JOIN users fo ON c.fo_id = fo.id
				WHERE c.id = ? LIMIT 1`
			param = caseId
		} else {
			query = `
				SELECT c.id, c.claim_number, COALESCE(c.policy_no, ''), COALESCE(c.patient_name, ''),
					   COALESCE(c.patient_age, 0), COALESCE(c.hospital_name, ''), COALESCE(c.hospital_address, ''),
					   COALESCE(d.district_name, ''), COALESCE(s.state_name, ''),
					   COALESCE(c.insurance_company_id, 0),
					   COALESCE(DATE_FORMAT(c.doa, '%Y-%m-%d'), ''),
					   COALESCE(DATE_FORMAT(c.dod, '%Y-%m-%d'), ''),
					   COALESCE(fo.fullname, '')
				FROM cases c
				LEFT JOIN master_states s ON c.state_id = s.id
				LEFT JOIN master_districts d ON c.city_id = d.id
				LEFT JOIN users fo ON c.fo_id = fo.id
				WHERE c.claim_number = ? AND c.insurance_company_id = ? LIMIT 1`
		}

		var cid, age, insCompId int
		var clm, pol, pat, hosp, hospAddr, city, state, doa, dod, foName string

		var err error
		if caseId > 0 {
			err = pool.QueryRow(query, param).Scan(&cid, &clm, &pol, &pat, &age, &hosp, &hospAddr, &city, &state, &insCompId, &doa, &dod, &foName)
		} else {
			err = pool.QueryRow(query, claimNo, insurerId).Scan(&cid, &clm, &pol, &pat, &age, &hosp, &hospAddr, &city, &state, &insCompId, &doa, &dod, &foName)
		}

		if err != nil {
			return c.JSON(fiber.Map{"status": "error", "message": "Matching case record not found for the selected Insurance Company and Claim Number."})
		}

		return c.JSON(fiber.Map{
			"status": "success",
			"data": fiber.Map{
				"case_id":              cid,
				"claim_no":             clm,
				"policy_no":            pol,
				"patient_name":         pat,
				"patient_age":          age,
				"hospital_name":        hosp,
				"hospital_address":     hospAddr,
				"hospital_city":        city,
				"hospital_state":       state,
				"insurance_company_id": insCompId,
				"doa":                  doa,
				"dod":                  dod,
				"fo_name":              foName,
			},
		})
	}
	app.Get("/api/letters/fetch-case-details", requireSession, fetchCaseDetails)
	app.Post("/api/letters/fetch-case-details", requireSession, fetchCaseDetails)

	// GET/POST /api/letters/download-document
	downloadDocument := func(c *fiber.Ctx) error {
		claimNo := strings.TrimSpace(c.Query("claim_no"))
		if claimNo == "" {
			claimNo = strings.TrimSpace(c.FormValue("claim_no"))
		}
		insurerId, _ := strconv.Atoi(c.Query("insurer_id"))
		if insurerId <= 0 {
			insurerId, _ = strconv.Atoi(c.FormValue("insurer_id"))
		}

		var tmplFile string
		_ = pool.QueryRow("SELECT file_name FROM letter_templates WHERE insurer_id = ?", insurerId).Scan(&tmplFile)

		if tmplFile != "" {
			filePath := filepath.Join("./public/uploads/templates", tmplFile)
			if _, err := os.Stat(filePath); err == nil {
				return c.Download(filePath, fmt.Sprintf("Authorization_Letter_%s.docx", claimNo))
			}
		}

		// Fallback sample docx / notice
		c.Set("Content-Type", "text/plain")
		return c.SendString(fmt.Sprintf("Authorization Letter generated for Claim: %s (Insurer ID: %d)", claimNo, insurerId))
	}
	app.Get("/api/letters/download-document", requireSession, downloadDocument)
	app.Post("/api/letters/download-document", requireSession, downloadDocument)
}
