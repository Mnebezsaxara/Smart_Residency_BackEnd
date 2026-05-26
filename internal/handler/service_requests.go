package handler

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// specialtyCategories maps a staff specialty to the request categories they handle.
var specialtyCategories = map[string][]string{
	"plumbing":   {"Протечка", "Дверь/вход"},
	"electrical": {"Электричество", "Освещение"},
	"cleaning":   {"Уборка"},
	"garbage":    {"Вывоз мусора"},
	"intercom":   {"Домофон"},
	"elevator":   {"Лифт"},
}

type ServiceRequestHandler struct{ db *pgxpool.Pool }

func NewServiceRequestHandler(db *pgxpool.Pool) *ServiceRequestHandler {
	return &ServiceRequestHandler{db: db}
}

type serviceRequest struct {
	ID             string     `json:"id"`
	UserID         string     `json:"user_id"`
	Category       string     `json:"category"`
	Description    string     `json:"description"`
	Status         string     `json:"status"`
	AssignedTo     *string    `json:"assigned_to,omitempty"`
	AssignedToName *string    `json:"assigned_to_name,omitempty"`
	TakenAt        *time.Time `json:"taken_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	Photos         []string   `json:"photos"`
}

const srSelect = `
	SELECT sr.id, sr.user_id, sr.category, sr.description, sr.status,
	       sr.assigned_to, p.full_name, sr.taken_at, sr.created_at, sr.updated_at
	FROM service_requests sr
	LEFT JOIN profiles p ON p.id = sr.assigned_to`

func scanSRRows(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
	Close()
}) ([]serviceRequest, error) {
	defer rows.Close()
	var out []serviceRequest
	for rows.Next() {
		var r serviceRequest
		if err := rows.Scan(
			&r.ID, &r.UserID, &r.Category, &r.Description, &r.Status,
			&r.AssignedTo, &r.AssignedToName, &r.TakenAt,
			&r.CreatedAt, &r.UpdatedAt,
		); err != nil {
			return nil, err
		}
		r.Photos = []string{}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (h *ServiceRequestHandler) List(c *gin.Context) {
	userID := c.GetString("user_id")
	role := c.GetString("user_role")
	ctx := context.Background()

	var (
		requests []serviceRequest
		err      error
	)

	switch role {
	case "admin":
		rows, e := h.db.Query(ctx, srSelect+` ORDER BY sr.created_at DESC`)
		if e != nil {
			internalError(c, "SR.List/admin", e)
			return
		}
		requests, err = scanSRRows(rows)

	case "staff":
		specialty, e := h.staffSpecialty(ctx, userID)
		if e != nil || specialty == "" {
			c.JSON(http.StatusOK, []serviceRequest{})
			return
		}
		cats := specialtyCategories[specialty]
		if len(cats) == 0 {
			c.JSON(http.StatusOK, []serviceRequest{})
			return
		}
		rows, e := h.db.Query(ctx,
			srSelect+` WHERE sr.category = ANY($1) ORDER BY sr.created_at DESC`, cats)
		if e != nil {
			internalError(c, "SR.List/staff", e)
			return
		}
		requests, err = scanSRRows(rows)

	default: // resident
		rows, e := h.db.Query(ctx,
			srSelect+` WHERE sr.user_id = $1 ORDER BY sr.created_at DESC`, userID)
		if e != nil {
			internalError(c, "SR.List/resident", e)
			return
		}
		requests, err = scanSRRows(rows)
	}

	if err != nil {
		internalError(c, "SR.List/scan", err)
		return
	}
	if requests == nil {
		requests = []serviceRequest{}
	}
	h.attachPhotos(ctx, requests)
	c.JSON(http.StatusOK, requests)
}

type createRequestReq struct {
	Category    string `json:"category"    binding:"required"`
	Description string `json:"description" binding:"required"`
}

func (h *ServiceRequestHandler) Create(c *gin.Context) {
	role := c.GetString("user_role")
	if role == "staff" || role == "guard" {
		forbiddenAccess(c, "residents only")
		return
	}
	var req createRequestReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	userID := c.GetString("user_id")
	id := uuid.New().String()
	_, err := h.db.Exec(context.Background(),
		`INSERT INTO service_requests (id, user_id, category, description, status)
		 VALUES ($1, $2, $3, $4, 'new')`,
		id, userID, req.Category, req.Description,
	)
	if err != nil {
		internalError(c, "SR.Create/insert", err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"id": id})
}

// Take — staff self-assigns an unassigned request in their specialty category.
// PATCH /service-requests/:id/take
func (h *ServiceRequestHandler) Take(c *gin.Context) {
	if c.GetString("user_role") != "staff" {
		forbiddenAccess(c, "staff only")
		return
	}
	userID := c.GetString("user_id")
	id := c.Param("id")
	ctx := context.Background()

	specialty, err := h.staffSpecialty(ctx, userID)
	if err != nil || specialty == "" {
		c.JSON(http.StatusForbidden, gin.H{"error": "no specialty assigned to your account"})
		return
	}
	cats := specialtyCategories[specialty]

	tag, err := h.db.Exec(ctx, `
		UPDATE service_requests
		SET assigned_to=$1, status='in_progress', taken_at=NOW(), updated_at=NOW()
		WHERE id=$2
		  AND category = ANY($3)
		  AND (assigned_to IS NULL OR assigned_to=$1)
		  AND status IN ('new','assigned')`,
		userID, id, cats,
	)
	if err != nil {
		internalError(c, "SR.Take/exec", err)
		return
	}
	if tag.RowsAffected() == 0 {
		c.JSON(http.StatusForbidden, gin.H{"error": "request not available for your specialty or already taken"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "in_progress"})
}

// Assign — admin assigns a request to a specific staff member.
// POST /admin/service-requests/:id/assign
func (h *ServiceRequestHandler) Assign(c *gin.Context) {
	if c.GetString("user_role") != "admin" {
		forbiddenAccess(c, "admin only")
		return
	}
	id := c.Param("id")
	var body struct {
		StaffID string `json:"staff_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx := context.Background()

	var role string
	if err := h.db.QueryRow(ctx, `SELECT role FROM profiles WHERE id=$1`, body.StaffID).Scan(&role); err != nil || role != "staff" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "target user is not staff"})
		return
	}

	_, err := h.db.Exec(ctx, `
		UPDATE service_requests
		SET assigned_to=$1, status='assigned', updated_at=NOW()
		WHERE id=$2`,
		body.StaffID, id,
	)
	if err != nil {
		internalError(c, "SR.Assign/exec", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"assigned_to": body.StaffID})
}

