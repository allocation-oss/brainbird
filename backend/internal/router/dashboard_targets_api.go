package router

import (
	"database/sql"
	"fmt"
	"sort"
	"time"

	"github.com/gofiber/fiber/v2"
)

type targetTotals struct {
	Current                  int `json:"current"`
	Projected                int `json:"projected"`
	PreviousMTD              int `json:"previousMtd"`
	PreviousMonth            int `json:"previousMonth"`
	Reimbursement            int `json:"reimbursement"`
	ReimbursementPreviousMTD int `json:"reimbursementPreviousMtd"`
	Cashless                 int `json:"cashless"`
	CashlessPreviousMTD      int `json:"cashlessPreviousMtd"`
	Other                    int `json:"other"`
	OtherPreviousMTD         int `json:"otherPreviousMtd"`
}

type managerMovement struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Insurance string `json:"insurance"`
	Current   int    `json:"current"`
	Previous  int    `json:"previous"`
	Delta     int    `json:"delta"`
}

type dashboardTargets struct {
	Allocation         targetTotals      `json:"allocation"`
	Closure            targetTotals      `json:"closure"`
	AllocationManagers []managerMovement `json:"allocationManagers"`
	ReportMakers       []managerMovement `json:"reportMakers"`
}

type dashboardFilterOption struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Email    string `json:"email,omitempty"`
	ParentID int    `json:"parentId,omitempty"`
}

type dashboardFilterOptions struct {
	Insurances         []dashboardFilterOption `json:"insurances"`
	Types              []dashboardFilterOption `json:"types"`
	SubTypes           []dashboardFilterOption `json:"subTypes"`
	States             []dashboardFilterOption `json:"states"`
	Cities             []dashboardFilterOption `json:"cities"`
	AreaManagers       []dashboardFilterOption `json:"areaManagers"`
	AllocationManagers []dashboardFilterOption `json:"allocationManagers"`
	FieldOfficers      []dashboardFilterOption `json:"fieldOfficers"`
}

