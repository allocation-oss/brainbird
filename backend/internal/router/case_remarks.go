package router

import (
	"database/sql"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gofiber/fiber/v2"
)

type caseRemark struct {
	Author    string `json:"author"`
	Remark    string `json:"remark"`
	UpdatedAt string `json:"updatedAt"`
}

func canViewAllCaseRemarks(roleID int) bool {
	return roleID == 1 || roleID == 2
}

func ensureDashboardCaseRemarksTable(pool *sql.DB) error {
	_, err := pool.Exec(`CREATE TABLE IF NOT EXISTS dashboard_case_remarks (
		id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
		case_id INT NOT NULL,
		user_id INT NOT NULL,
		remark TEXT NOT NULL,
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
		UNIQUE KEY uq_dashboard_case_remarks_case_user (case_id, user_id),
		KEY idx_dashboard_case_remarks_user (user_id)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`)
	return err
}

func loadDashboardCaseRemarks(pool *sql.DB, session dashboardSession, cases []CaseRow) error {
	if len(cases) == 0 {
		return nil
	}
	if err := ensureDashboardCaseRemarksTable(pool); err != nil {
		return err
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(cases)), ",")
	args := make([]any, 0, len(cases)+1)
	caseIndexes := make(map[int64]int, len(cases))
	for index, row := range cases {
		args = append(args, row.CaseID)
		caseIndexes[row.CaseID] = index
	}
	query := `SELECT r.case_id, r.user_id, COALESCE(u.fullname,''), r.remark, DATE_FORMAT(r.updated_at,'%Y-%m-%d %H:%i:%s')
		FROM dashboard_case_remarks r LEFT JOIN users u ON u.id = r.user_id
		WHERE r.case_id IN (` + placeholders + `)`
	if !canViewAllCaseRemarks(session.RoleID) {
		query += " AND r.user_id = ?"
		args = append(args, session.UserID)
	}
	query += " ORDER BY r.updated_at DESC"
	rows, err := pool.Query(query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var caseID, userID int64
		var item caseRemark
		if err := rows.Scan(&caseID, &userID, &item.Author, &item.Remark, &item.UpdatedAt); err != nil {
			return err
		}
		index, ok := caseIndexes[caseID]
		if !ok {
			continue
		}
		cases[index].Remarks = append(cases[index].Remarks, item)
		if userID == session.UserID {
			cases[index].OwnRemark = item.Remark
		}
	}
	return rows.Err()
}

func registerDashboardCaseRemarksAPI(api fiber.Router, pool *sql.DB, requireSession fiber.Handler) {
	api.Post("/cases/:id/remarks", requireSession, func(c *fiber.Ctx) error {
		if pool == nil {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "case remarks unavailable"})
		}
		session, ok := c.Locals("dashboardSession").(dashboardSession)
		if !ok {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "authentication required"})
		}
		caseID, err := strconv.ParseInt(c.Params("id"), 10, 64)
		if err != nil || caseID < 1 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid case id"})
		}
		var request struct {
			Remark string `json:"remark"`
		}
		if err := c.BodyParser(&request); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid remark request"})
		}
		request.Remark = strings.TrimSpace(request.Remark)
		if request.Remark == "" || utf8.RuneCountInString(request.Remark) > 5000 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "remark must contain 1 to 5000 characters"})
		}
		if err := ensureDashboardCaseRemarksTable(pool); err != nil {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "case remarks unavailable"})
		}
		where, args, err := dashboardCaseScope(pool, session)
		if err != nil {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "case scope unavailable"})
		}
		args = append(args, caseID)
		var visibleCaseID int64
		if err := pool.QueryRow("SELECT c.id FROM cases c WHERE "+where+" AND c.id = ? LIMIT 1", args...).Scan(&visibleCaseID); err != nil {
			if err == sql.ErrNoRows {
				return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "case not found"})
			}
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "case lookup failed"})
		}
		_, err = pool.Exec(`INSERT INTO dashboard_case_remarks (case_id, user_id, remark)
			VALUES (?, ?, ?) ON DUPLICATE KEY UPDATE remark = VALUES(remark), updated_at = CURRENT_TIMESTAMP`, caseID, session.UserID, request.Remark)
		if err != nil {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "remark could not be saved"})
		}
		return c.JSON(fiber.Map{"status": "ok", "caseId": visibleCaseID})
	})
}