type updateStatusReq struct {
	Status string `json:"status" binding:"required"`
}

func (h *ServiceRequestHandler) UpdateStatus(c *gin.Context) {
	role := c.GetString("user_role")
	userID := c.GetString("user_id")
	id := c.Param("id")
	ctx := context.Background()

	var req updateStatusReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if role == "admin" {
		allowed := map[string]bool{"new": true, "assigned": true, "in_progress": true, "done": true, "rejected": true}
		if !allowed[req.Status] {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid status"})
			return
		}
		_, err := h.db.Exec(ctx,
			`UPDATE service_requests SET status=$2, updated_at=NOW() WHERE id=$1`, id, req.Status)
		if err != nil {
			internalError(c, "SR.UpdateStatus/admin", err)
			return
		}
	} else if role == "staff" {
		allowed := map[string]bool{"in_progress": true, "done": true, "rejected": true}
		if !allowed[req.Status] {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid status for staff"})
			return
		}
		// Set taken_at on first in_progress transition
		extra := ""
		if req.Status == "in_progress" {
			extra = ", taken_at = COALESCE(taken_at, NOW())"
		}
		tag, err := h.db.Exec(ctx,
			`UPDATE service_requests SET status=$1, updated_at=NOW()`+extra+`
			 WHERE id=$2 AND assigned_to=$3`,
			req.Status, id, userID,
		)
		if err != nil {
			internalError(c, "SR.UpdateStatus/staff", err)
			return
		}
		if tag.RowsAffected() == 0 {
			c.JSON(http.StatusForbidden, gin.H{"error": "not your request"})
			return
		}
	} else {
		forbiddenAccess(c, "admin or staff only")
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": req.Status})
}

func (h *ServiceRequestHandler) UploadPhoto(c *gin.Context) {
	requestID := c.Param("id")
	file, header, err := c.Request.FormFile("photo")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "photo file required"})
		return
	}
	defer file.Close()
	ext := filepath.Ext(header.Filename)
	filename := uuid.New().String() + ext
	savePath := filepath.Join("uploads", "request-photos", filename)
	if err := c.SaveUploadedFile(header, savePath); err != nil {
		internalError(c, "SR.UploadPhoto/save", err)
		return
	}
	_, err = h.db.Exec(context.Background(),
		`INSERT INTO request_photos (id, request_id, file_path) VALUES ($1, $2, $3)`,
		uuid.New().String(), requestID, savePath,
	)
	if err != nil {
		internalError(c, "SR.UploadPhoto/insert", err)
		return
	}
	baseURL := os.Getenv("BASE_URL")
	c.JSON(http.StatusCreated, gin.H{
		"url": fmt.Sprintf("%s/uploads/request-photos/%s", baseURL, filename),
	})
}