func buildDashboardTargets(pool *sql.DB, session dashboardSession) (dashboardTargets, error) {
	where, args, err := dashboardCaseScope(pool, session)
	if err != nil {
		return dashboardTargets{}, err
	}
	var today time.Time
	if err := pool.QueryRow("SELECT CURDATE()").Scan(&today); err != nil {
		return dashboardTargets{}, err
	}
	monthStart := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, today.Location())
	previousStart := monthStart.AddDate(0, -1, 0)
	previousEnd := previousStart.AddDate(0, 0, today.Day()-1)
	previousLastDay := previousStart.AddDate(0, 1, -1)
	currentLastDay := monthStart.AddDate(0, 1, -1)
	if previousEnd.After(previousLastDay) {
		previousEnd = previousLastDay
	}
	monthKey := monthStart.Format("2006-01")
	previousKey := previousStart.Format("2006-01")
	previousStartText := previousStart.Format("2006-01-02")
	previousEndText := previousEnd.Format("2006-01-02")

	var result dashboardTargets
	allocationSQL := fmt.Sprintf(`SELECT
		COALESCE(SUM(DATE_FORMAT(COALESCE(c.allocation_date,c.created_at),'%%Y-%%m')='%s'),0),
		COALESCE(SUM(DATE_FORMAT(COALESCE(c.allocation_date,c.created_at),'%%Y-%%m')='%s'),0),
		COALESCE(SUM(DATE(COALESCE(c.allocation_date,c.created_at)) BETWEEN '%s' AND '%s'),0),
		COALESCE(SUM(DATE_FORMAT(COALESCE(c.allocation_date,c.created_at),'%%Y-%%m')='%s' AND LOWER(t.type_name) LIKE '%%reimbursement%%'),0),
		COALESCE(SUM(DATE(COALESCE(c.allocation_date,c.created_at)) BETWEEN '%s' AND '%s' AND LOWER(t.type_name) LIKE '%%reimbursement%%'),0),
		COALESCE(SUM(DATE_FORMAT(COALESCE(c.allocation_date,c.created_at),'%%Y-%%m')='%s' AND LOWER(t.type_name) LIKE '%%cashless%%'),0),
		COALESCE(SUM(DATE(COALESCE(c.allocation_date,c.created_at)) BETWEEN '%s' AND '%s' AND LOWER(t.type_name) LIKE '%%cashless%%'),0),
		COALESCE(SUM(DATE_FORMAT(COALESCE(c.allocation_date,c.created_at),'%%Y-%%m')='%s' AND LOWER(t.type_name) NOT LIKE '%%cashless%%' AND LOWER(t.type_name) NOT LIKE '%%reimbursement%%'),0),
		COALESCE(SUM(DATE(COALESCE(c.allocation_date,c.created_at)) BETWEEN '%s' AND '%s' AND LOWER(t.type_name) NOT LIKE '%%cashless%%' AND LOWER(t.type_name) NOT LIKE '%%reimbursement%%'),0)
		FROM cases c LEFT JOIN master_case_types t ON c.case_type_id=t.id WHERE %s`, monthKey, previousKey, previousStartText, previousEndText, monthKey, previousStartText, previousEndText, monthKey, previousStartText, previousEndText, monthKey, previousStartText, previousEndText, where)
	if err := pool.QueryRow(allocationSQL, args...).Scan(&result.Allocation.Current, &result.Allocation.PreviousMonth, &result.Allocation.PreviousMTD, &result.Allocation.Reimbursement, &result.Allocation.ReimbursementPreviousMTD, &result.Allocation.Cashless, &result.Allocation.CashlessPreviousMTD, &result.Allocation.Other, &result.Allocation.OtherPreviousMTD); err != nil {
		return dashboardTargets{}, err
	}
	if today.Day() > 0 {
		result.Allocation.Projected = (result.Allocation.Current*currentLastDay.Day() + today.Day() - 1) / today.Day()
	}

	closedWhere := where + " AND c.is_completed=1"
	closedDate := "COALESCE(NULLIF(c.report_submission_date,'0000-00-00 00:00:00'),c.updated_at,c.created_at)"
	closureSQL := fmt.Sprintf(`SELECT
		COALESCE(SUM(DATE_FORMAT(%s,'%%Y-%%m')='%s'),0),
		COALESCE(SUM(DATE_FORMAT(%s,'%%Y-%%m')='%s'),0),
		COALESCE(SUM(DATE(%s) BETWEEN '%s' AND '%s'),0),
		COALESCE(SUM(DATE_FORMAT(%s,'%%Y-%%m')='%s' AND LOWER(t.type_name) LIKE '%%reimbursement%%'),0),
		COALESCE(SUM(DATE(%s) BETWEEN '%s' AND '%s' AND LOWER(t.type_name) LIKE '%%reimbursement%%'),0),
		COALESCE(SUM(DATE_FORMAT(%s,'%%Y-%%m')='%s' AND LOWER(t.type_name) LIKE '%%cashless%%'),0),
		COALESCE(SUM(DATE(%s) BETWEEN '%s' AND '%s' AND LOWER(t.type_name) LIKE '%%cashless%%'),0),
		COALESCE(SUM(DATE_FORMAT(%s,'%%Y-%%m')='%s' AND LOWER(t.type_name) NOT LIKE '%%cashless%%' AND LOWER(t.type_name) NOT LIKE '%%reimbursement%%'),0),
		COALESCE(SUM(DATE(%s) BETWEEN '%s' AND '%s' AND LOWER(t.type_name) NOT LIKE '%%cashless%%' AND LOWER(t.type_name) NOT LIKE '%%reimbursement%%'),0)
		FROM cases c LEFT JOIN master_case_types t ON c.case_type_id=t.id WHERE %s`, closedDate, monthKey, closedDate, previousKey, closedDate, previousStartText, previousEndText, closedDate, monthKey, closedDate, previousStartText, previousEndText, closedDate, monthKey, closedDate, previousStartText, previousEndText, closedDate, monthKey, closedDate, previousStartText, previousEndText, closedWhere)
	if err := pool.QueryRow(closureSQL, args...).Scan(&result.Closure.Current, &result.Closure.PreviousMonth, &result.Closure.PreviousMTD, &result.Closure.Reimbursement, &result.Closure.ReimbursementPreviousMTD, &result.Closure.Cashless, &result.Closure.CashlessPreviousMTD, &result.Closure.Other, &result.Closure.OtherPreviousMTD); err != nil {
		return dashboardTargets{}, err
	}
	if today.Day() > 0 {
		result.Closure.Projected = (result.Closure.Current*currentLastDay.Day() + today.Day() - 1) / today.Day()
	}

	result.AllocationManagers, err = queryManagerMovement(pool, where, args, "allocation_manager_id", "COALESCE(c.allocation_date,c.created_at)", monthKey, previousStartText, previousEndText)
	if err != nil {
		return dashboardTargets{}, err
	}
	result.ReportMakers, err = queryManagerMovement(pool, closedWhere, args, "report_maker_id", closedDate, monthKey, previousStartText, previousEndText)
	if err != nil {
		return dashboardTargets{}, err
	}
	return result, nil
}

