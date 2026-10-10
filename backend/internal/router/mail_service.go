package router

import (
	"crypto/tls"
	"database/sql"
	"fmt"
	"html"
	"log"
	"net"
	"net/smtp"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

type MailAMRequest struct {
	AreaManagerID string `json:"am_id"`
	ClaimNumber   string `json:"claim_number"`
	HospitalName  string `json:"hospital_name"`
	InsuranceID   string `json:"insurance_id"`
	TypeID        string `json:"type_id"`
	SubTypeID     string `json:"sub_type_id"`
	StateID       string `json:"state_id"`
	CityID        string `json:"city_id"`
	Date          string `json:"allocation_date"`
	Month         string `json:"filter_month_year"`
	TabStatus     string `json:"tab_status"`
	Category      string `json:"category"`
	Overdue       string `json:"overdue"`
}

type emailCaseItem struct {
	ClaimNumber string
	Patient     string
	Insurance   string
	CaseType    string
	SubType     string
	Hospital    string
	City        string
	State       string
	AllocatedAt string
	TATHours    int
	ElapsedSec  int64
	IsOverdue   bool
	Status      string
	Verdict     string
	Remark      string
}

type areaManagerEmailPayload struct {
	RecipientName string
	RecipientMail string
	SenderName    string
	GeneratedAt   string
	FilterDetails []string
	TotalCases    int
	OverdueCases  int
	OpenCases     int
	ClosedCases   int
	Cases         []emailCaseItem
	DashboardURL  string
}

// SendHTMLEmail sends an HTML email via SMTP if configured, or logs in dev mode.
func SendHTMLEmail(to, recipientName, subject, htmlBody string) (string, error) {
	host := strings.TrimSpace(os.Getenv("SMTP_HOST"))
	port := strings.TrimSpace(os.Getenv("SMTP_PORT"))
	if port == "" {
		port = "587"
	}
	user := strings.TrimSpace(os.Getenv("SMTP_USER"))
	pass := strings.TrimSpace(os.Getenv("SMTP_PASS"))
	if pass == "" {
		pass = strings.TrimSpace(os.Getenv("SMTP_PASSWORD"))
	}
	from := strings.TrimSpace(os.Getenv("SMTP_FROM"))
	if from == "" {
		from = "Brain Bird <admin@brainbirdconsulting.co.in>"
	}

	// Dev / Local fallback when SMTP_HOST is not configured
	if host == "" {
		log.Printf("[MAIL SERVICE] (Dev Mode - SMTP_HOST not set) To: %s <%s>, Subject: %q, HTML Size: %d bytes. Set SMTP_HOST in .env for live dispatch.",
			recipientName, to, subject, len(htmlBody))
		return "simulated", nil
	}

	// Live SMTP Sending
	addr := net.JoinHostPort(host, port)
	headers := make(map[string]string)
	headers["From"] = from
	headers["To"] = fmt.Sprintf("%s <%s>", recipientName, to)
	headers["Subject"] = subject
	headers["MIME-Version"] = "1.0"
	headers["Content-Type"] = "text/html; charset=UTF-8"
	headers["Date"] = time.Now().Format(time.RFC1123Z)

	var msgBuilder strings.Builder
	for k, v := range headers {
		msgBuilder.WriteString(fmt.Sprintf("%s: %s\r\n", k, v))
	}
	msgBuilder.WriteString("\r\n")
	msgBuilder.WriteString(htmlBody)
	rawMsg := []byte(msgBuilder.String())

	// If port 465 (implicit TLS)
	if port == "465" {
		tlsConfig := &tls.Config{
			ServerName: host,
		}
		conn, err := tls.Dial("tcp", addr, tlsConfig)
		if err != nil {
			return "", fmt.Errorf("tls dial failed: %w", err)
		}
		defer conn.Close()

		client, err := smtp.NewClient(conn, host)
		if err != nil {
			return "", fmt.Errorf("smtp client init failed: %w", err)
		}
		defer client.Quit()

		if user != "" && pass != "" {
			auth := smtp.PlainAuth("", user, pass, host)
			if err := client.Auth(auth); err != nil {
				return "", fmt.Errorf("smtp auth failed: %w", err)
			}
		}
		if err := client.Mail(from); err != nil {
			return "", fmt.Errorf("smtp mail from failed: %w", err)
		}
		if err := client.Rcpt(to); err != nil {
			return "", fmt.Errorf("smtp rcpt to failed: %w", err)
		}
		w, err := client.Data()
		if err != nil {
			return "", fmt.Errorf("smtp data writer failed: %w", err)
		}
		if _, err := w.Write(rawMsg); err != nil {
			return "", fmt.Errorf("smtp write message failed: %w", err)
		}
		if err := w.Close(); err != nil {
			return "", fmt.Errorf("smtp close writer failed: %w", err)
		}
		return "delivered", nil
	}

	// Standard STARTTLS / Plain SMTP (587, 25, 2525)
	var auth smtp.Auth
	if user != "" && pass != "" {
		auth = smtp.PlainAuth("", user, pass, host)
	}
	if err := smtp.SendMail(addr, auth, from, []string{to}, rawMsg); err != nil {
		return "", fmt.Errorf("smtp sendmail failed: %w", err)
	}

	return "delivered", nil
}

// renderAreaManagerHTMLEmail builds an executive-grade HTML email template
func renderAreaManagerHTMLEmail(data areaManagerEmailPayload) string {
	var rowsHTML strings.Builder
	if len(data.Cases) == 0 {
		rowsHTML.WriteString(`
		<tr style="background:#ffffff; border-bottom:1px solid #e2e8f0;">
			<td colspan="6" style="padding:28px 14px; text-align:center; color:#64748b; font-size:12px;">
				No case records found matching the active filter criteria.
			</td>
		</tr>`)
	}
	for i, c := range data.Cases {
		bg := "#ffffff"
		if i%2 == 1 {
			bg = "#f8fafc"
		}

		tatBadge := `<span style="display:inline-block; padding:2px 7px; border-radius:4px; font-size:10px; font-weight:600; color:#15803d; background:#dcfce7; border:1px solid #bbf7d0;">Within TAT</span>`
		if c.IsOverdue {
			tatBadge = `<span style="display:inline-block; padding:2px 7px; border-radius:4px; font-size:10px; font-weight:700; color:#b91c1c; background:#fee2e2; border:1px solid #fecaca;">OUT OF TAT</span>`
		}

		statusBadge := `<span style="display:inline-block; padding:2px 7px; border-radius:4px; font-size:10px; font-weight:600; text-transform:capitalize; color:#1d4ed8; background:#dbeafe;">` + html.EscapeString(c.Status) + `</span>`
		if c.Status == "resolved" || c.Status == "completed" {
			statusBadge = `<span style="display:inline-block; padding:2px 7px; border-radius:4px; font-size:10px; font-weight:600; text-transform:capitalize; color:#15803d; background:#dcfce7;">Resolved</span>`
		}

		catText := html.EscapeString(c.CaseType)
		if c.SubType != "" {
			catText += " / " + html.EscapeString(c.SubType)
		}

		locText := html.EscapeString(c.Hospital)
		if c.City != "" {
			locText += " (" + html.EscapeString(c.City) + ")"
		}

		rowsHTML.WriteString(fmt.Sprintf(`
		<tr style="background:%s; border-bottom:1px solid #e2e8f0;">
			<td style="padding:10px 12px; font-size:12px; vertical-align:top;">
				<strong style="color:#1d4ed8; font-size:12.5px;">%s</strong>
				<div style="color:#0f172a; margin-top:2px;">%s</div>
			</td>
			<td style="padding:10px 12px; font-size:11.5px; vertical-align:top; color:#334155;">
				<div style="font-weight:600; color:#0f172a;">%s</div>
				<div style="color:#64748b; font-size:11px; margin-top:2px;">%s</div>
			</td>
			<td style="padding:10px 12px; font-size:11.5px; vertical-align:top; color:#334155;">
				%s
			</td>
			<td style="padding:10px 12px; font-size:11.5px; vertical-align:top; color:#475569; white-space:nowrap;">
				%s
			</td>
			<td style="padding:10px 12px; font-size:11px; vertical-align:top; white-space:nowrap;">
				%s
			</td>
			<td style="padding:10px 12px; font-size:11px; vertical-align:top;">
				%s
				%s
			</td>
		</tr>`,
			bg,
			html.EscapeString(c.ClaimNumber),
			html.EscapeString(c.Patient),
			html.EscapeString(c.Insurance),
			catText,
			locText,
			html.EscapeString(c.AllocatedAt),
			tatBadge,
			statusBadge,
			func() string {
				if c.Remark != "" {
					return `<div style="color:#64748b; font-size:10.5px; margin-top:3px; max-width:140px; word-break:break-word;">` + html.EscapeString(c.Remark) + `</div>`
				}
				return ""
			}(),
		))
	}

	filterTags := ""
	if len(data.FilterDetails) > 0 {
		var b strings.Builder
		b.WriteString(`<div style="margin:14px 0 18px; padding:10px 14px; background:#f1f5f9; border-radius:6px; border:1px solid #e2e8f0; font-size:11px; color:#475569;">`)
		b.WriteString(`<strong style="color:#1e293b; margin-right:6px;">Active Filter Scope:</strong> `)
		b.WriteString(strings.Join(data.FilterDetails, " &bull; "))
		b.WriteString(`</div>`)
		filterTags = b.String()
	}

	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Brain Bird - Case Allocation Summary</title>
</head>
<body style="margin:0; padding:24px 12px; background-color:#f1f5f9; font-family:-apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif; color:#0f172a; line-height:1.45;">
  <div style="max-width:720px; margin:0 auto; background:#ffffff; border-radius:10px; overflow:hidden; border:1px solid #cbd5e1; box-shadow:0 10px 30px rgba(15,23,42,0.08);">
    
    <!-- Header -->
    <div style="background:linear-gradient(135deg, #0f172a 0%%, #1e293b 100%%); padding:26px 30px; border-top:4px solid #2563eb;">
      <table style="width:100%%; border-collapse:collapse;">
        <tr>
          <td style="vertical-align:middle;">
            <div style="color:#2563eb; font-size:11px; font-weight:800; letter-spacing:0.12em; text-transform:uppercase;">BRAIN BIRD MEDICAL CLAIMS</div>
            <h1 style="color:#ffffff; margin:4px 0 0; font-size:21px; font-weight:700;">Case Register Allocation Dispatch</h1>
          </td>
          <td style="text-align:right; vertical-align:middle;">
            <span style="display:inline-block; padding:5px 12px; background:rgba(37,99,235,0.22); border:1px solid rgba(96,165,250,0.35); border-radius:20px; color:#93c5fd; font-size:11.5px; font-weight:600;">
              Live System Export
            </span>
          </td>
        </tr>
      </table>
    </div>

    <!-- Body Content -->
    <div style="padding:26px 30px;">
      
      <p style="margin:0 0 8px; font-size:14px; color:#1e293b;">
        Dear <strong>%s</strong>,
      </p>
      <p style="margin:0 0 16px; font-size:12.5px; color:#475569; line-height:1.5;">
        Please find below the current allocation snapshot of case records assigned to you under your Area Manager profile. This dispatch reflects the filtered view from the Brain Bird Dashboard generated on <strong>%s</strong>.
      </p>

      %s

      <!-- KPI Summary Cards -->
      <table style="width:100%%; border-collapse:separate; border-spacing:8px 0; margin-bottom:20px;">
        <tr>
          <td style="width:25%%; padding:12px 14px; background:#f8fafc; border:1px solid #e2e8f0; border-radius:8px; text-align:center;">
            <div style="font-size:10px; font-weight:700; color:#64748b; text-transform:uppercase; letter-spacing:0.04em;">Total Cases</div>
            <div style="font-size:22px; font-weight:800; color:#1d4ed8; margin-top:2px;">%d</div>
          </td>
          <td style="width:25%%; padding:12px 14px; background:#fef2f2; border:1px solid #fecaca; border-radius:8px; text-align:center;">
            <div style="font-size:10px; font-weight:700; color:#991b1b; text-transform:uppercase; letter-spacing:0.04em;">Out of TAT</div>
            <div style="font-size:22px; font-weight:800; color:#dc2626; margin-top:2px;">%d</div>
          </td>
          <td style="width:25%%; padding:12px 14px; background:#fffbeb; border:1px solid #fde68a; border-radius:8px; text-align:center;">
            <div style="font-size:10px; font-weight:700; color:#92400e; text-transform:uppercase; letter-spacing:0.04em;">Open / Pending</div>
            <div style="font-size:22px; font-weight:800; color:#d97706; margin-top:2px;">%d</div>
          </td>
          <td style="width:25%%; padding:12px 14px; background:#f0fdf4; border:1px solid #bbf7d0; border-radius:8px; text-align:center;">
            <div style="font-size:10px; font-weight:700; color:#166534; text-transform:uppercase; letter-spacing:0.04em;">Resolved</div>
            <div style="font-size:22px; font-weight:800; color:#16a34a; margin-top:2px;">%d</div>
          </td>
        </tr>
      </table>

      <!-- Table Section -->
      <div style="border:1px solid #e2e8f0; border-radius:8px; overflow:hidden; margin-bottom:22px;">
        <table style="width:100%%; border-collapse:collapse; text-align:left;">
          <thead>
            <tr style="background:#f1f5f9; border-bottom:1px solid #cbd5e1;">
              <th style="padding:9px 12px; font-size:10px; font-weight:700; color:#475569; text-transform:uppercase; letter-spacing:0.04em;">Claim / Patient</th>
              <th style="padding:9px 12px; font-size:10px; font-weight:700; color:#475569; text-transform:uppercase; letter-spacing:0.04em;">Insurance / Type</th>
              <th style="padding:9px 12px; font-size:10px; font-weight:700; color:#475569; text-transform:uppercase; letter-spacing:0.04em;">Hospital</th>
              <th style="padding:9px 12px; font-size:10px; font-weight:700; color:#475569; text-transform:uppercase; letter-spacing:0.04em;">Allocated</th>
              <th style="padding:9px 12px; font-size:10px; font-weight:700; color:#475569; text-transform:uppercase; letter-spacing:0.04em;">TAT</th>
              <th style="padding:9px 12px; font-size:10px; font-weight:700; color:#475569; text-transform:uppercase; letter-spacing:0.04em;">Status</th>
            </tr>
          </thead>
          <tbody>
            %s
          </tbody>
        </table>
      </div>

      <!-- Action Button -->
      <div style="text-align:center; margin:24px 0 10px;">
        <a href="%s" style="display:inline-block; padding:12px 28px; background:#2563eb; color:#ffffff; font-size:13px; font-weight:600; text-decoration:none; border-radius:6px; box-shadow:0 4px 12px rgba(37,99,235,0.25);">
          Open Brain Bird Dashboard &rarr;
        </a>
      </div>

    </div>

    <!-- Footer -->
    <div style="background:#f8fafc; padding:18px 30px; border-top:1px solid #e2e8f0; text-align:center; font-size:11px; color:#64748b;">
      <p style="margin:0 0 4px;">This is an automated dispatch from the <strong>Brain Bird Dashboard</strong> for Area Manager record keeping.</p>
      <p style="margin:0; color:#94a3b8;">Confidential &bull; Brain Bird Healthcare Analytics &bull; All rights reserved</p>
    </div>

  </div>
</body>
</html>`,
		html.EscapeString(data.RecipientName),
		data.GeneratedAt,
		filterTags,
		data.TotalCases,
		data.OverdueCases,
		data.OpenCases,
		data.ClosedCases,
		rowsHTML.String(),
		data.DashboardURL,
	)
}

// handleMailAreaManager processes the filter request, fetches cases, builds email and dispatches it
func handleMailAreaManager(pool *sql.DB) fiber.Handler {
	return func(c *fiber.Ctx) error {
		session, ok := c.Locals("dashboardSession").(dashboardSession)
		if !ok {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "authentication required"})
		}
		var req MailAMRequest
		_ = c.BodyParser(&req)

		// Also check query params if not provided in body
		if req.AreaManagerID == "" {
			req.AreaManagerID = c.Query("am_id")
		}

		amIDStr := strings.TrimSpace(req.AreaManagerID)
		if amIDStr == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Please select an Area Manager from the filter first"})
		}

		amID, err := strconv.ParseInt(amIDStr, 10, 64)
		if err != nil || amID < 1 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid Area Manager selected"})
		}

		if pool == nil {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "database not configured"})
		}

		// Look up Area Manager name and email
		var amName, amEmail string
		err = pool.QueryRow("SELECT fullname, COALESCE(email, '') FROM users WHERE id = ? AND status = 'active' AND deleted_at IS NULL LIMIT 1", amID).Scan(&amName, &amEmail)
		if err != nil {
			if err == sql.ErrNoRows {
				return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Selected Area Manager was not found or is inactive"})
			}
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to look up Area Manager: " + err.Error()})
		}

		amEmail = strings.TrimSpace(amEmail)
		if amEmail == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": fmt.Sprintf("Area Manager %q does not have a registered email address in the system.", amName),
			})
		}

		// Build filter scope
		where, args, err := dashboardCaseScope(pool, session)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		}

		// Filter explicitly for this Area Manager
		where += " AND c.cm_id = ?"
		args = append(args, amID)

		var filterDetails []string
		filterDetails = append(filterDetails, "Area Manager: <strong>"+html.EscapeString(amName)+"</strong>")

		if value := strings.TrimSpace(req.ClaimNumber); value != "" {
			where += " AND (c.claim_number LIKE ? OR c.patient_name LIKE ?)"
			args = append(args, "%"+value+"%", "%"+value+"%")
			filterDetails = append(filterDetails, "Search: "+html.EscapeString(value))
		}
		if value := strings.TrimSpace(req.HospitalName); value != "" {
			where += " AND c.hospital_name LIKE ?"
			args = append(args, "%"+value+"%")
			filterDetails = append(filterDetails, "Hospital: "+html.EscapeString(value))
		}
		if value := strings.TrimSpace(req.InsuranceID); value != "" {
			where += " AND c.insurance_company_id = ?"
			args = append(args, value)
		}
		if value := strings.TrimSpace(req.TypeID); value != "" {
			where += " AND c.case_type_id = ?"
			args = append(args, value)
		}
		if value := strings.TrimSpace(req.SubTypeID); value != "" {
			where += " AND c.case_sub_type_id = ?"
			args = append(args, value)
		}
		if value := strings.TrimSpace(req.StateID); value != "" {
			where += " AND c.state_id = ?"
			args = append(args, value)
		}
		if value := strings.TrimSpace(req.CityID); value != "" {
			where += " AND c.city_id = ?"
			args = append(args, value)
		}
		if value := strings.TrimSpace(req.Date); value != "" {
			where += " AND DATE(COALESCE(c.allocation_date,c.created_at)) = ?"
			args = append(args, value)
			filterDetails = append(filterDetails, "Date: "+html.EscapeString(value))
		}
		if value := strings.TrimSpace(req.Month); value != "" {
			where += " AND DATE_FORMAT(COALESCE(c.allocation_date, c.created_at), '%Y-%m') = ?"
			args = append(args, value)
			filterDetails = append(filterDetails, "Month: "+html.EscapeString(value))
		}
		if req.Overdue == "1" {
			where += " AND c.is_completed = 0 AND TIMESTAMPDIFF(HOUR, COALESCE(c.allocation_date, c.created_at), NOW()) > COALESCE(NULLIF(t.tat_hours, 0), 48)"
			filterDetails = append(filterDetails, "Out of TAT Only")
		}

		switch strings.ToLower(req.TabStatus) {
		case "open":
			where += " AND c.is_completed = 0 AND (LOWER(c.status) = 'open' OR c.status = '' OR c.status IS NULL) AND (c.fo_id IS NULL OR c.fo_id = 0)"
			filterDetails = append(filterDetails, "Status: Open Cases")
		case "completed", "closed":
			where += " AND c.is_completed = 1"
			filterDetails = append(filterDetails, "Status: Completed Cases")
		default:
			filterDetails = append(filterDetails, "Status: All Register Cases")
		}

		if category := strings.ToLower(strings.TrimSpace(req.Category)); category != "" {
			switch category {
			case "reimbursement":
				where += " AND LOWER(t.type_name) LIKE '%reimbursement%'"
				filterDetails = append(filterDetails, "Category: Reimbursement")
			case "cashless":
				where += " AND LOWER(t.type_name) LIKE '%cashless%'"
				filterDetails = append(filterDetails, "Category: Cashless")
			case "other":
				where += " AND LOWER(t.type_name) NOT LIKE '%cashless%' AND LOWER(t.type_name) NOT LIKE '%reimbursement%'"
				filterDetails = append(filterDetails, "Category: Other")
			}
		}

		orderBy := "(c.is_completed = 0 AND TIMESTAMPDIFF(SECOND, COALESCE(c.allocation_date, c.created_at), NOW()) > (COALESCE(NULLIF(t.tat_hours, 0), 48) * 3600)) DESC, COALESCE(c.allocation_date, c.created_at) DESC, c.id DESC"

		query := `SELECT c.id, COALESCE(c.claim_number, ''), COALESCE(c.patient_name, ''),
			COALESCE(ic.company_name, ''), COALESCE(t.type_name, ''), COALESCE(st.sub_type_name, ''),
			COALESCE(c.hospital_name, ''), COALESCE(s.state_name, ''), COALESCE(d.district_name, ''),
			DATE_FORMAT(COALESCE(c.allocation_date, c.created_at), '%Y-%m-%d %H:%i'),
			TIMESTAMPDIFF(SECOND, COALESCE(c.allocation_date, c.created_at), COALESCE(c.report_submission_date, NOW())),
			COALESCE(NULLIF(t.tat_hours, 0), 48),
			LOWER(COALESCE(NULLIF(c.status, ''), 'open')), c.is_completed,
			COALESCE(c.case_verdict, ''), COALESCE(c.final_remark, '')
			FROM cases c
			LEFT JOIN master_insurance_companies ic ON c.insurance_company_id = ic.id
			LEFT JOIN master_case_types t ON c.case_type_id = t.id
			LEFT JOIN master_case_sub_types st ON c.case_sub_type_id = st.id
			LEFT JOIN master_states s ON c.state_id = s.id
			LEFT JOIN master_districts d ON c.city_id = d.id
			WHERE ` + where + ` ORDER BY ` + orderBy

		rows, err := pool.Query(query, args...)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to fetch cases: " + err.Error()})
		}
		defer rows.Close()

		var cases []emailCaseItem
		var totalCount, overdueCount, openCount, closedCount int

		for rows.Next() {
			var item emailCaseItem
			var caseID int64
			var status string
			var isCompleted bool

			if err := rows.Scan(
				&caseID, &item.ClaimNumber, &item.Patient,
				&item.Insurance, &item.CaseType, &item.SubType,
				&item.Hospital, &item.State, &item.City,
				&item.AllocatedAt, &item.ElapsedSec, &item.TATHours,
				&status, &isCompleted, &item.Verdict, &item.Remark,
			); err != nil {
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed scanning cases: " + err.Error()})
			}

			if item.ClaimNumber == "" {
				item.ClaimNumber = fmt.Sprintf("%d", caseID)
			}

			tatSeconds := int64(item.TATHours * 3600)
			item.IsOverdue = !isCompleted && item.ElapsedSec > tatSeconds
			if isCompleted {
				item.Status = "resolved"
				closedCount++
			} else {
				item.Status = status
				openCount++
			}
			if item.IsOverdue {
				overdueCount++
			}
			totalCount++
			cases = append(cases, item)
		}
		if err := rows.Err(); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		}

		dashboardURL := c.BaseURL() + "/dashboard"
		if !strings.HasPrefix(dashboardURL, "http") {
			dashboardURL = "http://" + c.Hostname() + "/dashboard"
		}

		emailPayload := areaManagerEmailPayload{
			RecipientName: amName,
			RecipientMail: amEmail,
			SenderName:    session.FullName,
			GeneratedAt:   time.Now().Format("02 Jan 2006, 03:04 PM MST"),
			FilterDetails: filterDetails,
			TotalCases:    totalCount,
			OverdueCases:  overdueCount,
			OpenCases:     openCount,
			ClosedCases:   closedCount,
			Cases:         cases,
			DashboardURL:  dashboardURL,
		}

		subject := fmt.Sprintf("[Brain Bird] Case Register Allocation - %s (%d Cases)", amName, totalCount)
		htmlBody := renderAreaManagerHTMLEmail(emailPayload)

		sendMode, err := SendHTMLEmail(amEmail, amName, subject, htmlBody)
		if err != nil {
			return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{
				"error": fmt.Sprintf("Failed delivering email to %s: %v", amEmail, err),
			})
		}

		msg := fmt.Sprintf("Mail successfully sent to %s (%s) with %d case records!", amName, amEmail, totalCount)
		if sendMode == "simulated" {
			msg = fmt.Sprintf("Mail dispatched for %s (%s) with %d case records (Template generated & ready for live SMTP)!", amName, amEmail, totalCount)
		}

		return c.JSON(fiber.Map{
			"success":       true,
			"message":       msg,
			"recipient":     amEmail,
			"recipientName": amName,
			"casesCount":    totalCount,
			"mode":          sendMode,
		})
	}
}

// renderSingleCaseHTMLEmail builds an executive HTML template for single-case follow-up
func renderSingleCaseHTMLEmail(c emailCaseItem, amName, amEmail, dashboardURL string) string {
	tatBadge := `<span style="display:inline-block; padding:2px 7px; border-radius:4px; font-size:10px; font-weight:600; color:#15803d; background:#dcfce7; border:1px solid #bbf7d0;">Within TAT</span>`
	if c.IsOverdue {
		tatBadge = `<span style="display:inline-block; padding:2px 7px; border-radius:4px; font-size:10px; font-weight:700; color:#b91c1c; background:#fee2e2; border:1px solid #fecaca;">OUT OF TAT</span>`
	}

	statusBadge := `<span style="display:inline-block; padding:2px 7px; border-radius:4px; font-size:10px; font-weight:600; text-transform:capitalize; color:#1d4ed8; background:#dbeafe;">` + html.EscapeString(c.Status) + `</span>`
	if c.Status == "resolved" || c.Status == "completed" {
		statusBadge = `<span style="display:inline-block; padding:2px 7px; border-radius:4px; font-size:10px; font-weight:600; text-transform:capitalize; color:#15803d; background:#dcfce7;">Resolved</span>`
	}

	catText := html.EscapeString(c.CaseType)
	if c.SubType != "" {
		catText += " / " + html.EscapeString(c.SubType)
	}

	locText := html.EscapeString(c.Hospital)
	if c.City != "" {
		locText += " (" + html.EscapeString(c.City)
		if c.State != "" {
			locText += ", " + html.EscapeString(c.State)
		}
		locText += ")"
	}

	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Brain Bird - Case Follow-up</title>
</head>
<body style="margin:0; padding:24px 12px; background-color:#f1f5f9; font-family:-apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif; color:#0f172a; line-height:1.45;">
  <div style="max-width:640px; margin:0 auto; background:#ffffff; border-radius:10px; overflow:hidden; border:1px solid #cbd5e1; box-shadow:0 10px 30px rgba(15,23,42,0.08);">
    
    <!-- Header -->
    <div style="background:linear-gradient(135deg, #0f172a 0%%, #1e293b 100%%); padding:24px 28px; border-top:4px solid #2563eb;">
      <table style="width:100%%; border-collapse:collapse;">
        <tr>
          <td style="vertical-align:middle;">
            <div style="color:#2563eb; font-size:11px; font-weight:800; letter-spacing:0.12em; text-transform:uppercase;">BRAIN BIRD MEDICAL CLAIMS</div>
            <h1 style="color:#ffffff; margin:4px 0 0; font-size:20px; font-weight:700;">Case Follow-up Dispatch</h1>
          </td>
          <td style="text-align:right; vertical-align:middle;">
            <span style="display:inline-block; padding:4px 10px; background:rgba(37,99,235,0.22); border:1px solid rgba(96,165,250,0.35); border-radius:16px; color:#93c5fd; font-size:11px; font-weight:600;">
              Claim #%s
            </span>
          </td>
        </tr>
      </table>
    </div>

    <!-- Body Content -->
    <div style="padding:24px 28px;">
      
      <p style="margin:0 0 8px; font-size:14px; color:#1e293b;">
        Dear <strong>%s</strong>,
      </p>
      <p style="margin:0 0 18px; font-size:12.5px; color:#475569; line-height:1.5;">
        Please review the following case record allocated to your operations queue in the Brain Bird portal:
      </p>

      <!-- Case Detail Table -->
      <table style="width:100%%; border-collapse:collapse; border:1px solid #e2e8f0; border-radius:8px; overflow:hidden; margin-bottom:20px;">
        <tr style="background:#f8fafc; border-bottom:1px solid #e2e8f0;">
          <td style="padding:10px 14px; font-size:11.5px; font-weight:600; color:#64748b; width:140px;">Claim Number</td>
          <td style="padding:10px 14px; font-size:12.5px; font-weight:700; color:#1d4ed8;">%s</td>
        </tr>
        <tr style="border-bottom:1px solid #e2e8f0;">
          <td style="padding:10px 14px; font-size:11.5px; font-weight:600; color:#64748b;">Patient Name</td>
          <td style="padding:10px 14px; font-size:12px; color:#0f172a; font-weight:600;">%s</td>
        </tr>
        <tr style="background:#f8fafc; border-bottom:1px solid #e2e8f0;">
          <td style="padding:10px 14px; font-size:11.5px; font-weight:600; color:#64748b;">Insurance Company</td>
          <td style="padding:10px 14px; font-size:12px; color:#0f172a;">%s</td>
        </tr>
        <tr style="border-bottom:1px solid #e2e8f0;">
          <td style="padding:10px 14px; font-size:11.5px; font-weight:600; color:#64748b;">Category / Sub-type</td>
          <td style="padding:10px 14px; font-size:12px; color:#334155;">%s</td>
        </tr>
        <tr style="background:#f8fafc; border-bottom:1px solid #e2e8f0;">
          <td style="padding:10px 14px; font-size:11.5px; font-weight:600; color:#64748b;">Hospital / Location</td>
          <td style="padding:10px 14px; font-size:12px; color:#334155;">%s</td>
        </tr>
        <tr style="border-bottom:1px solid #e2e8f0;">
          <td style="padding:10px 14px; font-size:11.5px; font-weight:600; color:#64748b;">Allocation Date</td>
          <td style="padding:10px 14px; font-size:12px; color:#475569;">%s</td>
        </tr>
        <tr style="background:#f8fafc; border-bottom:1px solid #e2e8f0;">
          <td style="padding:10px 14px; font-size:11.5px; font-weight:600; color:#64748b;">TAT Status</td>
          <td style="padding:10px 14px; font-size:12px;">%s <span style="color:#64748b; font-size:11px; margin-left:6px;">(%d hrs allocated)</span></td>
        </tr>
        <tr style="border-bottom:1px solid #e2e8f0;">
          <td style="padding:10px 14px; font-size:11.5px; font-weight:600; color:#64748b;">Current Status</td>
          <td style="padding:10px 14px; font-size:12px;">%s</td>
        </tr>
        <tr style="background:#f8fafc;">
          <td style="padding:10px 14px; font-size:11.5px; font-weight:600; color:#64748b;">Remark / Verdict</td>
          <td style="padding:10px 14px; font-size:12px; color:#334155;">%s</td>
        </tr>
      </table>

      <!-- Action Button -->
      <div style="text-align:center; margin:22px 0 8px;">
        <a href="%s" style="display:inline-block; padding:12px 28px; background:#2563eb; color:#ffffff; font-size:13px; font-weight:600; text-decoration:none; border-radius:6px; box-shadow:0 4px 12px rgba(37,99,235,0.25);">
          Open Case in Brain Bird Dashboard &rarr;
        </a>
      </div>

    </div>

    <!-- Footer -->
    <div style="background:#f8fafc; padding:16px 28px; border-top:1px solid #e2e8f0; text-align:center; font-size:11px; color:#64748b;">
      <p style="margin:0 0 3px;">Dispatched from <strong>admin@brainbirdconsulting.co.in</strong> via Brain Bird Operations.</p>
      <p style="margin:0; color:#94a3b8;">Confidential &bull; Brain Bird Healthcare Analytics &bull; All rights reserved</p>
    </div>

  </div>
</body>
</html>`,
		html.EscapeString(c.ClaimNumber),
		html.EscapeString(amName),
		html.EscapeString(c.ClaimNumber),
		html.EscapeString(c.Patient),
		html.EscapeString(c.Insurance),
		catText,
		locText,
		html.EscapeString(c.AllocatedAt),
		tatBadge,
		c.TATHours,
		statusBadge,
		func() string {
			text := c.Verdict
			if c.Remark != "" {
				if text != "" {
					text += " — " + c.Remark
				} else {
					text = c.Remark
				}
			}
			if text == "" {
				text = "—"
			}
			return html.EscapeString(text)
		}(),
		dashboardURL,
	)
}

