package handler

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type VerificationHandler struct{ db *pgxpool.Pool }

func NewVerificationHandler(db *pgxpool.Pool) *VerificationHandler {
	return &VerificationHandler{db: db}
}

type verificationRequest struct {
	ID            string        `json:"id"`
	UserID        string        `json:"user_id"`
	RequestedRole string        `json:"requested_role"`
	Comment       string        `json:"comment"`
	Status        string        `json:"status"`
	Entrance      *int          `json:"entrance"`
	Floor         *int          `json:"floor"`
	Apartment     *string       `json:"apartment"`
	ReviewedBy    *string       `json:"reviewed_by"`
	ReviewedAt    *time.Time    `json:"reviewed_at"`
	CreatedAt     time.Time     `json:"created_at"`
	UpdatedAt     time.Time     `json:"updated_at"`
	Profile       *verProfile   `json:"profile,omitempty"`
	Documents     []verDocument `json:"documents,omitempty"`
}

type verProfile struct {
	FullName    string `json:"full_name"`
	Email       string `json:"email"`
	Phone       string `json:"phone"`
	IIN         string `json:"iin"`
	FullAddress string `json:"full_address"`
}

type verDocument struct {
	ID       string `json:"id"`
	FilePath string `json:"file_path"`
	FileName string `json:"file_name"`
	FileSize int64  `json:"file_size"`
	URL      string `json:"url"`
}

type submitVerificationReq struct {
	RequestedRole string `json:"requested_role" binding:"required"`
	Comment       string `json:"comment"`
	Entrance      *int   `json:"entrance"`
	Floor         *int   `json:"floor"`
	Apartment     *int   `json:"apartment"`
}

func (h *VerificationHandler) Submit(c *gin.Context) {
	var req submitVerificationReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Self-service registration can only request the resident role. Historical
	// values (owner/tenant) are normalised to resident; staff and security are
	// created by an admin via POST /admin/staff, never through verification.
	req.RequestedRole = "resident"

	userID := c.GetString("user_id")
	id := uuid.New().String()
	ctx := context.Background()

	var apartmentStr *string
	if req.Apartment != nil {
		s := strconv.Itoa(*req.Apartment)
		apartmentStr = &s
	}

	_, err := h.db.Exec(ctx, `
		INSERT INTO verification_requests
			(id, user_id, requested_role, comment, status, entrance, floor, apartment)
		VALUES ($1, $2, $3, $4, 'pending', $5, $6, $7)`,
		id, userID, req.RequestedRole, req.Comment,
		req.Entrance, req.Floor, apartmentStr,
	)
	if err != nil {
		internalError(c, "Verification.Submit/insert", err)
		return
	}

	_, _ = h.db.Exec(ctx,
		`UPDATE profiles SET verification_status = 'pending', updated_at = NOW() WHERE id = $1`,
		userID)

	c.JSON(http.StatusCreated, gin.H{"id": id})
}

func (h *VerificationHandler) UploadDocuments(c *gin.Context) {
	verID := c.Param("id")
	userID := c.GetString("user_id")
	ctx := context.Background()

	form, err := c.MultipartForm()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "multipart form required"})
		return
	}

	files := form.File["documents"]
	if len(files) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "at least one document required"})
		return
	}

	baseURL := os.Getenv("BASE_URL")
	dir := filepath.Join("uploads", "verification-docs", userID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		internalError(c, "Verification.UploadDocuments/mkdir", err)
		return
	}

	uploaded := []string{}
	for _, fh := range files {
		ext := filepath.Ext(fh.Filename)
		filename := fmt.Sprintf("%d_%s%s", time.Now().UnixMilli(), uuid.New().String()[:8], ext)
		savePath := filepath.Join(dir, filename)

		if err := c.SaveUploadedFile(fh, savePath); err != nil {
			internalError(c, "Verification.UploadDocuments/save", err)
			return
		}

		_, err = h.db.Exec(ctx, `
			INSERT INTO verification_documents
				(id, verification_request_id, file_path, file_name, file_size)
			VALUES ($1, $2, $3, $4, $5)`,
			uuid.New().String(), verID, savePath, fh.Filename, fh.Size,
		)
		if err != nil {
			internalError(c, "Verification.UploadDocuments/insert", err)
			return
		}
		uploaded = append(uploaded,
			fmt.Sprintf("%s/uploads/verification-docs/%s/%s", baseURL, userID, filename))
	}

	c.JSON(http.StatusCreated, gin.H{"uploaded": uploaded})
}

