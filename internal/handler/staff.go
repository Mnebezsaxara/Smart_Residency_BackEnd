package handler

import (
	"context"
	"crypto/rand"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

// generateTempPassword returns 8 chars from an unambiguous alphabet
// (no 0/O/1/l/I) so admins can read it aloud over the phone.
const tempPasswordAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789"

func generateTempPassword() (string, error) {
	out := make([]byte, 8)
	max := big.NewInt(int64(len(tempPasswordAlphabet)))
	for i := range out {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		out[i] = tempPasswordAlphabet[n.Int64()]
	}
	return string(out), nil
}

type StaffHandler struct{ db *pgxpool.Pool }

func NewStaffHandler(db *pgxpool.Pool) *StaffHandler {
	return &StaffHandler{db: db}
}

// allowedSpecialties mirrors the values Flutter sends from admin_staff_page.dart.
var allowedSpecialties = map[string]bool{
	"plumbing":   true,
	"electrical": true,
	"cleaning":   true,
	"garbage":    true,
	"intercom":   true,
	"elevator":   true,
	"security":   true,
}

type staffMember struct {
	ID          string `json:"id"`
	FullName    string `json:"full_name"`
	Email       string `json:"email"`
	Phone       string `json:"phone"`
	Specialty   string `json:"specialty"`
	InProgress  int    `json:"in_progress_count"`
	DoneToday   int    `json:"done_today_count"`
	LastTakenAt *time.Time `json:"last_taken_at,omitempty"`
}

// publicStaffMember is the shape returned by GET /staff (visible to any
// authenticated user, used by the resident "Services" page).
type publicStaffMember struct {
	ID            string `json:"id"`
	FullName      string `json:"full_name"`
	Email         string `json:"email"`
	Phone         string `json:"phone"`
	Specialty     string `json:"specialty"`
	WorkSchedule  string `json:"work_schedule"`
	Description   string `json:"description"`
	IsAvailable   bool   `json:"is_available"`
}

// POST /admin/staff — create a new staff account (admin only).
// The backend always generates the temporary password itself and returns it
// in the response so the admin can hand it to the new account. The account
// is forced to change the password on first login.
//
// Specialty is required and must be one of allowedSpecialties. Security guards
// are ordinary staff with specialty='security' (the "Охранник" picker maps onto
// this value).
type createStaffReq struct {
	FullName     string `json:"full_name"     binding:"required"`
	Email        string `json:"email"         binding:"required,email"`
	Phone        string `json:"phone"         binding:"required"`
	Specialty    string `json:"specialty"     binding:"required"`
	WorkSchedule string `json:"work_schedule"`
	Description  string `json:"description"`
}

type createStaffResp struct {
	publicStaffMember
	TemporaryPassword string `json:"temporary_password"`
}

func (h *StaffHandler) Create(c *gin.Context) {
	if c.GetString("user_role") != "admin" {
		forbiddenAccess(c, "admin only")
		return
	}

	var req createStaffReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	specialty := strings.ToLower(strings.TrimSpace(req.Specialty))
	if specialty == "" || !allowedSpecialties[specialty] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported or missing specialty for staff"})
		return
	}

	tempPassword, err := generateTempPassword()
	if err != nil {
		internalError(c, "Staff.Create/genpass", err)
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(tempPassword), bcrypt.DefaultCost)
	if err != nil {
		internalError(c, "Staff.Create/bcrypt", err)
		return
	}

	ctx := context.Background()
	tx, err := h.db.Begin(ctx)
	if err != nil {
		internalError(c, "Staff.Create/begin", err)
		return
	}
	defer tx.Rollback(ctx)

	userID := uuid.New().String()
	email := strings.ToLower(strings.TrimSpace(req.Email))

	if _, err := tx.Exec(ctx,
		`INSERT INTO users (id, email, password_hash) VALUES ($1, $2, $3)`,
		userID, email, string(hash),
	); err != nil {
		if strings.Contains(err.Error(), "23505") || strings.Contains(err.Error(), "duplicate") {
			c.JSON(http.StatusConflict, gin.H{"error": "email already registered"})
			return
		}
		internalError(c, "Staff.Create/users", err)
		return
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO profiles (
			id, full_name, email, phone,
			role, specialty, work_schedule, description,
			verification_status, password_change_required
		) VALUES ($1, $2, $3, $4, 'staff', $5, $6, $7, 'approved', TRUE)`,
		userID, req.FullName, email, req.Phone,
		specialty, req.WorkSchedule, req.Description,
	); err != nil {
		internalError(c, "Staff.Create/profiles", err)
		return
	}

	if err := tx.Commit(ctx); err != nil {
		internalError(c, "Staff.Create/commit", err)
		return
	}

	c.JSON(http.StatusCreated, createStaffResp{
		publicStaffMember: publicStaffMember{
			ID:           userID,
			FullName:     req.FullName,
			Email:        email,
			Phone:        req.Phone,
			Specialty:    specialty,
			WorkSchedule: req.WorkSchedule,
			Description:  req.Description,
			IsAvailable:  true,
		},
		TemporaryPassword: tempPassword,
	})
}

// GET /staff — public list visible to any authenticated user.
// Drives the resident "Services" page. Lists all staff with their specialty;
// security guards appear here as ordinary staff with specialty='security'.
func (h *StaffHandler) PublicList(c *gin.Context) {
	rows, err := h.db.Query(context.Background(), `
		SELECT
			p.id,
			p.full_name,
			p.email,
			p.phone,
			COALESCE(p.specialty, '')                                     AS specialty,
			COALESCE(p.work_schedule, ''),
			COALESCE(p.description, ''),
			COUNT(sr.id) FILTER (WHERE sr.status = 'in_progress')         AS in_progress
		FROM profiles p
		LEFT JOIN service_requests sr ON sr.assigned_to = p.id
		WHERE p.role = 'staff'
		GROUP BY p.id
		ORDER BY p.full_name`)
	if err != nil {
		internalError(c, "Staff.PublicList/query", err)
		return
	}
	defer rows.Close()

	out := []publicStaffMember{}
	for rows.Next() {
		var m publicStaffMember
		var inProgress int
		if err := rows.Scan(
			&m.ID, &m.FullName, &m.Email, &m.Phone,
			&m.Specialty, &m.WorkSchedule, &m.Description,
			&inProgress,
		); err != nil {
			internalError(c, "Staff.PublicList/scan", err)
			return
		}
		m.IsAvailable = inProgress == 0
		out = append(out, m)
	}
	c.JSON(http.StatusOK, out)
}

// GET /admin/staff — list all staff members with their current workload.
func (h *StaffHandler) List(c *gin.Context) {
	if c.GetString("user_role") != "admin" {
		forbiddenAccess(c, "admin only")
		return
	}
	ctx := context.Background()

	rows, err := h.db.Query(ctx, `
		SELECT
			p.id,
			p.full_name,
			p.email,
			p.phone,
			COALESCE(p.specialty, '')                                             AS specialty,
			COUNT(sr.id) FILTER (WHERE sr.status = 'in_progress')                  AS in_progress,
			COUNT(sr.id) FILTER (WHERE sr.status = 'done'
			                       AND sr.updated_at >= NOW() - INTERVAL '24h')    AS done_today,
			MAX(sr.taken_at)                                                       AS last_taken_at
		FROM profiles p
		LEFT JOIN service_requests sr ON sr.assigned_to = p.id
		WHERE p.role = 'staff'
		GROUP BY p.id
		ORDER BY p.full_name`)
	if err != nil {
		internalError(c, "Staff.List/query", err)
		return
	}
	defer rows.Close()

	out := []staffMember{}
	for rows.Next() {
		var m staffMember
		if err := rows.Scan(
			&m.ID, &m.FullName, &m.Email, &m.Phone, &m.Specialty,
			&m.InProgress, &m.DoneToday, &m.LastTakenAt,
		); err != nil {
			internalError(c, "Staff.List/scan", err)
			return
		}
		out = append(out, m)
	}
	c.JSON(http.StatusOK, out)
}

// GET /admin/staff/:id/requests — all requests assigned to a specific staff member.
func (h *StaffHandler) Requests(c *gin.Context) {
	if c.GetString("user_role") != "admin" {
		forbiddenAccess(c, "admin only")
		return
	}
	staffID := c.Param("id")
	rows, err := h.db.Query(context.Background(), `
		SELECT id, user_id, category, description, status,
		       assigned_to, NULL::text, taken_at, created_at, updated_at
		FROM service_requests
		WHERE assigned_to = $1
		ORDER BY created_at DESC`, staffID)
	if err != nil {
		internalError(c, "Staff.Requests/query", err)
		return
	}
	requests, err := scanSRRows(rows)
	if err != nil {
		internalError(c, "Staff.Requests/scan", err)
		return
	}
	if requests == nil {
		requests = []serviceRequest{}
	}
	c.JSON(http.StatusOK, requests)
}