func queryManagerMovement(pool *sql.DB, where string, args []any, userColumn, dateExpression, monthKey, previousStart, previousEnd string) ([]managerMovement, error) {
	query := fmt.Sprintf(`SELECT u.id, u.fullname, COALESCE(GROUP_CONCAT(DISTINCT ic.company_name ORDER BY ic.company_name SEPARATOR ', '),''),
		COALESCE(SUM(DATE_FORMAT(%s,'%%Y-%%m')='%s'),0) AS current_count, COALESCE(SUM(DATE(%s) BETWEEN '%s' AND '%s'),0) AS previous_count
		FROM cases c JOIN users u ON c.%s=u.id
		LEFT JOIN master_insurance_companies ic ON c.insurance_company_id=ic.id
		WHERE %s AND c.%s IS NOT NULL AND c.%s>0 AND DATE(%s) BETWEEN '%s' AND CURDATE()
		GROUP BY u.id,u.fullname`, dateExpression, monthKey, dateExpression, previousStart, previousEnd, userColumn, where, userColumn, userColumn, dateExpression, previousStart)
	rows, err := pool.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	movements := make([]managerMovement, 0, 6)
	for rows.Next() {
		var item managerMovement
		if err := rows.Scan(&item.ID, &item.Name, &item.Insurance, &item.Current, &item.Previous); err != nil {
			return nil, err
		}
		item.Delta = item.Current - item.Previous
		movements = append(movements, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(movements, func(first, second int) bool {
		firstMovement := movements[first].Delta
		if firstMovement < 0 {
			firstMovement = -firstMovement
		}
		secondMovement := movements[second].Delta
		if secondMovement < 0 {
			secondMovement = -secondMovement
		}
		return firstMovement > secondMovement
	})
	return movements, nil
}

func registerDashboardTargetsAPI(api fiber.Router, pool *sql.DB, requireSession fiber.Handler) {
	api.Get("/dashboard/filters", requireSession, func(c *fiber.Ctx) error {
		session, ok := c.Locals("dashboardSession").(dashboardSession)
		if !ok {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "authentication required"})
		}
		filters, err := buildDashboardFilterOptions(pool, session, c.Query("state_id"))
		if err != nil {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "dashboard filters unavailable"})
		}
		return c.JSON(filters)
	})
	api.Get("/dashboard/targets", requireSession, func(c *fiber.Ctx) error {
		session, ok := c.Locals("dashboardSession").(dashboardSession)
		if !ok {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "authentication required"})
		}
		payload, err := buildDashboardTargets(pool, session)
		if err != nil {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "dashboard target data unavailable"})
		}
		return c.JSON(payload)
	})
}

