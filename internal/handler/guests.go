package handler

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type GuestHandler struct{ db *pgxpool.Pool }

func NewGuestHandler(db *pgxpool.Pool) *GuestHandler { return &GuestHandler{db: db} }

type guestPass struct {
	ID            string    `json:"id"`
	ResidentID    string    `json:"resident_id"`
	GuestName     string    `json:"guest_name"`
	GuestPhone    *string   `json:"guest_phone"`
	CarNumber     *string   `json:"car_number"`
	AccessType    string    `json:"access_type"`
	AccessCode    string    `json:"access_code"`
	QRCode        *string   `json:"qr_code"`
	ValidFrom     time.Time `json:"valid_from"`
	ValidUntil    time.Time `json:"valid_until"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
	ParkingSpotID *string   `json:"parking_spot_id"`
}

func (h *GuestHandler) List(c *gin.Context) {
	userID := c.GetString("user_id")
	ctx := context.Background()

	rows, err := h.db.Query(ctx, `
		SELECT id, resident_id, guest_name, guest_phone, car_number,
		       access_type, access_code, qr_code, valid_from, valid_until, status, created_at,
		       parking_spot_id
		FROM guest_access
		WHERE resident_id = $1
		ORDER BY created_at DESC`, userID)
	if err != nil {
		internalError(c, "Guest", err)
		return
	}
	defer rows.Close()

	passes := []guestPass{}
	for rows.Next() {
		var p guestPass
		if err := rows.Scan(
			&p.ID, &p.ResidentID, &p.GuestName, &p.GuestPhone, &p.CarNumber,
			&p.AccessType, &p.AccessCode, &p.QRCode, &p.ValidFrom, &p.ValidUntil,
			&p.Status, &p.CreatedAt, &p.ParkingSpotID,
		); err != nil {
			internalError(c, "Guest", err)
			return
		}
		passes = append(passes, p)
	}
	c.JSON(http.StatusOK, passes)
}

type createGuestReq struct {
	GuestName     string  `json:"guest_name"      binding:"required"`
	GuestPhone    string  `json:"guest_phone"`
	CarNumber     string  `json:"car_number"`
	AccessType    string  `json:"access_type"     binding:"required"`
	ValidFrom     string  `json:"valid_from"      binding:"required"`
	ValidUntil    string  `json:"valid_until"     binding:"required"`
	ParkingSpotID *string `json:"parking_spot_id"`
}

func (h *GuestHandler) Create(c *gin.Context) {
	var req createGuestReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	validFrom, err := time.Parse(time.RFC3339, req.ValidFrom)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid valid_from format, use RFC3339"})
		return
	}
	validUntil, err := time.Parse(time.RFC3339, req.ValidUntil)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid valid_until format, use RFC3339"})
		return
	}
	if !validUntil.After(validFrom) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "valid_until must be after valid_from"})
		return
	}

	// Если выбрано место — проверяем что оно свободно
	if req.ParkingSpotID != nil {
		var status string
		if err := h.db.QueryRow(context.Background(),
			`SELECT status FROM parking_spots WHERE id = $1 AND type = 'guest'`,
			*req.ParkingSpotID,
		).Scan(&status); err != nil || status != "free" {
			c.JSON(http.StatusConflict, gin.H{"error": "parking spot is not available"})
			return
		}
	}

	userID := c.GetString("user_id")
	id := uuid.New().String()
	ms := time.Now().UnixMilli()
	code := fmt.Sprintf("G-%06d", ms%1000000)
	qrCode := uuid.New().String()

	var phone, car *string
	if req.GuestPhone != "" {
		phone = &req.GuestPhone
	}
	if req.CarNumber != "" {
		normalized := strings.ToUpper(strings.TrimSpace(req.CarNumber))
		car = &normalized
	}

	_, err = h.db.Exec(context.Background(), `
		INSERT INTO guest_access
			(id, resident_id, guest_name, guest_phone, car_number,
			 access_type, access_code, qr_code, valid_from, valid_until, status, parking_spot_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'active',$11)`,
		id, userID, req.GuestName, phone, car,
		req.AccessType, code, qrCode, validFrom, validUntil, req.ParkingSpotID,
	)
	if err != nil {
		internalError(c, "Guest", err)
		return
	}

	// Помечаем место как забронированное
	if req.ParkingSpotID != nil {
		_, _ = h.db.Exec(context.Background(),
			`UPDATE parking_spots SET status = 'reserved' WHERE id = $1`,
			*req.ParkingSpotID)
	}

	c.JSON(http.StatusCreated, gin.H{"id": id, "access_code": code, "qr_code": qrCode})
}

func (h *GuestHandler) Cancel(c *gin.Context) {
	id := c.Param("id")
	userID := c.GetString("user_id")

	// Получаем parking_spot_id до отмены, чтобы освободить место
	var spotID *string
	_ = h.db.QueryRow(context.Background(),
		`SELECT parking_spot_id FROM guest_access WHERE id = $1 AND resident_id = $2`,
		id, userID,
	).Scan(&spotID)

	_, err := h.db.Exec(context.Background(),
		`UPDATE guest_access SET status = 'cancelled'
		 WHERE id = $1 AND resident_id = $2`, id, userID)
	if err != nil {
		internalError(c, "Guest", err)
		return
	}

	// Освобождаем место обратно
	if spotID != nil {
		_, _ = h.db.Exec(context.Background(),
			`UPDATE parking_spots SET status = 'free' WHERE id = $1 AND status = 'reserved'`,
			*spotID)
	}

	c.JSON(http.StatusOK, gin.H{"status": "cancelled"})
}