func (h *VerificationHandler) List(c *gin.Context) {
	role := c.GetString("user_role")
	userID := c.GetString("user_id")
	ctx := context.Background()
	baseURL := os.Getenv("BASE_URL")

	var rows interface {
		Next() bool
		Scan(...any) error
		Close()
		Err() error
	}
	var err error

	if role == "admin" {
		rows, err = h.db.Query(ctx, `
			SELECT vr.id, vr.user_id, vr.requested_role, vr.comment, vr.status,
			       vr.entrance, vr.floor, vr.apartment,
			       vr.reviewed_by, vr.reviewed_at, vr.created_at, vr.updated_at,
			       COALESCE(p.full_name,''), COALESCE(p.email,''),
			       COALESCE(p.phone,''), COALESCE(p.iin,''), COALESCE(p.full_address,'')
			FROM verification_requests vr
			LEFT JOIN profiles p ON p.id = vr.user_id
			ORDER BY vr.created_at DESC`)
	} else {
		rows, err = h.db.Query(ctx, `
			SELECT vr.id, vr.user_id, vr.requested_role, vr.comment, vr.status,
			       vr.entrance, vr.floor, vr.apartment,
			       vr.reviewed_by, vr.reviewed_at, vr.created_at, vr.updated_at,
			       COALESCE(p.full_name,''), COALESCE(p.email,''),
			       COALESCE(p.phone,''), COALESCE(p.iin,''), COALESCE(p.full_address,'')
			FROM verification_requests vr
			LEFT JOIN profiles p ON p.id = vr.user_id
			WHERE vr.user_id = $1
			ORDER BY vr.created_at DESC`, userID)
	}
	if err != nil {
		internalError(c, "Verification.List/query", err)
		return
	}
	defer rows.Close()

	result := []verificationRequest{}
	idIndex := map[string]int{}

	for rows.Next() {
		var r verificationRequest
		var prof verProfile
		if err := rows.Scan(
			&r.ID, &r.UserID, &r.RequestedRole, &r.Comment, &r.Status,
			&r.Entrance, &r.Floor, &r.Apartment,
			&r.ReviewedBy, &r.ReviewedAt, &r.CreatedAt, &r.UpdatedAt,
			&prof.FullName, &prof.Email, &prof.Phone, &prof.IIN, &prof.FullAddress,
		); err != nil {
			internalError(c, "Verification.List/scan", err)
			return
		}
		r.Profile = &prof
		r.Documents = []verDocument{}
		idIndex[r.ID] = len(result)
		result = append(result, r)
	}

	if len(result) > 0 {
		ids := make([]string, len(result))
		for i, r := range result {
			ids[i] = r.ID
		}
		docRows, err := h.db.Query(ctx,
			`SELECT id, verification_request_id, file_path, file_name, file_size
			 FROM verification_documents WHERE verification_request_id = ANY($1)`, ids)
		if err == nil {
			defer docRows.Close()
			for docRows.Next() {
				var d verDocument
				var reqID string
				if err := docRows.Scan(&d.ID, &reqID, &d.FilePath, &d.FileName, &d.FileSize); err == nil {
					d.URL = fmt.Sprintf("%s/uploads/verification-docs/%s", baseURL,
						filepath.Base(d.FilePath))
					if i, ok := idIndex[reqID]; ok {
						result[i].Documents = append(result[i].Documents, d)
					}
				}
			}
		}
	}

	c.JSON(http.StatusOK, result)
}

type updateVerStatusReq struct {
	Status string `json:"status" binding:"required"`
}

func (h *VerificationHandler) UpdateStatus(c *gin.Context) {
	if c.GetString("user_role") != "admin" {
		forbiddenAccess(c, "admin only")
		return
	}

	id := c.Param("id")
	var req updateVerStatusReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	allowed := map[string]bool{"approved": true, "rejected": true, "pending": true}
	if !allowed[req.Status] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid status"})
		return
	}

	reviewerID := c.GetString("user_id")
	ctx := context.Background()

	var targetUserID string
	err := h.db.QueryRow(ctx,
		`UPDATE verification_requests
		 SET status = $2, reviewed_by = $3, reviewed_at = NOW(), updated_at = NOW()
		 WHERE id = $1
		 RETURNING user_id`, id, req.Status, reviewerID,
	).Scan(&targetUserID)
	if err != nil {
		internalError(c, "Verification.UpdateStatus/exec", err)
		return
	}

	if req.Status == "approved" {
		_, _ = h.db.Exec(ctx, `
			UPDATE profiles p SET
				role                = vr.requested_role,
				verification_status = 'approved',
				entrance            = COALESCE(vr.entrance,  p.entrance),
				floor               = COALESCE(vr.floor,     p.floor),
				apartment           = COALESCE(vr.apartment, p.apartment),
				updated_at          = NOW()
			FROM verification_requests vr
			WHERE vr.id = $1 AND p.id = vr.user_id`, id)
	} else if req.Status == "rejected" {
		_, _ = h.db.Exec(ctx,
			`UPDATE profiles SET verification_status = 'rejected', updated_at = NOW()
			 WHERE id = $1`, targetUserID)
	}

	c.JSON(http.StatusOK, gin.H{"status": req.Status})
}