func buildDashboardFilterOptions(pool *sql.DB, session dashboardSession, stateID string) (dashboardFilterOptions, error) {
	where, args, err := dashboardCaseScope(pool, session)
	if err != nil {
		return dashboardFilterOptions{}, err
	}
	options := dashboardFilterOptions{}
	queries := []struct {
		destination *[]dashboardFilterOption
		query       string
		args        []any
	}{
		{&options.Insurances, `SELECT DISTINCT ic.id, ic.company_name FROM master_insurance_companies ic JOIN cases c ON c.insurance_company_id=ic.id WHERE ic.status='active' AND ` + where + ` ORDER BY ic.company_name`, args},
		{&options.States, `SELECT DISTINCT s.id, s.state_name FROM master_states s JOIN cases c ON c.state_id=s.id WHERE ` + where + ` ORDER BY s.state_name`, args},
		{&options.AllocationManagers, `SELECT DISTINCT u.id, u.fullname FROM users u JOIN cases c ON c.allocation_manager_id=u.id WHERE u.status='active' AND ` + where + ` ORDER BY u.fullname`, args},
		{&options.FieldOfficers, `SELECT DISTINCT u.id, u.fullname FROM users u WHERE u.role_id = 6 AND u.status='active' AND u.deleted_at IS NULL ORDER BY u.fullname`, nil},
	}
	for _, item := range queries {
		rows, err := pool.Query(item.query, item.args...)
		if err != nil {
			return dashboardFilterOptions{}, err
		}
		for rows.Next() {
			var option dashboardFilterOption
			if err := rows.Scan(&option.ID, &option.Name); err != nil {
				rows.Close()
				return dashboardFilterOptions{}, err
			}
			*item.destination = append(*item.destination, option)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return dashboardFilterOptions{}, err
		}
		if err := rows.Close(); err != nil {
			return dashboardFilterOptions{}, err
		}
	}
	areaManagerRows, err := pool.Query(`SELECT DISTINCT u.id, u.fullname, COALESCE(u.email,'') FROM users u JOIN cases c ON c.cm_id=u.id WHERE u.status='active' AND `+where+` ORDER BY u.fullname`, args...)
	if err != nil {
		return dashboardFilterOptions{}, err
	}
	for areaManagerRows.Next() {
		var option dashboardFilterOption
		if err := areaManagerRows.Scan(&option.ID, &option.Name, &option.Email); err != nil {
			areaManagerRows.Close()
			return dashboardFilterOptions{}, err
		}
		options.AreaManagers = append(options.AreaManagers, option)
	}
	if err := areaManagerRows.Err(); err != nil {
		areaManagerRows.Close()
		return dashboardFilterOptions{}, err
	}
	if err := areaManagerRows.Close(); err != nil {
		return dashboardFilterOptions{}, err
	}
	typeRows, err := pool.Query(`SELECT DISTINCT t.id, t.type_name, t.insurance_company_id FROM master_case_types t JOIN cases c ON c.case_type_id=t.id WHERE t.status='active' AND `+where+` ORDER BY t.type_name`, args...)
	if err != nil {
		return dashboardFilterOptions{}, err
	}
	for typeRows.Next() {
		var option dashboardFilterOption
		if err := typeRows.Scan(&option.ID, &option.Name, &option.ParentID); err != nil {
			typeRows.Close()
			return dashboardFilterOptions{}, err
		}
		options.Types = append(options.Types, option)
	}
	if err := typeRows.Err(); err != nil {
		typeRows.Close()
		return dashboardFilterOptions{}, err
	}
	if err := typeRows.Close(); err != nil {
		return dashboardFilterOptions{}, err
	}
	subTypeRows, err := pool.Query(`SELECT DISTINCT st.id, st.sub_type_name, st.case_type_id FROM master_case_sub_types st JOIN cases c ON c.case_sub_type_id=st.id WHERE st.status='active' AND `+where+` ORDER BY st.sub_type_name`, args...)
	if err != nil {
		return dashboardFilterOptions{}, err
	}
	for subTypeRows.Next() {
		var option dashboardFilterOption
		if err := subTypeRows.Scan(&option.ID, &option.Name, &option.ParentID); err != nil {
			subTypeRows.Close()
			return dashboardFilterOptions{}, err
		}
		options.SubTypes = append(options.SubTypes, option)
	}
	if err := subTypeRows.Err(); err != nil {
		subTypeRows.Close()
		return dashboardFilterOptions{}, err
	}
	if err := subTypeRows.Close(); err != nil {
		return dashboardFilterOptions{}, err
	}
	cityQuery := `SELECT DISTINCT d.id, d.district_name, d.state_id FROM master_districts d JOIN cases c ON c.city_id=d.id WHERE ` + where
	cityArgs := append([]any{}, args...)
	if stateID != "" {
		cityQuery += " AND d.state_id = ?"
		cityArgs = append(cityArgs, stateID)
	}
	cityQuery += " ORDER BY d.district_name"
	rows, err := pool.Query(cityQuery, cityArgs...)
	if err != nil {
		return dashboardFilterOptions{}, err
	}
	for rows.Next() {
		var option dashboardFilterOption
		if err := rows.Scan(&option.ID, &option.Name, &option.ParentID); err != nil {
			rows.Close()
			return dashboardFilterOptions{}, err
		}
		options.Cities = append(options.Cities, option)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return dashboardFilterOptions{}, err
	}
	if err := rows.Close(); err != nil {
		return dashboardFilterOptions{}, err
	}
	return options, nil
}
