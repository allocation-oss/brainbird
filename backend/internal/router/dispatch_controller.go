package router

import (
	"database/sql"
	_ "embed"
	"fmt"
	"html"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

//go:embed templates/dispatch/index.html
var dispatchHTML []byte

type dispatchCaseRow struct {
	ID                  int64    `json:"id"`
	CaseID              int64    `json:"case_id"`
	ClaimNumber         string   `json:"claim_number"`
	PatientName         string   `json:"patient_name"`
	InsuranceCompanyID  int      `json:"insurance_company_id"`
	CompanyName         string   `json:"company_name"`
	CaseTypeID          int      `json:"case_type_id"`
	TypeName            string   `json:"type_name"`
	CaseSubTypeID       int      `json:"case_sub_type_id"`
	SubTypeName         string   `json:"sub_type_name"`
	HospitalName        string   `json:"hospital_name"`
	StateID             int      `json:"state_id"`
	StateName           string   `json:"state_name"`
	CityID              int      `json:"city_id"`
	DistrictName        string   `json:"district_name"`
	FOID                int      `json:"fo_id"`
	AssignedFOName      string   `json:"assigned_fo_name"`
	FOHospitalID        int      `json:"fo_hospital_id"`
	FOHospName          string   `json:"fo_hosp_name"`
	FOPatientID         int      `json:"fo_patient_id"`
	FOPatName           string   `json:"fo_pat_name"`
	FOPharmacyID        int      `json:"fo_pharmacy_id"`
	FOPharName          string   `json:"fo_phar_name"`
	FOPathologyID       int      `json:"fo_pathology_id"`
	FOPathName          string   `json:"fo_path_name"`
	FODoctorID          int      `json:"fo_doctor_id"`
	FODocName           string   `json:"fo_doc_name"`
	DocDoctorStatement  int      `json:"doc_doctor_statement"`
	DocPathology        int      `json:"doc_pathology"`
	DocFinalBill        int      `json:"doc_final_bill"`
	DocDischargeSummary int      `json:"doc_discharge_summary"`
	DocInsuredPart      int      `json:"doc_insured_part"`
	DocPreAuth          int      `json:"doc_pre_auth"`
	DocICP              int      `json:"doc_icp"`
	HardcopyReceived    int      `json:"hardcopy_received"`
	CompletedByType     string   `json:"completed_by_type"`
	CompletedByName     string   `json:"completed_by_name"`
	CaseVerdict         string   `json:"case_verdict"`
	AllocationDate      string   `json:"allocation_date"`
	ClosingDate         string   `json:"closing_date"`
	StatusDocUpdatedAt  string   `json:"status_doc_updated_at"`
	AllowedParts        []string `json:"allowed_parts"`
	IsFOComplete        bool     `json:"is_fo_complete"`
}

func ensureDispatchTables(pool *sql.DB) {
	if pool == nil {
		return
	}
	_, _ = pool.Exec(`
		CREATE TABLE IF NOT EXISTS case_document_status (
			id INT AUTO_INCREMENT PRIMARY KEY,
			case_id INT NOT NULL,
			doc_doctor_statement TINYINT(1) DEFAULT 0,
			doc_pathology TINYINT(1) DEFAULT 0,
			doc_final_bill TINYINT(1) DEFAULT 0,
			doc_discharge_summary TINYINT(1) DEFAULT 0,
			doc_insured_part TINYINT(1) DEFAULT 0,
			doc_pre_auth TINYINT(1) DEFAULT 0,
			doc_icp TINYINT(1) DEFAULT 0,
			hardcopy_received TINYINT(1) DEFAULT 0,
			completed_by_type VARCHAR(50) DEFAULT 'same_fo',
			completed_by_name VARCHAR(255) DEFAULT '',
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			UNIQUE KEY uq_case (case_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
	`)
}

func expandCasesByFOs(rawCases []dispatchCaseRow, tabFilter string) []dispatchCaseRow {
	var expanded []dispatchCaseRow

	for _, c := range rawCases {
		primaryFoID := c.FOID
		primaryFoName := c.AssignedFOName
		if primaryFoName == "" {
			primaryFoName = "Not Assigned"
		}

		hospFoID := c.FOHospitalID
		hospFoName := c.FOHospName
		if hospFoName == "" {
			hospFoName = primaryFoName
		}

		patFoID := c.FOPatientID
		patFoName := c.FOPatName
		if patFoName == "" {
			patFoName = primaryFoName
		}

		pharFoID := c.FOPharmacyID
		pharFoName := c.FOPharName
		if pharFoName == "" {
			pharFoName = primaryFoName
		}
		_ = pharFoID
		_ = pharFoName

		pathFoID := c.FOPathologyID
		pathFoName := c.FOPathName
		if pathFoName == "" {
			pathFoName = primaryFoName
		}

		docFoID := c.FODoctorID
		docFoName := c.FODocName
		if docFoName == "" {
			docFoName = primaryFoName
		}

		type foPartInfo struct {
			foID   int
			foName string
		}
		partMap := map[string]foPartInfo{
			"doc_doctor_statement":  {foID: ifZero(docFoID, primaryFoID), foName: ifEmpty(docFoName, primaryFoName)},
			"doc_pathology":         {foID: ifZero(pathFoID, primaryFoID), foName: ifEmpty(pathFoName, primaryFoName)},
			"doc_final_bill":        {foID: ifZero(hospFoID, primaryFoID), foName: ifEmpty(hospFoName, primaryFoName)},
			"doc_discharge_summary": {foID: ifZero(hospFoID, primaryFoID), foName: ifEmpty(hospFoName, primaryFoName)},
			"doc_insured_part":      {foID: ifZero(patFoID, primaryFoID), foName: ifEmpty(patFoName, primaryFoName)},
			"doc_pre_auth":          {foID: ifZero(hospFoID, primaryFoID), foName: ifEmpty(hospFoName, primaryFoName)},
			"doc_icp":               {foID: ifZero(hospFoID, primaryFoID), foName: ifEmpty(hospFoName, primaryFoName)},
		}

		type groupInfo struct {
			foID   int
			foName string
			parts  []string
		}
		foGroups := make(map[int]*groupInfo)

		for docKey, info := range partMap {
			if _, exists := foGroups[info.foID]; !exists {
				foGroups[info.foID] = &groupInfo{
					foID:   info.foID,
					foName: info.foName,
					parts:  []string{},
				}
			}
			foGroups[info.foID].parts = append(foGroups[info.foID].parts, docKey)
		}

		for _, g := range foGroups {
			cRow := c
			cRow.AssignedFOName = g.foName
			cRow.AllowedParts = g.parts

			docCount := 0
			totalAllowed := len(g.parts)
			for _, pKey := range g.parts {
				switch pKey {
				case "doc_doctor_statement":
					if c.DocDoctorStatement == 1 {
						docCount++
					}
				case "doc_pathology":
					if c.DocPathology == 1 {
						docCount++
					}
				case "doc_final_bill":
					if c.DocFinalBill == 1 {
						docCount++
					}
				case "doc_discharge_summary":
					if c.DocDischargeSummary == 1 {
						docCount++
					}
				case "doc_insured_part":
					if c.DocInsuredPart == 1 {
						docCount++
					}
				case "doc_pre_auth":
					if c.DocPreAuth == 1 {
						docCount++
					}
				case "doc_icp":
					if c.DocICP == 1 {
						docCount++
					}
				}
			}

			isFoComplete := (totalAllowed > 0 && docCount == totalAllowed) || (c.HardcopyReceived == 1)
			cRow.IsFOComplete = isFoComplete

			if tabFilter == "received" && !isFoComplete {
				continue
			}
			if tabFilter == "pending" && isFoComplete {
				continue
			}

			expanded = append(expanded, cRow)
		}
	}

	return expanded
}

func ifZero(v, fallback int) int {
	if v > 0 {
		return v
	}
	return fallback
}

func ifEmpty(v, fallback string) string {
	if strings.TrimSpace(v) != "" {
		return v
	}
	return fallback
}

func RegisterDispatchRoutes(app *fiber.App, pool *sql.DB, requireSession fiber.Handler) {
	ensureDispatchTables(pool)

	app.Get("/dispatch", requireSession, func(c *fiber.Ctx) error {
		exportExcel := c.QueryInt("export_excel", 0)

		if exportExcel == 1 {
			rawCases, _ := queryDispatchCases(pool, c)
			tabFilter := c.Query("tab_filter", "all")
			targetList := expandCasesByFOs(rawCases, tabFilter)

			var b strings.Builder
			b.WriteString(`<html xmlns:o="urn:schemas-microsoft-com:office:office" xmlns:x="urn:schemas-microsoft-com:office:excel" xmlns="http://www.w3.org/TR/REC-html40">`)
			b.WriteString(`<head><meta charset="utf-8"></head><body>`)
			b.WriteString(`<table border="1" cellpadding="5" cellspacing="0" style="border-collapse:collapse; font-family:Arial, sans-serif; font-size:12px;">`)
			b.WriteString(`<thead><tr style="background-color: #1e293b; color: #ffffff; font-weight: bold;">`)
			b.WriteString(`<th>Claim Number</th><th>Patient Name</th><th>Insurance Company</th><th>Case Type</th><th>Hospital</th><th>State</th><th>District</th><th>Assigned FO</th><th>Allocation Date</th><th>Closing Date</th><th>Verdict</th><th>Received Parts Summary</th><th>Hardcopy Received Status</th>`)
			b.WriteString(`</tr></thead><tbody>`)

			for _, r := range targetList {
				rcvdParts := []string{}
				for _, p := range r.AllowedParts {
					switch p {
					case "doc_doctor_statement":
						if r.DocDoctorStatement == 1 {
							rcvdParts = append(rcvdParts, "Doctor Statement")
						}
					case "doc_pathology":
						if r.DocPathology == 1 {
							rcvdParts = append(rcvdParts, "Pathology")
						}
					case "doc_final_bill":
						if r.DocFinalBill == 1 {
							rcvdParts = append(rcvdParts, "Final Bill")
						}
					case "doc_discharge_summary":
						if r.DocDischargeSummary == 1 {
							rcvdParts = append(rcvdParts, "Discharge Summary")
						}
					case "doc_insured_part":
						if r.DocInsuredPart == 1 {
							rcvdParts = append(rcvdParts, "Insured Part")
						}
					case "doc_pre_auth":
						if r.DocPreAuth == 1 {
							rcvdParts = append(rcvdParts, "Pre Auth")
						}
					case "doc_icp":
						if r.DocICP == 1 {
							rcvdParts = append(rcvdParts, "ICP")
						}
					}
				}
				rcvdSummary := "None"
				if len(rcvdParts) > 0 {
					rcvdSummary = strings.Join(rcvdParts, ", ")
				}

				status := "Pending"
				if r.IsFOComplete {
					status = "Received"
				} else if len(rcvdParts) > 0 {
					status = "Partially Received"
				}

				rowStyle := ""
				if len(rcvdParts) == 0 && !r.IsFOComplete && r.HardcopyReceived == 0 {
					rowStyle = `style="background-color: #ffff00;"`
				}

				b.WriteString(fmt.Sprintf(`<tr %s>`, rowStyle))
				b.WriteString(fmt.Sprintf(`<td>%s</td>`, html.EscapeString(r.ClaimNumber)))
				b.WriteString(fmt.Sprintf(`<td>%s</td>`, html.EscapeString(r.PatientName)))
				b.WriteString(fmt.Sprintf(`<td>%s</td>`, html.EscapeString(r.CompanyName)))
				cType := r.TypeName
				if r.SubTypeName != "" {
					cType += " (" + r.SubTypeName + ")"
				}
				b.WriteString(fmt.Sprintf(`<td>%s</td>`, html.EscapeString(cType)))
				b.WriteString(fmt.Sprintf(`<td>%s</td>`, html.EscapeString(r.HospitalName)))
				b.WriteString(fmt.Sprintf(`<td>%s</td>`, html.EscapeString(r.StateName)))
				b.WriteString(fmt.Sprintf(`<td>%s</td>`, html.EscapeString(r.DistrictName)))
				b.WriteString(fmt.Sprintf(`<td>%s</td>`, html.EscapeString(r.AssignedFOName)))
				b.WriteString(fmt.Sprintf(`<td>%s</td>`, html.EscapeString(r.AllocationDate)))
				b.WriteString(fmt.Sprintf(`<td>%s</td>`, html.EscapeString(r.ClosingDate)))
				b.WriteString(fmt.Sprintf(`<td>%s</td>`, html.EscapeString(r.CaseVerdict)))
				b.WriteString(fmt.Sprintf(`<td>%s</td>`, html.EscapeString(rcvdSummary)))
				b.WriteString(fmt.Sprintf(`<td>%s</td>`, status))
				b.WriteString(`</tr>`)
			}

			b.WriteString(`</tbody></table></body></html>`)
			c.Type("application/vnd.ms-excel")
			c.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="Dispatch_Records_%s.xls"`, time.Now().Format("20060102_150405")))
			return c.SendString(b.String())
		}

		html := string(dispatchHTML)
		html = strings.ReplaceAll(html, "{{SIDEBAR_PARTIAL}}", string(dashboardSidebarPartial))
		html = strings.ReplaceAll(html, "{{HEADER_PARTIAL}}", string(dashboardHeaderPartial))
		html = strings.ReplaceAll(html, "{{FOOTER_PARTIAL}}", string(dashboardFooterPartial))
		c.Type("html", "utf-8")
		return c.SendString(html)
	})

	api := app.Group("/api/dispatch", requireSession)

	api.All("/list", func(c *fiber.Ctx) error {
		rawCases, err := queryDispatchCases(pool, c)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"status": "error", "message": err.Error()})
		}

		limit := c.QueryInt("limit", 25)
		page := c.QueryInt("page", 1)
		if limit < 1 {
			limit = 25
		}
		if page < 1 {
			page = 1
		}

		tabFilter := c.Query("tab_filter", "all")

		allExpanded := expandCasesByFOs(rawCases, "all")
		recExpanded := expandCasesByFOs(rawCases, "received")
		penExpanded := expandCasesByFOs(rawCases, "pending")

		cntTotal := len(allExpanded)
		cntReceived := len(recExpanded)
		cntPending := len(penExpanded)

		var targetList []dispatchCaseRow
		if tabFilter == "received" {
			targetList = recExpanded
		} else if tabFilter == "pending" {
			targetList = penExpanded
		} else {
			targetList = allExpanded
		}

		totalRecords := len(targetList)
		totalPages := int(math.Ceil(float64(totalRecords) / float64(limit)))
		if totalPages < 1 {
			totalPages = 1
		}

		offset := (page - 1) * limit
		if offset > totalRecords {
			offset = totalRecords
		}
		end := offset + limit
		if end > totalRecords {
			end = totalRecords
		}

		pagedCases := targetList[offset:end]

		return c.JSON(fiber.Map{
			"status": "success",
			"cases":  pagedCases,
			"stats": fiber.Map{
				"total":    cntTotal,
				"received": cntReceived,
				"pending":  cntPending,
			},
			"pagination": fiber.Map{
				"current_page":  page,
				"total_pages":   totalPages,
				"total_records": totalRecords,
				"limit":         limit,
			},
		})
	})

	api.All("/initial_data", func(c *fiber.Ctx) error {
		type simpleItem struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		}

		insurances := []simpleItem{}
		if rows, err := pool.Query("SELECT id, company_name FROM master_insurance_companies WHERE status='active' ORDER BY company_name ASC"); err == nil {
			defer rows.Close()
			for rows.Next() {
				var it simpleItem
				if err := rows.Scan(&it.ID, &it.Name); err == nil {
					insurances = append(insurances, it)
				}
			}
		}

		types := []simpleItem{}
		if rows, err := pool.Query("SELECT id, type_name FROM master_case_types WHERE status='active' ORDER BY type_name ASC"); err == nil {
			defer rows.Close()
			for rows.Next() {
				var it simpleItem
				if err := rows.Scan(&it.ID, &it.Name); err == nil {
					types = append(types, it)
				}
			}
		}

		states := []simpleItem{}
		if rows, err := pool.Query("SELECT id, state_name FROM master_states ORDER BY state_name ASC"); err == nil {
			defer rows.Close()
			for rows.Next() {
				var it simpleItem
				if err := rows.Scan(&it.ID, &it.Name); err == nil {
					states = append(states, it)
				}
			}
		}

		fos := []simpleItem{}
		if rows, err := pool.Query("SELECT id, fullname FROM users WHERE status='active' AND role_id = 6 ORDER BY fullname ASC"); err == nil {
			defer rows.Close()
			for rows.Next() {
				var it simpleItem
				if err := rows.Scan(&it.ID, &it.Name); err == nil {
					fos = append(fos, it)
				}
			}
		}

		return c.JSON(fiber.Map{
			"status":     "success",
			"insurances": insurances,
			"types":      types,
			"states":     states,
			"fos":        fos,
		})
	})

	api.Post("/update-part-fo", func(c *fiber.Ctx) error {
		caseID, _ := strconv.Atoi(c.FormValue("case_id"))
		foField := strings.TrimSpace(c.FormValue("fo_field"))
		foUserID, _ := strconv.Atoi(c.FormValue("fo_user_id"))

		if caseID <= 0 {
			return c.JSON(fiber.Map{"status": "error", "message": "Invalid Case ID"})
		}

		fieldMap := map[string]string{
			"fo_id":           "fo_id",
			"fo_doc_id":       "fo_doctor_id",
			"fo_doctor_id":    "fo_doctor_id",
			"fo_path_id":      "fo_pathology_id",
			"fo_pathology_id": "fo_pathology_id",
			"fo_hosp_id":      "fo_hospital_id",
			"fo_hospital_id":  "fo_hospital_id",
			"fo_pat_id":       "fo_patient_id",
			"fo_patient_id":   "fo_patient_id",
			"fo_phar_id":      "fo_pharmacy_id",
			"fo_pharmacy_id":  "fo_pharmacy_id",
		}

		dbCol, exists := fieldMap[foField]
		if !exists {
			return c.JSON(fiber.Map{"status": "error", "message": "Invalid FO Field"})
		}

		var foName string
		if foUserID > 0 {
			_ = pool.QueryRow("SELECT fullname FROM users WHERE id = ?", foUserID).Scan(&foName)
		}

		if dbCol == "fo_id" && foName != "" {
			_, err := pool.Exec("UPDATE cases SET fo_id = ?, fo_name = ? WHERE id = ?", foUserID, foName, caseID)
			if err != nil {
				return c.Status(500).JSON(fiber.Map{"status": "error", "message": err.Error()})
			}
		} else {
			var val interface{} = nil
			if foUserID > 0 {
				val = foUserID
			}
			query := fmt.Sprintf("UPDATE cases SET %s = ? WHERE id = ?", dbCol)
			_, err := pool.Exec(query, val, caseID)
			if err != nil {
				return c.Status(500).JSON(fiber.Map{"status": "error", "message": err.Error()})
			}
		}

		return c.JSON(fiber.Map{"status": "success", "fo_name": foName})
	})

	api.Post("/final-submit-fo", func(c *fiber.Ctx) error {
		caseID, _ := strconv.Atoi(c.FormValue("case_id"))
		partsStr := c.FormValue("parts_str")
		if partsStr == "" {
			partsStr = c.FormValue("parts")
		}

		if caseID <= 0 {
			return c.JSON(fiber.Map{"status": "error", "message": "Invalid Case ID"})
		}

		allowedFields := map[string]bool{
			"doc_doctor_statement":  true,
			"doc_pathology":         true,
			"doc_final_bill":        true,
			"doc_discharge_summary": true,
			"doc_insured_part":      true,
			"doc_pre_auth":          true,
			"doc_icp":               true,
		}

		partsRaw := strings.Split(partsStr, ",")
		updates := []string{}
		for _, p := range partsRaw {
			p = strings.TrimSpace(p)
			if allowedFields[p] {
				updates = append(updates, fmt.Sprintf("%s = 1", p))
			}
		}

		var existsID int
		_ = pool.QueryRow("SELECT id FROM case_document_status WHERE case_id = ?", caseID).Scan(&existsID)

		if existsID > 0 {
			if len(updates) > 0 {
				q := fmt.Sprintf("UPDATE case_document_status SET %s WHERE case_id = ?", strings.Join(updates, ", "))
				_, _ = pool.Exec(q, caseID)
			}
		} else {
			_, _ = pool.Exec("INSERT INTO case_document_status (case_id) VALUES (?)", caseID)
			if len(updates) > 0 {
				q := fmt.Sprintf("UPDATE case_document_status SET %s WHERE case_id = ?", strings.Join(updates, ", "))
				_, _ = pool.Exec(q, caseID)
			}
		}

		var docCount, hardcopy int
		_ = pool.QueryRow(`
			SELECT (doc_doctor_statement + doc_pathology + doc_final_bill + doc_discharge_summary + doc_insured_part + doc_pre_auth + doc_icp), hardcopy_received 
			FROM case_document_status WHERE case_id = ?`, caseID).Scan(&docCount, &hardcopy)

		if docCount == 7 && hardcopy == 0 {
			_, _ = pool.Exec("UPDATE case_document_status SET hardcopy_received = 1 WHERE case_id = ?", caseID)
		}

		return c.JSON(fiber.Map{"status": "success", "docCount": docCount})
	})

	api.Get("/docs/:id", func(c *fiber.Ctx) error {
		id, _ := strconv.Atoi(c.Params("id"))

		query := `
			SELECT COALESCE(d.doc_doctor_statement, 0), COALESCE(d.doc_pathology, 0), COALESCE(d.doc_final_bill, 0),
			       COALESCE(d.doc_discharge_summary, 0), COALESCE(d.doc_insured_part, 0), COALESCE(d.doc_pre_auth, 0),
			       COALESCE(d.doc_icp, 0), COALESCE(d.hardcopy_received, 0),
			       COALESCE(d.completed_by_type, 'same_fo'), COALESCE(d.completed_by_name, ''),
			       COALESCE(c.fo_id, 0), COALESCE(c.fo_hospital_id, 0), COALESCE(c.fo_patient_id, 0),
			       COALESCE(c.fo_pharmacy_id, 0), COALESCE(c.fo_pathology_id, 0), COALESCE(c.fo_doctor_id, 0),
			       COALESCE(u_fo.fullname, u_alloc.fullname, c.fo_name, 'Not Assigned'),
			       COALESCE(u_fo_h.fullname, ''), COALESCE(u_fo_p.fullname, ''),
			       COALESCE(u_fo_ph.fullname, ''), COALESCE(u_fo_pa.fullname, ''),
			       COALESCE(u_fo_doc.fullname, '')
			FROM cases c
			LEFT JOIN case_document_status d ON c.id = d.case_id
			LEFT JOIN users u_fo ON c.fo_id = u_fo.id
			LEFT JOIN users u_alloc ON c.allocated_to = u_alloc.id
			LEFT JOIN users u_fo_h ON c.fo_hospital_id = u_fo_h.id
			LEFT JOIN users u_fo_p ON c.fo_patient_id = u_fo_p.id
			LEFT JOIN users u_fo_ph ON c.fo_pharmacy_id = u_fo_ph.id
			LEFT JOIN users u_fo_pa ON c.fo_pathology_id = u_fo_pa.id
			LEFT JOIN users u_fo_doc ON c.fo_doctor_id = u_fo_doc.id
			WHERE c.id = ? LIMIT 1
		`

		var dDoc, dPath, dBill, dDisc, dIns, dPre, dICP, dHard int
		var compType, compName string
		var foID, hospFoID, patFoID, pharFoID, pathFoID, docFoID int
		var primFOName, hospFOName, patFOName, pharFOName, pathFOName, docFOName string

		err := pool.QueryRow(query, id).Scan(
			&dDoc, &dPath, &dBill, &dDisc, &dIns, &dPre, &dICP, &dHard,
			&compType, &compName,
			&foID, &hospFoID, &patFoID, &pharFoID, &pathFoID, &docFoID,
			&primFOName, &hospFOName, &patFOName, &pharFOName, &pathFOName, &docFOName,
		)
		if err != nil {
			return c.JSON(fiber.Map{"status": "success", "data": fiber.Map{
				"doc_doctor_statement": 0, "doc_pathology": 0, "doc_final_bill": 0,
				"doc_discharge_summary": 0, "doc_insured_part": 0, "doc_pre_auth": 0,
				"doc_icp": 0, "hardcopy_received": 0,
				"completed_by_type": "same_fo", "completed_by_name": "",
			}})
		}

		return c.JSON(fiber.Map{"status": "success", "data": fiber.Map{
			"doc_doctor_statement":  dDoc,
			"doc_pathology":         dPath,
			"doc_final_bill":        dBill,
			"doc_discharge_summary": dDisc,
			"doc_insured_part":      dIns,
			"doc_pre_auth":          dPre,
			"doc_icp":               dICP,
			"hardcopy_received":     dHard,
			"completed_by_type":     compType,
			"completed_by_name":     compName,
			"fo_id":                 foID,
			"fo_hospital_id":        hospFoID,
			"fo_patient_id":         patFoID,
			"fo_pharmacy_id":        pharFoID,
			"fo_pathology_id":       pathFoID,
			"fo_doctor_id":          docFoID,
			"primary_fo_name":       primFOName,
			"fo_hosp_name":          hospFOName,
			"fo_pat_name":           patFOName,
			"fo_phar_name":          pharFOName,
			"fo_path_name":          pathFOName,
			"fo_doc_name":           docFOName,
		}})
	})

	api.Post("/toggle-doc", func(c *fiber.Ctx) error {
		caseID, _ := strconv.Atoi(c.FormValue("case_id"))
		field := strings.TrimSpace(c.FormValue("field"))
		value, _ := strconv.Atoi(c.FormValue("value"))

		if caseID <= 0 {
			return c.JSON(fiber.Map{"status": "error", "message": "Invalid Case ID"})
		}

		allowedFields := map[string]bool{
			"doc_doctor_statement":  true,
			"doc_pathology":         true,
			"doc_final_bill":        true,
			"doc_discharge_summary": true,
			"doc_insured_part":      true,
			"doc_pre_auth":          true,
			"doc_icp":               true,
			"hardcopy_received":     true,
		}

		if !allowedFields[field] {
			return c.JSON(fiber.Map{"status": "error", "message": "Invalid Field"})
		}

		var existsID int
		_ = pool.QueryRow("SELECT id FROM case_document_status WHERE case_id = ?", caseID).Scan(&existsID)

		if existsID > 0 {
			q := fmt.Sprintf("UPDATE case_document_status SET %s = ? WHERE case_id = ?", field)
			_, _ = pool.Exec(q, value, caseID)
		} else {
			q := fmt.Sprintf("INSERT INTO case_document_status (case_id, %s) VALUES (?, ?)", field)
			_, _ = pool.Exec(q, caseID, value)
		}

		var docCount, hardcopy int
		_ = pool.QueryRow(`
			SELECT (doc_doctor_statement + doc_pathology + doc_final_bill + doc_discharge_summary + doc_insured_part + doc_pre_auth + doc_icp), hardcopy_received 
			FROM case_document_status WHERE case_id = ?`, caseID).Scan(&docCount, &hardcopy)

		if docCount == 7 && hardcopy == 0 {
			_, _ = pool.Exec("UPDATE case_document_status SET hardcopy_received = 1 WHERE case_id = ?", caseID)
			hardcopy = 1
		}

		return c.JSON(fiber.Map{"status": "success", "docCount": docCount, "hardcopy_received": hardcopy})
	})

	api.Post("/receive-all", func(c *fiber.Ctx) error {
		caseID, _ := strconv.Atoi(c.FormValue("case_id"))
		if caseID <= 0 {
			return c.JSON(fiber.Map{"status": "error", "message": "Invalid Case ID"})
		}

		var existsID int
		_ = pool.QueryRow("SELECT id FROM case_document_status WHERE case_id = ?", caseID).Scan(&existsID)

		if existsID > 0 {
			_, _ = pool.Exec(`
				UPDATE case_document_status 
				SET doc_doctor_statement=1, doc_pathology=1, doc_final_bill=1, doc_discharge_summary=1, 
				    doc_insured_part=1, doc_pre_auth=1, doc_icp=1, hardcopy_received=1 
				WHERE case_id = ?`, caseID)
		} else {
			_, _ = pool.Exec(`
				INSERT INTO case_document_status (case_id, doc_doctor_statement, doc_pathology, doc_final_bill, doc_discharge_summary, doc_insured_part, doc_pre_auth, doc_icp, hardcopy_received) 
				VALUES (?, 1, 1, 1, 1, 1, 1, 1, 1)`, caseID)
		}

		return c.JSON(fiber.Map{"status": "success"})
	})

	api.Post("/save-meta", func(c *fiber.Ctx) error {
		caseID, _ := strconv.Atoi(c.FormValue("case_id"))
		compType := strings.TrimSpace(c.FormValue("completed_by_type"))
		compName := strings.TrimSpace(c.FormValue("completed_by_name"))

		if caseID <= 0 {
			return c.JSON(fiber.Map{"status": "error", "message": "Invalid Case ID"})
		}

		var existsID int
		_ = pool.QueryRow("SELECT id FROM case_document_status WHERE case_id = ?", caseID).Scan(&existsID)

		if existsID > 0 {
			_, _ = pool.Exec("UPDATE case_document_status SET completed_by_type = ?, completed_by_name = ? WHERE case_id = ?", compType, compName, caseID)
		} else {
			_, _ = pool.Exec("INSERT INTO case_document_status (case_id, completed_by_type, completed_by_name) VALUES (?, ?, ?)", caseID, compType, compName)
		}

		return c.JSON(fiber.Map{"status": "success"})
	})
}

func queryDispatchCases(pool *sql.DB, c *fiber.Ctx) ([]dispatchCaseRow, error) {
	where := []string{"(c.deleted_at IS NULL AND c.claim_number IS NOT NULL AND TRIM(c.claim_number) != '')"}
	var args []interface{}

	insurance := c.QueryInt("insurance", 0)
	if insurance > 0 {
		where = append(where, "c.insurance_company_id = ?")
		args = append(args, insurance)
	}

	search := strings.TrimSpace(c.Query("search"))
	if search != "" {
		where = append(where, "(c.claim_number LIKE ? OR c.patient_name LIKE ?)")
		args = append(args, "%"+search+"%", "%"+search+"%")
	}

	caseType := c.QueryInt("case_type", 0)
	if caseType > 0 {
		where = append(where, "c.case_type_id = ?")
		args = append(args, caseType)
	}

	subType := c.QueryInt("sub_type", 0)
	if subType > 0 {
		where = append(where, "c.case_sub_type_id = ?")
		args = append(args, subType)
	}

	month := strings.TrimSpace(c.Query("month"))
	if month != "" {
		where = append(where, "DATE_FORMAT(c.created_at, '%Y-%m') = ?")
		args = append(args, month)
	}

	exactDate := strings.TrimSpace(c.Query("exact_date"))
	if exactDate != "" {
		where = append(where, "DATE(c.created_at) = ?")
		args = append(args, exactDate)
	}

	hospital := strings.TrimSpace(c.Query("hospital"))
	if hospital != "" {
		where = append(where, "c.hospital_name LIKE ?")
		args = append(args, "%"+hospital+"%")
	}

	state := c.QueryInt("state", 0)
	if state > 0 {
		where = append(where, "c.state_id = ?")
		args = append(args, state)
	}

	city := c.QueryInt("city", 0)
	if city > 0 {
		where = append(where, "c.city_id = ?")
		args = append(args, city)
	}

	foID := c.QueryInt("fo_id", 0)
	if foID > 0 {
		where = append(where, "(c.fo_id = ? OR c.allocated_to = ? OR c.fo_hospital_id = ? OR c.fo_patient_id = ? OR c.fo_pharmacy_id = ? OR c.fo_pathology_id = ? OR c.fo_doctor_id = ?)")
		for i := 0; i < 7; i++ {
			args = append(args, foID)
		}
	}

	query := fmt.Sprintf(`
		SELECT c.id, c.id, COALESCE(c.claim_number, ''), COALESCE(c.patient_name, ''),
		       COALESCE(c.insurance_company_id, 0), COALESCE(m.company_name, ''),
		       COALESCE(c.case_type_id, 0), COALESCE(t.type_name, ''),
		       COALESCE(c.case_sub_type_id, 0), COALESCE(st.sub_type_name, ''),
		       COALESCE(c.hospital_name, ''),
		       COALESCE(c.state_id, 0), COALESCE(s.state_name, ''),
		       COALESCE(c.city_id, 0), COALESCE(dst.district_name, ''),
		       COALESCE(c.fo_id, 0), COALESCE(u_fo.fullname, u_alloc.fullname, c.fo_name, 'Not Assigned'),
		       COALESCE(c.fo_hospital_id, 0), COALESCE(u_fo_h.fullname, ''),
		       COALESCE(c.fo_patient_id, 0), COALESCE(u_fo_p.fullname, ''),
		       COALESCE(c.fo_pharmacy_id, 0), COALESCE(u_fo_ph.fullname, ''),
		       COALESCE(c.fo_pathology_id, 0), COALESCE(u_fo_pa.fullname, ''),
		       COALESCE(c.fo_doctor_id, 0), COALESCE(u_fo_doc.fullname, ''),
		       COALESCE(d.doc_doctor_statement, 0), COALESCE(d.doc_pathology, 0), COALESCE(d.doc_final_bill, 0),
		       COALESCE(d.doc_discharge_summary, 0), COALESCE(d.doc_insured_part, 0), COALESCE(d.doc_pre_auth, 0),
		       COALESCE(d.doc_icp, 0), COALESCE(d.hardcopy_received, 0),
		       COALESCE(d.completed_by_type, 'same_fo'), COALESCE(d.completed_by_name, ''),
		       COALESCE(c.case_verdict, ''),
		       COALESCE(DATE_FORMAT(c.created_at, '%%d-%%b-%%Y'), '-'),
		       COALESCE(DATE_FORMAT(COALESCE(c.hospital_done_at, c.patient_done_at, c.pathology_done_at, c.pharmacy_done_at, c.report_submission_date, c.updated_at), '%%d-%%b-%%Y'), '-'),
		       COALESCE(DATE_FORMAT(d.updated_at, '%%d-%%b-%%Y %%H:%%i'), '')
		FROM cases c
		LEFT JOIN users u_fo ON c.fo_id = u_fo.id
		LEFT JOIN users u_alloc ON c.allocated_to = u_alloc.id
		LEFT JOIN users u_fo_h ON c.fo_hospital_id = u_fo_h.id
		LEFT JOIN users u_fo_p ON c.fo_patient_id = u_fo_p.id
		LEFT JOIN users u_fo_ph ON c.fo_pharmacy_id = u_fo_ph.id
		LEFT JOIN users u_fo_pa ON c.fo_pathology_id = u_fo_pa.id
		LEFT JOIN users u_fo_doc ON c.fo_doctor_id = u_fo_doc.id
		LEFT JOIN master_insurance_companies m ON c.insurance_company_id = m.id
		LEFT JOIN master_case_types t ON c.case_type_id = t.id
		LEFT JOIN master_case_sub_types st ON c.case_sub_type_id = st.id
		LEFT JOIN master_states s ON c.state_id = s.id
		LEFT JOIN master_districts dst ON c.city_id = dst.id
		LEFT JOIN case_document_status d ON c.id = d.case_id
		WHERE %s
		ORDER BY c.id DESC
	`, strings.Join(where, " AND "))

	rows, err := pool.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []dispatchCaseRow
	for rows.Next() {
		var r dispatchCaseRow
		err := rows.Scan(
			&r.ID, &r.CaseID, &r.ClaimNumber, &r.PatientName,
			&r.InsuranceCompanyID, &r.CompanyName,
			&r.CaseTypeID, &r.TypeName,
			&r.CaseSubTypeID, &r.SubTypeName,
			&r.HospitalName,
			&r.StateID, &r.StateName,
			&r.CityID, &r.DistrictName,
			&r.FOID, &r.AssignedFOName,
			&r.FOHospitalID, &r.FOHospName,
			&r.FOPatientID, &r.FOPatName,
			&r.FOPharmacyID, &r.FOPharName,
			&r.FOPathologyID, &r.FOPathName,
			&r.FODoctorID, &r.FODocName,
			&r.DocDoctorStatement, &r.DocPathology, &r.DocFinalBill,
			&r.DocDischargeSummary, &r.DocInsuredPart, &r.DocPreAuth,
			&r.DocICP, &r.HardcopyReceived,
			&r.CompletedByType, &r.CompletedByName,
			&r.CaseVerdict,
			&r.AllocationDate, &r.ClosingDate, &r.StatusDocUpdatedAt,
		)
		if err != nil {
			continue
		}
		result = append(result, r)
	}

	return result, nil
}
