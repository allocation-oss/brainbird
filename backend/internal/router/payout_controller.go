package router

import (
	"bytes"
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

//go:embed templates/payout/verification.html
var payoutVerificationHTML []byte

func ensurePayoutTables(pool *sql.DB) {
	if pool == nil {
		return
	}
	_, _ = pool.Exec(`
		CREATE TABLE IF NOT EXISTS payout_imports (
			id INT AUTO_INCREMENT PRIMARY KEY,
			file_name VARCHAR(255) NOT NULL,
			uploaded_by INT DEFAULT NULL,
			uploaded_by_name VARCHAR(150) DEFAULT NULL,
			total_rows INT NOT NULL DEFAULT 0,
			verified_count INT NOT NULL DEFAULT 0,
			suspicious_count INT NOT NULL DEFAULT 0,
			excel_duplicate_count INT NOT NULL DEFAULT 0,
			db_duplicate_count INT NOT NULL DEFAULT 0,
			not_found_count INT NOT NULL DEFAULT 0,
			withdrawn_count INT NOT NULL DEFAULT 0,
			rejected_count INT NOT NULL DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
	`)

	_, _ = pool.Exec(`
		CREATE TABLE IF NOT EXISTS payout_records (
			id INT AUTO_INCREMENT PRIMARY KEY,
			import_id INT NOT NULL,
			claim_number VARCHAR(120) NOT NULL,
			case_id INT DEFAULT NULL,
			amount DECIMAL(12,2) NOT NULL DEFAULT 0.00,
			paid_date DATE DEFAULT NULL,
			remark VARCHAR(500) DEFAULT NULL,
			status VARCHAR(40) NOT NULL DEFAULT 'VERIFIED',
			patient_name VARCHAR(200) DEFAULT NULL,
			hospital_name VARCHAR(255) DEFAULT NULL,
			assigned_user VARCHAR(150) DEFAULT NULL,
			fo_parts_count INT DEFAULT 0,
			fo_parts_done VARCHAR(255) DEFAULT NULL,
			is_on_role TINYINT(1) DEFAULT 0,
			employment_type VARCHAR(40) DEFAULT NULL,
			flags_json TEXT DEFAULT NULL,
			on_role_parts VARCHAR(500) DEFAULT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE KEY uniq_claim_number (claim_number)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
	`)

	_, _ = pool.Exec(`
		CREATE TABLE IF NOT EXISTS payout_rate_config (
			id INT AUTO_INCREMENT PRIMARY KEY,
			insurance_company_id INT NOT NULL,
			case_type_id INT NOT NULL,
			case_sub_type_id INT NOT NULL DEFAULT 0,
			user_id INT DEFAULT NULL,
			part_code VARCHAR(20) NOT NULL DEFAULT 'ALL',
			rate_amount DECIMAL(12,2) NOT NULL DEFAULT 0.00,
			status ENUM('active','inactive') DEFAULT 'active',
			created_by INT DEFAULT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			UNIQUE KEY uniq_rate (insurance_company_id, case_type_id, case_sub_type_id, user_id, part_code)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
	`)

	_, _ = pool.Exec(`
		CREATE TABLE IF NOT EXISTS payout_import_rows (
			id INT AUTO_INCREMENT PRIMARY KEY,
			import_id INT NOT NULL,
			row_no INT NOT NULL DEFAULT 0,
			claim_number VARCHAR(120) NOT NULL,
			case_id INT DEFAULT NULL,
			amount DECIMAL(12,2) NOT NULL DEFAULT 0.00,
			paid_date DATE DEFAULT NULL,
			remark VARCHAR(500) DEFAULT NULL,
			status VARCHAR(40) NOT NULL DEFAULT 'VERIFIED_READY',
			patient_name VARCHAR(200) DEFAULT NULL,
			hospital_name VARCHAR(255) DEFAULT NULL,
			assigned_user VARCHAR(150) DEFAULT NULL,
			expected_fo_id INT DEFAULT NULL,
			employment_type VARCHAR(40) DEFAULT NULL,
			fo_parts_count INT DEFAULT 0,
			fo_parts_done VARCHAR(255) DEFAULT NULL,
			part_detail TEXT DEFAULT NULL,
			on_role_parts VARCHAR(500) DEFAULT NULL,
			prev_paid_amount DECIMAL(12,2) DEFAULT NULL,
			prev_paid_on VARCHAR(40) DEFAULT NULL,
			prev_paid_month VARCHAR(20) DEFAULT NULL,
			flags_json TEXT DEFAULT NULL,
			prev_import_id INT DEFAULT NULL,
			prev_file VARCHAR(255) DEFAULT NULL,
			prev_uploaded_by VARCHAR(150) DEFAULT NULL,
			prev_assigned VARCHAR(255) DEFAULT NULL,
			dup_note VARCHAR(1000) DEFAULT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			KEY idx_import_id (import_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
	`)

	_, _ = pool.Exec(`
		CREATE TABLE IF NOT EXISTS payout_record_parts (
			id INT AUTO_INCREMENT PRIMARY KEY,
			claim_number VARCHAR(120) NOT NULL,
			import_id INT DEFAULT NULL,
			case_id INT DEFAULT NULL,
			part_code VARCHAR(20) NOT NULL DEFAULT 'ALL',
			user_id INT DEFAULT NULL,
			user_name VARCHAR(150) DEFAULT NULL,
			is_on_role TINYINT(1) NOT NULL DEFAULT 0,
			amount DECIMAL(12,2) NOT NULL DEFAULT 0.00,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			KEY idx_claim (claim_number),
			KEY idx_import (import_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
	`)
}

func RegisterPayoutRoutes(app *fiber.App, pool *sql.DB, requireSession fiber.Handler) {
	ensurePayoutTables(pool)

	// HTML View
	app.Get("/payout/verification", requireSession, func(c *fiber.Ctx) error {
		htmlStr := string(payoutVerificationHTML)
		htmlStr = strings.ReplaceAll(htmlStr, "{{SIDEBAR_PARTIAL}}", string(dashboardSidebarPartial))
		htmlStr = strings.ReplaceAll(htmlStr, "{{HEADER_PARTIAL}}", string(dashboardHeaderPartial))
		htmlStr = strings.ReplaceAll(htmlStr, "{{FOOTER_PARTIAL}}", string(dashboardFooterPartial))
		c.Type("html", "utf-8")
		return c.SendString(htmlStr)
	})

	// Download sample CSV
	app.Get("/api/payout/sample", requireSession, func(c *fiber.Ctx) error {
		csvData := "Claim Number,Amount,Paid Date,Remark\nCLM-100201,1250.00,2026-03-15,Regular Investigation Payout\nCLM-100202,800.00,2026-03-15,Hospital Part Only\nCLM-100203,1500.00,2026-03-16,Full Case Comprehensive\n"
		c.Set("Content-Type", "text/csv; charset=utf-8")
		c.Set("Content-Disposition", "attachment; filename=\"payout_verification_sample.csv\"")
		return c.SendString(csvData)
	})

	// GET /api/payout/history
	app.Get("/api/payout/history", requireSession, func(c *fiber.Ctx) error {
		type importItem struct {
			ID                  int    `json:"id"`
			FileName            string `json:"file_name"`
			UploadedByName      string `json:"uploaded_by_name"`
			TotalRows           int    `json:"total_rows"`
			VerifiedCount       int    `json:"verified_count"`
			SuspiciousCount     int    `json:"suspicious_count"`
			ExcelDuplicateCount int    `json:"excel_duplicate_count"`
			DbDuplicateCount    int    `json:"db_duplicate_count"`
			NotFoundCount       int    `json:"not_found_count"`
			WithdrawnCount      int    `json:"withdrawn_count"`
			RejectedCount       int    `json:"rejected_count"`
			CreatedAt           string `json:"created_at"`
		}

		list := []importItem{}
		if pool == nil {
			return c.JSON(fiber.Map{"status": "success", "data": list})
		}
		rows, err := pool.Query(`
			SELECT id, file_name, COALESCE(uploaded_by_name, 'System'), total_rows, verified_count,
				   COALESCE(suspicious_count,0), excel_duplicate_count, db_duplicate_count,
				   COALESCE(not_found_count,0), COALESCE(withdrawn_count,0), COALESCE(rejected_count,0),
				   DATE_FORMAT(created_at, '%d-%b-%Y %h:%i %p')
			FROM payout_imports
			ORDER BY id DESC LIMIT 100`)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var it importItem
				if err := rows.Scan(&it.ID, &it.FileName, &it.UploadedByName, &it.TotalRows, &it.VerifiedCount,
					&it.SuspiciousCount, &it.ExcelDuplicateCount, &it.DbDuplicateCount,
					&it.NotFoundCount, &it.WithdrawnCount, &it.RejectedCount, &it.CreatedAt); err == nil {
					list = append(list, it)
				}
			}
		}

		return c.JSON(fiber.Map{"status": "success", "data": list})
	})

	// GET /api/payout/import/:id
	app.Get("/api/payout/import/:id", requireSession, func(c *fiber.Ctx) error {
		id, _ := strconv.Atoi(c.Params("id"))
		if id <= 0 {
			return c.JSON(fiber.Map{"status": "error", "message": "Invalid import id"})
		}

		type importDetail struct {
			ID        int    `json:"id"`
			FileName  string `json:"file_name"`
			TotalRows int    `json:"total_rows"`
			Verified  int    `json:"verified_count"`
		}
		var imp importDetail
		err := pool.QueryRow("SELECT id, file_name, total_rows, verified_count FROM payout_imports WHERE id = ?", id).Scan(&imp.ID, &imp.FileName, &imp.TotalRows, &imp.Verified)
		if err != nil {
			return c.JSON(fiber.Map{"status": "error", "message": "Import not found"})
		}

		rows, err := pool.Query(`
			SELECT id, row_no, claim_number, COALESCE(case_id, 0), amount, COALESCE(DATE_FORMAT(paid_date, '%Y-%m-%d'), ''),
				   COALESCE(remark, ''), status, COALESCE(patient_name, '—'), COALESCE(hospital_name, '—'),
				   COALESCE(assigned_user, '—'), COALESCE(fo_parts_count, 0), COALESCE(fo_parts_done, ''),
				   COALESCE(employment_type, ''), COALESCE(on_role_parts, ''), COALESCE(dup_note, '')
			FROM payout_import_rows
			WHERE import_id = ?
			ORDER BY row_no ASC LIMIT 2000`, id)

		records := []fiber.Map{}
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var rid, rowNo, caseId, foParts int
				var claimNo, paidDate, remark, status, patientName, hospName, assigned, empType, onRole, dupNote string
				var amt float64

				if err := rows.Scan(&rid, &rowNo, &claimNo, &caseId, &amt, &paidDate, &remark, &status, &patientName, &hospName, &assigned, &foParts, &empType, &onRole, &dupNote, &dupNote); err == nil {
					records = append(records, fiber.Map{
						"id":              rid,
						"row_no":          rowNo,
						"claim_number":    claimNo,
						"case_id":         caseId,
						"amount":          amt,
						"paid_date":       paidDate,
						"remark":          remark,
						"status":          status,
						"patient_name":    patientName,
						"hospital_name":   hospName,
						"assigned_user":   assigned,
						"fo_parts_count":  foParts,
						"employment_type": empType,
						"on_role_parts":   onRole,
						"dup_note":        dupNote,
					})
				}
			}
		}

		return c.JSON(fiber.Map{
			"status":  "success",
			"import":  imp,
			"records": records,
		})
	})

	// GET /api/payout/import/:id/export
	app.Get("/api/payout/import/:id/export", requireSession, func(c *fiber.Ctx) error {
		id, _ := strconv.Atoi(c.Params("id"))
		csvBuf := bytes.NewBuffer(nil)
		writer := csv.NewWriter(csvBuf)
		_ = writer.Write([]string{"Row #", "Claim Number", "Status", "Amount", "Patient Name", "Hospital Name", "Assigned User", "Paid Date", "Remark"})

		rows, err := pool.Query(`
			SELECT row_no, claim_number, status, amount, COALESCE(patient_name, ''), COALESCE(hospital_name, ''), COALESCE(assigned_user, ''), COALESCE(paid_date, ''), COALESCE(remark, '')
			FROM payout_import_rows WHERE import_id = ? ORDER BY row_no ASC`, id)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var rNo int
				var clm, st, pt, hp, usr, dt, rem string
				var amt float64
				if err := rows.Scan(&rNo, &clm, &st, &amt, &pt, &hp, &usr, &dt, &rem); err == nil {
					_ = writer.Write([]string{strconv.Itoa(rNo), clm, st, fmt.Sprintf("%.2f", amt), pt, hp, usr, dt, rem})
				}
			}
		}
		writer.Flush()

		c.Set("Content-Type", "text/csv; charset=utf-8")
		c.Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"payout_import_%d.csv\"", id))
		return c.Send(csvBuf.Bytes())
	})

	// GET /api/payout/records
	app.Get("/api/payout/records", requireSession, func(c *fiber.Ctx) error {
		month := strings.TrimSpace(c.Query("month"))
		summaryOnly := c.Query("summary") == "1"

		type monthStat struct {
			Key    string  `json:"key"`
			Label  string  `json:"label"`
			Count  int     `json:"count"`
			Amount float64 `json:"amount"`
		}

		months := []monthStat{}
		mRows, err := pool.Query(`
			SELECT DATE_FORMAT(created_at, '%Y-%m') AS mk, COUNT(*) AS cnt, COALESCE(SUM(amount),0) AS amt
			FROM payout_records
			GROUP BY DATE_FORMAT(created_at, '%Y-%m')
			ORDER BY mk DESC`)
		totalCnt := 0
		totalAmt := 0.0
		if err == nil {
			defer mRows.Close()
			for mRows.Next() {
				var mk string
				var cnt int
				var amt float64
				if err := mRows.Scan(&mk, &cnt, &amt); err == nil {
					t, _ := time.Parse("2006-01", mk)
					lbl := mk
					if !t.IsZero() {
						lbl = t.Format("Jan 2006")
					}
					months = append(months, monthStat{Key: mk, Label: lbl, Count: cnt, Amount: amt})
					totalCnt += cnt
					totalAmt += amt
				}
			}
		}

		if summaryOnly {
			return c.JSON(fiber.Map{
				"status":       "success",
				"months":       months,
				"total_count":  totalCnt,
				"total_amount": math.Round(totalAmt*100) / 100,
			})
		}

		if month == "latest" && len(months) > 0 {
			month = months[0].Key
		}

		whereCond := "1=1"
		var params []interface{}
		if month != "" {
			whereCond += " AND DATE_FORMAT(pr.created_at, '%Y-%m') = ?"
			params = append(params, month)
		}

		q := fmt.Sprintf(`
			SELECT pr.id, pr.claim_number, COALESCE(pr.case_id, 0), pr.amount, COALESCE(DATE_FORMAT(pr.paid_date, '%%Y-%%m-%%d'), ''),
				   COALESCE(pr.remark, ''), pr.status, COALESCE(pr.patient_name, '—'), COALESCE(pr.hospital_name, '—'),
				   COALESCE(pr.assigned_user, '—'), COALESCE(pr.fo_parts_count, 0), COALESCE(pr.fo_parts_done, ''),
				   COALESCE(pr.employment_type, ''), COALESCE(pr.on_role_parts, ''), DATE_FORMAT(pr.created_at, '%%d-%%b-%%Y %%h:%%i %%p'),
				   COALESCE(pi.file_name, '—')
			FROM payout_records pr
			LEFT JOIN payout_imports pi ON pi.id = pr.import_id
			WHERE %s
			ORDER BY pr.created_at DESC, pr.id DESC LIMIT 2000`, whereCond)

		records := []fiber.Map{}
		rRows, err := pool.Query(q, params...)
		if err == nil {
			defer rRows.Close()
			for rRows.Next() {
				var id, caseId, foParts int
				var claimNo, paidDate, remark, status, patientName, hospName, assigned, foDone, empType, onRole, savedOn, importFile string
				var amt float64

				if err := rRows.Scan(&id, &claimNo, &caseId, &amt, &paidDate, &remark, &status, &patientName, &hospName, &assigned, &foParts, &foDone, &empType, &onRole, &savedOn, &importFile); err == nil {
					records = append(records, fiber.Map{
						"id":              id,
						"claim_number":    claimNo,
						"case_id":         caseId,
						"amount":          amt,
						"paid_date":       paidDate,
						"remark":          remark,
						"status":          status,
						"patient_name":    patientName,
						"hospital_name":   hospName,
						"assigned_user":   assigned,
						"fo_parts_count":  foParts,
						"fo_parts_done":   foDone,
						"employment_type": empType,
						"on_role_parts":   onRole,
						"saved_on":        savedOn,
						"import_file":     importFile,
					})
				}
			}
		}

		return c.JSON(fiber.Map{
			"status":       "success",
			"month":        month,
			"months":       months,
			"rows":         records,
			"total_count":  totalCnt,
			"total_amount": math.Round(totalAmt*100) / 100,
		})
	})

	// GET /api/payout/rate-masters
	app.Get("/api/payout/rate-masters", requireSession, func(c *fiber.Ctx) error {
		type masterItem struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		}

		insurances := []masterItem{}
		types := []masterItem{}
		subtypes := []masterItem{}
		freelancers := []masterItem{}

		if pool == nil {
			return c.JSON(fiber.Map{
				"status":      "success",
				"insurance":   insurances,
				"types":       types,
				"subtypes":    subtypes,
				"freelancers": freelancers,
			})
		}

		iRows, err := pool.Query("SELECT id, company_name FROM master_insurance_companies WHERE status = 'active' ORDER BY company_name ASC")
		if err == nil {
			defer iRows.Close()
			for iRows.Next() {
				var it masterItem
				if err := iRows.Scan(&it.ID, &it.Name); err == nil {
					insurances = append(insurances, it)
				}
			}
		}

		tRows, err := pool.Query("SELECT id, type_name FROM master_case_types WHERE status = 'active' ORDER BY type_name ASC")
		if err == nil {
			defer tRows.Close()
			for tRows.Next() {
				var it masterItem
				if err := tRows.Scan(&it.ID, &it.Name); err == nil {
					types = append(types, it)
				}
			}
		}

		sRows, err := pool.Query("SELECT id, sub_type_name FROM master_case_sub_types WHERE status = 'active' ORDER BY sub_type_name ASC")
		if err == nil {
			defer sRows.Close()
			for sRows.Next() {
				var it masterItem
				if err := sRows.Scan(&it.ID, &it.Name); err == nil {
					subtypes = append(subtypes, it)
				}
			}
		}

		fRows, err := pool.Query("SELECT id, fullname FROM users WHERE status = 'active' ORDER BY fullname ASC")
		if err == nil {
			defer fRows.Close()
			for fRows.Next() {
				var it masterItem
				if err := fRows.Scan(&it.ID, &it.Name); err == nil {
					freelancers = append(freelancers, it)
				}
			}
		}

		return c.JSON(fiber.Map{
			"status":      "success",
			"insurance":   insurances,
			"types":       types,
			"subtypes":    subtypes,
			"freelancers": freelancers,
		})
	})

	// GET /api/payout/rates
	app.Get("/api/payout/rates", requireSession, func(c *fiber.Ctx) error {
		rates := []fiber.Map{}
		if pool == nil {
			return c.JSON(fiber.Map{"status": "success", "rates": rates})
		}
		rows, err := pool.Query(`
			SELECT r.id, r.insurance_company_id, r.case_type_id, r.case_sub_type_id, r.user_id,
				   COALESCE(NULLIF(r.part_code,''), 'ALL') AS part_code, r.rate_amount, r.status,
				   COALESCE(i.company_name, 'All Insurers') AS insurance_name,
				   COALESCE(t.type_name, 'All Types') AS type_name,
				   COALESCE(s.sub_type_name, '— All —') AS sub_type_name,
				   COALESCE(u.fullname, 'ALL FREELANCERS') AS user_name
			FROM payout_rate_config r
			LEFT JOIN master_insurance_companies i ON i.id = r.insurance_company_id
			LEFT JOIN master_case_types t ON t.id = r.case_type_id
			LEFT JOIN master_case_sub_types s ON s.id = r.case_sub_type_id AND r.case_sub_type_id > 0
			LEFT JOIN users u ON u.id = r.user_id
			ORDER BY i.company_name, t.type_name, r.user_id IS NOT NULL, u.fullname
			LIMIT 500`)

		rates = []fiber.Map{}
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var id, insId, typeId, subId int
				var uId sql.NullInt64
				var partCode, status, insName, typeName, subName, userName string
				var rateAmt float64

				if err := rows.Scan(&id, &insId, &typeId, &subId, &uId, &partCode, &rateAmt, &status, &insName, &typeName, &subName, &userName); err == nil {
					var uidVal interface{} = nil
					if uId.Valid {
						uidVal = int(uId.Int64)
					}
					rates = append(rates, fiber.Map{
						"id":                   id,
						"insurance_company_id": insId,
						"case_type_id":         typeId,
						"case_sub_type_id":     subId,
						"user_id":              uidVal,
						"part_code":            partCode,
						"rate_amount":          rateAmt,
						"status":               status,
						"insurance_name":       insName,
						"type_name":            typeName,
						"sub_type_name":        subName,
						"user_name":            userName,
					})
				}
			}
		}

		return c.JSON(fiber.Map{"status": "success", "data": rates})
	})

	// POST /api/payout/rates (Save / Update)
	app.Post("/api/payout/rates", requireSession, func(c *fiber.Ctx) error {
		id, _ := strconv.Atoi(c.FormValue("id"))
		ins, _ := strconv.Atoi(c.FormValue("insurance_company_id"))
		typeId, _ := strconv.Atoi(c.FormValue("case_type_id"))
		subId, _ := strconv.Atoi(c.FormValue("case_sub_type_id"))
		scope := c.FormValue("scope", "all")
		targetUser, _ := strconv.Atoi(c.FormValue("user_id"))
		amount, _ := strconv.ParseFloat(c.FormValue("rate_amount"), 64)
		partCode := strings.ToUpper(strings.TrimSpace(c.FormValue("part_code", "ALL")))

		if ins <= 0 || typeId <= 0 {
			return c.JSON(fiber.Map{"status": "error", "message": "Insurance and Case Type are required."})
		}
		if amount <= 0 {
			return c.JSON(fiber.Map{"status": "error", "message": "Rate amount must be greater than 0."})
		}

		var uid interface{} = nil
		if scope == "individual" && targetUser > 0 {
			uid = targetUser
		}

		if id > 0 {
			_, err := pool.Exec(`
				UPDATE payout_rate_config 
				SET insurance_company_id=?, case_type_id=?, case_sub_type_id=?, user_id=?, part_code=?, rate_amount=?, status='active' 
				WHERE id=?`, ins, typeId, subId, uid, partCode, amount, id)
			if err != nil {
				return c.Status(500).JSON(fiber.Map{"status": "error", "message": err.Error()})
			}
		} else {
			_, err := pool.Exec(`
				INSERT INTO payout_rate_config (insurance_company_id, case_type_id, case_sub_type_id, user_id, part_code, rate_amount, status) 
				VALUES (?, ?, ?, ?, ?, ?, 'active') 
				ON DUPLICATE KEY UPDATE rate_amount = VALUES(rate_amount), status = 'active'`, ins, typeId, subId, uid, partCode, amount)
			if err != nil {
				return c.Status(500).JSON(fiber.Map{"status": "error", "message": err.Error()})
			}
		}

		return c.JSON(fiber.Map{"status": "success", "message": "Rate configured successfully."})
	})

	// DELETE /api/payout/rates/:id
	deleteRateHandler := func(c *fiber.Ctx) error {
		id, _ := strconv.Atoi(c.Params("id"))
		if id <= 0 {
			return c.JSON(fiber.Map{"status": "error", "message": "Invalid rate identifier."})
		}
		_, _ = pool.Exec("DELETE FROM payout_rate_config WHERE id = ?", id)
		return c.JSON(fiber.Map{"status": "success", "message": "Rate record deleted successfully."})
	}
	app.Delete("/api/payout/rates/:id", requireSession, deleteRateHandler)
	app.Post("/api/payout/rates/:id", requireSession, deleteRateHandler)

	// POST /api/payout/verify (Upload & Verification)
	app.Post("/api/payout/verify", requireSession, func(c *fiber.Ctx) error {
		fileHeader, err := c.FormFile("payout_file")
		if err != nil {
			return c.JSON(fiber.Map{"status": "error", "message": "Please select a valid CSV or spreadsheet file."})
		}

		file, err := fileHeader.Open()
		if err != nil {
			return c.JSON(fiber.Map{"status": "error", "message": "Could not read uploaded file."})
		}
		defer file.Close()

		save := c.FormValue("save") == "1"
		targetFoId, _ := strconv.Atoi(c.FormValue("upload_fo_id"))

		reader := csv.NewReader(file)
		reader.LazyQuotes = true
		reader.TrimLeadingSpace = true

		rawRows, err := reader.ReadAll()
		if err != nil || len(rawRows) < 2 {
			return c.JSON(fiber.Map{"status": "error", "message": "File is empty or could not parse CSV rows."})
		}

		headers := rawRows[0]
		claimCol := -1
		amtCol := -1
		dateCol := -1
		remCol := -1

		for i, h := range headers {
			hClean := strings.ToLower(strings.TrimSpace(h))
			if strings.Contains(hClean, "claim") {
				claimCol = i
			} else if strings.Contains(hClean, "amount") || strings.Contains(hClean, "payout") || strings.Contains(hClean, "rate") {
				amtCol = i
			} else if strings.Contains(hClean, "date") {
				dateCol = i
			} else if strings.Contains(hClean, "remark") || strings.Contains(hClean, "note") {
				remCol = i
			}
		}

		if claimCol == -1 {
			claimCol = 0
		}

		type verifiedRow struct {
			RowNo          int      `json:"row_no"`
			ClaimNumber    string   `json:"claim_number"`
			CaseID         int      `json:"case_id"`
			Amount         float64  `json:"amount"`
			PaidDate       string   `json:"paid_date"`
			Remark         string   `json:"remark"`
			Status         string   `json:"status"`
			PatientName    string   `json:"patient_name"`
			HospitalName   string   `json:"hospital_name"`
			AssignedUser   string   `json:"assigned_user"`
			FoPartsCount   int      `json:"fo_parts_count"`
			FoPartsDone    string   `json:"fo_parts_done"`
			RateDetail     string   `json:"rate_parts_detail"`
			OnRoleParts    string   `json:"on_role_parts"`
			EmploymentType string   `json:"employment_type"`
			Flags          []string `json:"flags"`
			IsOnRole       bool     `json:"is_on_role"`
		}

		results := []verifiedRow{}
		counts := fiber.Map{
			"total":           len(rawRows) - 1,
			"verified":        0,
			"suspicious":      0,
			"excel_duplicate": 0,
			"db_duplicate":    0,
			"not_found":       0,
			"withdrawn":       0,
			"rejected":        0,
		}

		seenInExcel := make(map[string]bool)

		for rowIdx := 1; rowIdx < len(rawRows); rowIdx++ {
			row := rawRows[rowIdx]
			if len(row) <= claimCol {
				continue
			}
			clm := strings.TrimSpace(row[claimCol])
			if clm == "" {
				continue
			}

			userAmt := 0.0
			if amtCol >= 0 && len(row) > amtCol {
				userAmt, _ = strconv.ParseFloat(strings.TrimSpace(row[amtCol]), 64)
			}
			paidDt := time.Now().Format("2006-01-02")
			if dateCol >= 0 && len(row) > dateCol {
				pRaw := strings.TrimSpace(row[dateCol])
				if pRaw != "" {
					paidDt = pRaw
				}
			}
			remk := ""
			if remCol >= 0 && len(row) > remCol {
				remk = strings.TrimSpace(row[remCol])
			}

			// Check Excel duplicates
			if seenInExcel[clm] {
				counts["excel_duplicate"] = counts["excel_duplicate"].(int) + 1
				results = append(results, verifiedRow{
					RowNo:        rowIdx,
					ClaimNumber:  clm,
					Amount:       userAmt,
					PaidDate:     paidDt,
					Remark:       "Duplicate in this uploaded spreadsheet",
					Status:       "EXCEL_DUPLICATE",
					PatientName:  "—",
					HospitalName: "—",
					AssignedUser: "—",
				})
				continue
			}
			seenInExcel[clm] = true

			// Check DB duplicate
			var prevId int
			_ = pool.QueryRow("SELECT id FROM payout_records WHERE claim_number = ?", clm).Scan(&prevId)
			if prevId > 0 {
				counts["db_duplicate"] = counts["db_duplicate"].(int) + 1
				results = append(results, verifiedRow{
					RowNo:        rowIdx,
					ClaimNumber:  clm,
					Amount:       userAmt,
					PaidDate:     paidDt,
					Remark:       "Already paid in database record #" + strconv.Itoa(prevId),
					Status:       "DB_DUPLICATE",
					PatientName:  "—",
					HospitalName: "—",
					AssignedUser: "—",
				})
				continue
			}

			// Lookup in cases table
			var caseId int
			var patName, hospName, rawStat sql.NullString
			var insId, typeId, subId sql.NullInt64
			var foId sql.NullInt64

			err := pool.QueryRow(`
				SELECT c.id, c.patient_name, c.hospital_name, c.status, c.insurance_company_id, c.case_type_id, c.case_sub_type_id, c.fo_id 
				FROM cases c 
				WHERE c.claim_number = ? LIMIT 1`, clm).Scan(&caseId, &patName, &hospName, &rawStat, &insId, &typeId, &subId, &foId)

			if err != nil {
				counts["not_found"] = counts["not_found"].(int) + 1
				results = append(results, verifiedRow{
					RowNo:        rowIdx,
					ClaimNumber:  clm,
					Amount:       userAmt,
					PaidDate:     paidDt,
					Remark:       "Claim Number not found in system cases database",
					Status:       "NOT_FOUND",
					PatientName:  "—",
					HospitalName: "—",
					AssignedUser: "—",
				})
				continue
			}

			statUpper := strings.ToUpper(rawStat.String)
			if strings.Contains(statUpper, "WITHDRAW") {
				counts["withdrawn"] = counts["withdrawn"].(int) + 1
				results = append(results, verifiedRow{
					RowNo:        rowIdx,
					ClaimNumber:  clm,
					CaseID:       caseId,
					Amount:       0,
					PaidDate:     paidDt,
					Remark:       "Case was Withdrawn — payout blocked",
					Status:       "WITHDRAWN",
					PatientName:  patName.String,
					HospitalName: hospName.String,
					AssignedUser: "—",
				})
				continue
			}
			if strings.Contains(statUpper, "REJECT") {
				counts["rejected"] = counts["rejected"].(int) + 1
				results = append(results, verifiedRow{
					RowNo:        rowIdx,
					ClaimNumber:  clm,
					CaseID:       caseId,
					Amount:       0,
					PaidDate:     paidDt,
					Remark:       "Case was Rejected — zero payout",
					Status:       "REJECTED",
					PatientName:  patName.String,
					HospitalName: hospName.String,
					AssignedUser: "—",
				})
				continue
			}

			// Calculate rate from payout_rate_config
			calcRate := 0.0
			_ = pool.QueryRow(`
				SELECT rate_amount FROM payout_rate_config 
				WHERE insurance_company_id = ? AND case_type_id = ? AND (case_sub_type_id = ? OR case_sub_type_id = 0)
				ORDER BY case_sub_type_id DESC LIMIT 1`, insId.Int64, typeId.Int64, subId.Int64).Scan(&calcRate)

			if calcRate <= 0 {
				calcRate = userAmt
				if calcRate <= 0 {
					calcRate = 500.00 // Default fallback standard rate
				}
			}

			var assignedUser string
			var empType string
			if foId.Valid && foId.Int64 > 0 {
				_ = pool.QueryRow("SELECT fullname, COALESCE(employment_type, 'Freelancer') FROM users WHERE id = ?", foId.Int64).Scan(&assignedUser, &empType)
			}
			if assignedUser == "" {
				assignedUser = "Field Team"
			}

			suspicious := false
			flags := []string{}
			if targetFoId > 0 && foId.Valid && int(foId.Int64) != targetFoId {
				suspicious = true
				flags = append(flags, "fo_mismatch")
			}

			rowStatus := "VERIFIED_READY"
			if suspicious {
				rowStatus = "SUSPICIOUS"
				counts["suspicious"] = counts["suspicious"].(int) + 1
			} else {
				counts["verified"] = counts["verified"].(int) + 1
			}

			results = append(results, verifiedRow{
				RowNo:          rowIdx,
				ClaimNumber:    clm,
				CaseID:         caseId,
				Amount:         calcRate,
				PaidDate:       paidDt,
				Remark:         remk,
				Status:         rowStatus,
				PatientName:    patName.String,
				HospitalName:   hospName.String,
				AssignedUser:   assignedUser,
				FoPartsCount:   1,
				FoPartsDone:    "Full",
				RateDetail:     fmt.Sprintf("Base: ₹%.2f", calcRate),
				EmploymentType: empType,
				Flags:          flags,
			})
		}

		importId := 0
		if save {
			sess, ok := c.Locals(sessionContextKey).(dashboardSession)
			uId := int64(1)
			uName := "Admin"
			if ok {
				uId = sess.UserID
				uName = sess.FullName
			}

			res, _ := pool.Exec(`
				INSERT INTO payout_imports (file_name, uploaded_by, uploaded_by_name, total_rows, verified_count, suspicious_count, excel_duplicate_count, db_duplicate_count, not_found_count, withdrawn_count, rejected_count, created_at) 
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NOW())`, fileHeader.Filename, uId, uName, counts["total"], counts["verified"], counts["suspicious"], counts["excel_duplicate"], counts["db_duplicate"], counts["not_found"], counts["withdrawn"], counts["rejected"])
			if res != nil {
				iid, _ := res.LastInsertId()
				importId = int(iid)
			}

			for _, r := range results {
				fJSON, _ := json.Marshal(r.Flags)
				_, _ = pool.Exec(`
					INSERT INTO payout_import_rows (import_id, row_no, claim_number, case_id, amount, paid_date, remark, status, patient_name, hospital_name, assigned_user, flags_json) 
					VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, importId, r.RowNo, r.ClaimNumber, r.CaseID, r.Amount, r.PaidDate, r.Remark, r.Status, r.PatientName, r.HospitalName, r.AssignedUser, string(fJSON))

				if r.Status == "VERIFIED_READY" {
					_, _ = pool.Exec(`
						INSERT INTO payout_records (import_id, claim_number, case_id, amount, paid_date, remark, status, patient_name, hospital_name, assigned_user, created_at) 
						VALUES (?, ?, ?, ?, ?, ?, 'VERIFIED', ?, ?, ?, NOW()) 
						ON DUPLICATE KEY UPDATE amount = VALUES(amount), paid_date = VALUES(paid_date)`, importId, r.ClaimNumber, r.CaseID, r.Amount, r.PaidDate, r.Remark, r.PatientName, r.HospitalName, r.AssignedUser)
				}
			}
		}

		return c.JSON(fiber.Map{
			"status":    "success",
			"counts":    counts,
			"results":   results,
			"import_id": importId,
			"saved":     save,
		})
	})

	// GET /api/payout/export (Full Excel Export)
	app.Get("/api/payout/export", requireSession, func(c *fiber.Ctx) error {
		month := strings.TrimSpace(c.Query("month"))
		from := strings.TrimSpace(c.Query("from"))
		to := strings.TrimSpace(c.Query("to"))

		where := []string{"1=1"}
		var params []interface{}
		if month != "" {
			where = append(where, "DATE_FORMAT(pr.created_at, '%Y-%m') = ?")
			params = append(params, month)
		}
		if from != "" {
			where = append(where, "DATE(pr.created_at) >= ?")
			params = append(params, from)
		}
		if to != "" {
			where = append(where, "DATE(pr.created_at) <= ?")
			params = append(params, to)
		}

		q := fmt.Sprintf(`
			SELECT COALESCE(pr.paid_date, ''), DATE_FORMAT(pr.created_at, '%%Y-%%m-%%d %%H:%%i'), pr.claim_number,
				   COALESCE(pr.case_id, 0), COALESCE(pr.patient_name, ''), COALESCE(pr.hospital_name, ''),
				   pr.amount, COALESCE(pr.assigned_user, ''), COALESCE(pr.employment_type, ''),
				   COALESCE(pr.fo_parts_done, ''), COALESCE(pi.file_name, '')
			FROM payout_records pr
			LEFT JOIN payout_imports pi ON pi.id = pr.import_id
			WHERE %s
			ORDER BY pr.created_at ASC, pr.id ASC LIMIT 10000`, strings.Join(where, " AND "))

		csvBuf := bytes.NewBuffer(nil)
		writer := csv.NewWriter(csvBuf)
		_ = writer.Write([]string{"Paid Date", "Saved On", "Claim #", "Case ID", "Patient", "Hospital", "Amount (INR)", "Assigned User", "Employment Type", "FO Parts", "Import File"})

		rows, err := pool.Query(q, params...)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var pDate, savedOn, clm, pat, hosp, usr, emp, parts, fName string
				var cid int
				var amt float64
				if err := rows.Scan(&pDate, &savedOn, &clm, &cid, &pat, &hosp, &amt, &usr, &emp, &parts, &fName); err == nil {
					_ = writer.Write([]string{pDate, savedOn, clm, strconv.Itoa(cid), pat, hosp, fmt.Sprintf("%.2f", amt), usr, emp, parts, fName})
				}
			}
		}
		writer.Flush()

		c.Set("Content-Type", "text/csv; charset=utf-8")
		c.Set("Content-Disposition", "attachment; filename=\"payout_export.csv\"")
		return c.Send(csvBuf.Bytes())
	})
}
