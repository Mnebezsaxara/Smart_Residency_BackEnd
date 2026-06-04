package handler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
	// Канонная категория парковочных жалоб — «Паркинг» (фикс e5ae60f). «Парковка»
	// принимаем как синоним, чтобы жалобы доходили до охраны независимо от того,
	// какое из написаний прислал клиент, и чтобы старые строки «Парковка» в БД тоже
	// маршрутизировались верно.
	"security": {"Паркинг", "Парковка"},
}

// categorySpecialty is the reverse map of specialtyCategories: given a request
// category, return the staff specialty that handles it. Returns "" for
// categories no specialty handles.
var categorySpecialty = func() map[string]string {
	m := map[string]string{}
	for sp, cats := range specialtyCategories {
		for _, c := range cats {
			m[c] = sp
		}
	}
	return m
}()

// RequestNotifier pushes FCM messages about service-request lifecycle events.
// Implemented by *fcm.Sender. When nil (e.g., FIREBASE_CREDENTIALS_PATH unset)
// the bell-notification rows are still written, only the push is skipped.
type RequestNotifier interface {
	SendToUser(ctx context.Context, userID string, data map[string]string) (int, error)
	SendToAdmins(ctx context.Context, data map[string]string) (int, error)
	SendToStaffBySpecialty(ctx context.Context, specialty string, data map[string]string) (int, error)
}

type ServiceRequestHandler struct {
	db       *pgxpool.Pool
	notifier RequestNotifier
}

func NewServiceRequestHandler(db *pgxpool.Pool, notifier RequestNotifier) *ServiceRequestHandler {
	return &ServiceRequestHandler{db: db, notifier: notifier}
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
			// One malformed row must not take down the whole requests screen with
			// a 500. Log it (so we can find the bad row) and keep the good ones.
			// A failed Scan can leave the pgx result stream unusable, so stop here
			// and return what we already collected.
			log.Printf("[sr] scan row skipped: %v", err)
			break
		}
		r.Photos = []string{}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		log.Printf("[sr] list rows iteration: %v", err)
	}
	return out, nil
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
			srSelect+` WHERE sr.category = ANY($1) OR sr.assigned_to = $2
			           ORDER BY sr.created_at DESC`, cats, userID)
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
	if role == "staff" {
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
	ctx := context.Background()
	_, err := h.db.Exec(ctx,
		`INSERT INTO service_requests (id, user_id, category, description, status)
		 VALUES ($1, $2, $3, $4, 'new')`,
		id, userID, req.Category, req.Description,
	)
	if err != nil {
		internalError(c, "SR.Create/insert", err)
		return
	}

	// Push + bell to admins and to whoever handles this category:
	// staff with the matching specialty (parking → specialty='security').
	title := "Новая заявка: " + req.Category
	body := req.Description
	if len(body) > 120 {
		body = body[:117] + "..."
	}
	go h.notifyNewRequest(context.Background(), id, req.Category, title, body)

	c.JSON(http.StatusCreated, gin.H{"id": id})
}