// handleMailSingleCaseAM dispatches an email for a specific case to its Area Manager
func handleMailSingleCaseAM(pool *sql.DB) fiber.Handler {
	return func(c *fiber.Ctx) error {
		_, ok := c.Locals("dashboardSession").(dashboardSession)
		if !ok {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "authentication required"})
		}
		caseIDStr := c.Params("id")
		caseID, err := strconv.ParseInt(caseIDStr, 10, 64)
		if err != nil || caseID < 1 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid case id"})
		}
		if pool == nil {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "database not configured"})
		}

		var req struct {
			Email string `json:"email"`
		}
		_ = c.BodyParser(&req)

		query := `SELECT c.id, COALESCE(c.claim_number, ''), COALESCE(c.patient_name, ''),
			COALESCE(ic.company_name, ''), COALESCE(t.type_name, ''), COALESCE(st.sub_type_name, ''),
			COALESCE(c.hospital_name, ''), COALESCE(s.state_name, ''), COALESCE(d.district_name, ''),
			DATE_FORMAT(COALESCE(c.allocation_date, c.created_at), '%Y-%m-%d %H:%i'),
			TIMESTAMPDIFF(SECOND, COALESCE(c.allocation_date, c.created_at), COALESCE(c.report_submission_date, NOW())),
			COALESCE(NULLIF(t.tat_hours, 0), 48),
			LOWER(COALESCE(NULLIF(c.status, ''), 'open')), c.is_completed,
			COALESCE(c.case_verdict, ''), COALESCE(c.final_remark, ''),
			COALESCE(am.fullname, 'Area Manager'), COALESCE(am.email, '')
			FROM cases c
			LEFT JOIN master_insurance_companies ic ON c.insurance_company_id = ic.id
			LEFT JOIN master_case_types t ON c.case_type_id = t.id
			LEFT JOIN master_case_sub_types st ON c.case_sub_type_id = st.id
			LEFT JOIN master_states s ON c.state_id = s.id
			LEFT JOIN master_districts d ON c.city_id = d.id
			LEFT JOIN users am ON c.cm_id = am.id
			WHERE c.id = ? AND c.deleted_at IS NULL LIMIT 1`

		var item emailCaseItem
		var amName, amEmail string
		var isCompleted bool
		var status string

		err = pool.QueryRow(query, caseID).Scan(
			&caseID, &item.ClaimNumber, &item.Patient,
			&item.Insurance, &item.CaseType, &item.SubType,
			&item.Hospital, &item.State, &item.City,
			&item.AllocatedAt, &item.ElapsedSec, &item.TATHours,
			&status, &isCompleted, &item.Verdict, &item.Remark,
			&amName, &amEmail,
		)
		if err != nil {
			if err == sql.ErrNoRows {
				return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Case not found"})
			}
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed loading case: " + err.Error()})
		}

		if strings.TrimSpace(req.Email) != "" {
			amEmail = strings.TrimSpace(req.Email)
		} else {
			amEmail = strings.TrimSpace(amEmail)
		}

		if amEmail == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": fmt.Sprintf("Area Manager %q does not have a registered email address. Please enter an email.", amName),
			})
		}

		if item.ClaimNumber == "" {
			item.ClaimNumber = fmt.Sprintf("%d", caseID)
		}

		tatSeconds := int64(item.TATHours * 3600)
		item.IsOverdue = !isCompleted && item.ElapsedSec > tatSeconds
		if isCompleted {
			item.Status = "resolved"
		} else {
			item.Status = status
		}

		dashboardURL := c.BaseURL() + "/dashboard"
		if !strings.HasPrefix(dashboardURL, "http") {
			dashboardURL = "http://" + c.Hostname() + "/dashboard"
		}

		subject := fmt.Sprintf("[Brain Bird] Case Follow-up: Claim #%s (%s)", item.ClaimNumber, item.Patient)
		htmlBody := renderSingleCaseHTMLEmail(item, amName, amEmail, dashboardURL)

		sendMode, err := SendHTMLEmail(amEmail, amName, subject, htmlBody)
		if err != nil {
			return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{
				"error": fmt.Sprintf("Failed delivering email to %s: %v", amEmail, err),
			})
		}

		msg := fmt.Sprintf("Case #%s mailed to %s (%s) successfully from admin@brainbirdconsulting.co.in!", item.ClaimNumber, amName, amEmail)
		if sendMode == "simulated" {
			msg = fmt.Sprintf("Case #%s mailed to %s (%s) (Simulated in Dev Mode; ready for live SMTP)!", item.ClaimNumber, amName, amEmail)
		}

		return c.JSON(fiber.Map{
			"success":   true,
			"message":   msg,
			"recipient": amEmail,
			"claim":     item.ClaimNumber,
			"mode":      sendMode,
		})
	}
}