func (h *ServiceRequestHandler) staffSpecialty(ctx context.Context, userID string) (string, error) {
	var s string
	err := h.db.QueryRow(ctx, `SELECT COALESCE(specialty,'') FROM profiles WHERE id=$1`, userID).Scan(&s)
	return s, err
}

// ResolveAppeal — admin resolves a parking appeal service request.
// POST /admin/service-requests/:id/resolve-appeal
func (h *ServiceRequestHandler) ResolveAppeal(c *gin.Context) {
	if c.GetString("user_role") != "admin" {
		forbiddenAccess(c, "admin only")
		return
	}
	id := c.Param("id")
	var body struct {
		Approved bool `json:"approved"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx := context.Background()

	var userID string
	if err := h.db.QueryRow(ctx,
		`SELECT user_id FROM service_requests WHERE id=$1`, id,
	).Scan(&userID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "request not found"})
		return
	}

	if body.Approved {
		var permitID string
		if err := h.db.QueryRow(ctx,
			`SELECT id FROM parking_permits
			 WHERE user_id=$1 AND status='rejected'
			 ORDER BY created_at DESC LIMIT 1`, userID,
		).Scan(&permitID); err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "no rejected permit found for user"})
			return
		}
		if _, err := h.db.Exec(ctx,
			`UPDATE parking_permits SET status='approved', updated_at=NOW() WHERE id=$1`, permitID,
		); err != nil {
			internalError(c, "SR.ResolveAppeal/approve_permit", err)
			return
		}
		if _, err := h.db.Exec(ctx,
			`UPDATE profiles SET parking_permit_status='approved', updated_at=NOW() WHERE id=$1`, userID,
		); err != nil {
			internalError(c, "SR.ResolveAppeal/update_profile", err)
			return
		}
		if _, err := h.db.Exec(ctx,
			`UPDATE service_requests SET status='done', updated_at=NOW() WHERE id=$1`, id,
		); err != nil {
			internalError(c, "SR.ResolveAppeal/mark_done", err)
			return
		}
	} else {
		if _, err := h.db.Exec(ctx,
			`UPDATE service_requests SET status='rejected', updated_at=NOW() WHERE id=$1`, id,
		); err != nil {
			internalError(c, "SR.ResolveAppeal/reject", err)
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"resolved": true, "approved": body.Approved})
}

func (h *ServiceRequestHandler) attachPhotos(ctx context.Context, requests []serviceRequest) {
	if len(requests) == 0 {
		return
	}
	idIndex := map[string]int{}
	ids := make([]string, len(requests))
	for i, r := range requests {
		ids[i] = r.ID
		idIndex[r.ID] = i
	}
	photoRows, err := h.db.Query(ctx,
		`SELECT request_id, file_path FROM request_photos WHERE request_id = ANY($1)`, ids)
	if err != nil {
		return
	}
	defer photoRows.Close()
	baseURL := os.Getenv("BASE_URL")
	for photoRows.Next() {
		var reqID, path string
		if photoRows.Scan(&reqID, &path) == nil {
			if i, ok := idIndex[reqID]; ok {
				requests[i].Photos = append(requests[i].Photos,
					fmt.Sprintf("%s/uploads/request-photos/%s", baseURL, filepath.Base(path)))
			}
		}
	}
}