// Take — staff self-assigns an unassigned request in their categories.
// PATCH /service-requests/:id/take
func (h *ServiceRequestHandler) Take(c *gin.Context) {
	role := c.GetString("user_role")
	if role != "staff" {
		forbiddenAccess(c, "staff only")
		return
	}
	userID := c.GetString("user_id")
	id := c.Param("id")
	ctx := context.Background()

	specialty, e := h.staffSpecialty(ctx, userID)
	if e != nil || specialty == "" {
		c.JSON(http.StatusForbidden, gin.H{"error": "no specialty assigned to your account"})
		return
	}
	cats := specialtyCategories[specialty]

	var (
		residentID string
		category   string
		staffName  string
	)
	err := h.db.QueryRow(ctx, `
		WITH upd AS (
			UPDATE service_requests
			SET assigned_to=$1, status='in_progress', taken_at=NOW(), updated_at=NOW()
			WHERE id=$2
			  AND category = ANY($3)
			  AND (assigned_to IS NULL OR assigned_to=$1)
			  AND status IN ('new','assigned')
			RETURNING user_id, category
		)
		SELECT upd.user_id, upd.category, COALESCE(p.full_name, '')
		FROM upd
		LEFT JOIN profiles p ON p.id=$1`,
		userID, id, cats,
	).Scan(&residentID, &category, &staffName)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusForbidden, gin.H{"error": "request not available for your specialty or already taken"})
		return
	}
	if err != nil {
		internalError(c, "SR.Take/exec", err)
		return
	}

	title := "Заявка взята в работу"
	body := "Сотрудник " + staffName + " взял вашу заявку: " + category
	go h.notifyResident(context.Background(), residentID, "service_request_taken",
		title, body, id, category, "in_progress", staffName)

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

	var (
		residentID string
		category   string
		staffName  string
	)
	err := h.db.QueryRow(ctx, `
		WITH upd AS (
			UPDATE service_requests
			SET assigned_to=$1, status='assigned', updated_at=NOW()
			WHERE id=$2
			RETURNING user_id, category
		)
		SELECT upd.user_id, upd.category, COALESCE(p.full_name, '')
		FROM upd
		LEFT JOIN profiles p ON p.id=$1`,
		body.StaffID, id,
	).Scan(&residentID, &category, &staffName)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "request not found"})
		return
	}
	if err != nil {
		internalError(c, "SR.Assign/exec", err)
		return
	}

	// Notify resident: an admin assigned a staff member to their request.
	residentTitle := "На вашу заявку назначен сотрудник"
	residentBody := staffName + " займётся вашей заявкой: " + category
	go h.notifyResident(context.Background(), residentID, "service_request_assigned",
		residentTitle, residentBody, id, category, "assigned", staffName)

	// Notify the assigned staff member directly.
	staffTitle := "Вам назначена заявка: " + category
	staffBody := "Откройте раздел заявок, чтобы приступить к работе."
	go h.notifyStaff(context.Background(), body.StaffID, "service_request_assigned",
		staffTitle, staffBody, id, category, "assigned")

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
		var (
			residentID string
			category   string
			staffName  string
		)
		err := h.db.QueryRow(ctx,
			`WITH upd AS (
				UPDATE service_requests SET status=$1, updated_at=NOW()`+extra+`
				WHERE id=$2 AND assigned_to=$3
				RETURNING user_id, category
			)
			SELECT upd.user_id, upd.category, COALESCE(p.full_name, '')
			FROM upd
			LEFT JOIN profiles p ON p.id=$3`,
			req.Status, id, userID,
		).Scan(&residentID, &category, &staffName)
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusForbidden, gin.H{"error": "not your request"})
			return
		}
		if err != nil {
			internalError(c, "SR.UpdateStatus/staff", err)
			return
		}

		// Push + bell to resident on terminal status changes.
		switch req.Status {
		case "done":
			go h.notifyResident(context.Background(), residentID, "service_request_done",
				"Заявка выполнена",
				"Сотрудник "+staffName+" выполнил вашу заявку: "+category,
				id, category, "done", staffName)
		case "rejected":
			go h.notifyResident(context.Background(), residentID, "service_request_rejected",
				"Заявка отклонена",
				"Сотрудник "+staffName+" отклонил вашу заявку: "+category,
				id, category, "rejected", staffName)
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
			`UPDATE parking_permits SET status='approved', reviewed_at=NOW() WHERE id=$1`, permitID,
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

// requestDataJSON serializes the common payload that Flutter uses to open the
// right request screen on notification tap.
func requestDataJSON(requestID, category, status, assignedName string) string {
	return fmt.Sprintf(
		`{"request_id":"%s","category":%q,"status":"%s","assigned_to_name":%q}`,
		requestID, category, status, assignedName,
	)
}

// notifyNewRequest fires when a resident creates a request: bell + push to
// all admins and to whoever handles this category — staff with the matching
// specialty (parking goes to specialty='security').
func (h *ServiceRequestHandler) notifyNewRequest(ctx context.Context, requestID, category, title, body string) {
	data := requestDataJSON(requestID, category, "new", "")
	specialty := categorySpecialty[category]

	if _, err := h.db.Exec(ctx, `
		INSERT INTO notifications (target_role, kind, title, body, data)
		VALUES ('admin', 'service_request_new', $1, $2, $3::jsonb)`,
		title, body, data,
	); err != nil {
		log.Printf("[sr] new admin bell: %v", err)
	}

	if specialty != "" {
		if _, err := h.db.Exec(ctx, `
			INSERT INTO notifications (target_user_id, kind, title, body, data)
			SELECT p.id, 'service_request_new', $1, $2, $3::jsonb
			FROM profiles p
			WHERE p.role = 'staff' AND p.specialty = $4`,
			title, body, data, specialty,
		); err != nil {
			log.Printf("[sr] new staff bell: %v", err)
		}
	}

	if h.notifier == nil {
		return
	}
	push := map[string]string{
		"kind":       "service_request_new",
		"request_id": requestID,
		"category":   category,
		"status":     "new",
		"title":      title,
		"body":       body,
	}
	_, _ = h.notifier.SendToAdmins(ctx, push)
	if specialty != "" {
		_, _ = h.notifier.SendToStaffBySpecialty(ctx, specialty, push)
	}
}

// notifyResident sends bell + push to one resident about a status change on
// their request (taken / assigned / done / rejected).
func (h *ServiceRequestHandler) notifyResident(ctx context.Context, residentID, kind, title, body, requestID, category, status, assignedName string) {
	if residentID == "" {
		return
	}
	data := requestDataJSON(requestID, category, status, assignedName)
	if _, err := h.db.Exec(ctx, `
		INSERT INTO notifications (target_user_id, kind, title, body, data)
		VALUES ($1, $2, $3, $4, $5::jsonb)`,
		residentID, kind, title, body, data,
	); err != nil {
		log.Printf("[sr] resident bell %s: %v", kind, err)
	}
	if h.notifier == nil {
		return
	}
	_, _ = h.notifier.SendToUser(ctx, residentID, map[string]string{
		"kind":             kind,
		"request_id":       requestID,
		"category":         category,
		"status":           status,
		"assigned_to_name": assignedName,
		"title":            title,
		"body":             body,
	})
}

// notifyStaff sends bell + push to one staff member (used when admin assigns
// a request directly to a specific worker).
func (h *ServiceRequestHandler) notifyStaff(ctx context.Context, staffID, kind, title, body, requestID, category, status string) {
	if staffID == "" {
		return
	}
	data := requestDataJSON(requestID, category, status, "")
	if _, err := h.db.Exec(ctx, `
		INSERT INTO notifications (target_user_id, kind, title, body, data)
		VALUES ($1, $2, $3, $4, $5::jsonb)`,
		staffID, kind, title, body, data,
	); err != nil {
		log.Printf("[sr] staff bell %s: %v", kind, err)
	}
	if h.notifier == nil {
		return
	}
	_, _ = h.notifier.SendToUser(ctx, staffID, map[string]string{
		"kind":       kind,
		"request_id": requestID,
		"category":   category,
		"status":     status,
		"title":      title,
		"body":       body,
	})
}
