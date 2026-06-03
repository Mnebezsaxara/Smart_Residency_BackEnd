package handler

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ParkingHandler struct {
	db *pgxpool.Pool
}

func NewParkingHandler(db *pgxpool.Pool) *ParkingHandler {
	return &ParkingHandler{db: db}
}

type ParkingSpot struct {
	ID             string    `json:"id"`
	SpotNumber     string    `json:"spot_number"`
	Type           string    `json:"type"`
	Status         string    `json:"status"`
	AssignedUserID *string   `json:"assigned_user_id"`
	IsMine         bool      `json:"is_mine"`
	// Alert is true only when the spot is occupied by a STRANGER (the backend
	// confirmed no owner/expected-guest entry). Flutter shows the red "занято
	// посторонним ТС" + "Сообщить администратору" view based on this, NOT on
	// status == occupied. Set by the MQTT parking handler (see handleParking).
	Alert     bool      `json:"alert"`
	CreatedAt time.Time `json:"created_at"`
}

type ParkingSpotAdmin struct {
	ID               string    `json:"id"`
	SpotNumber       string    `json:"spot_number"`
	Type             string    `json:"type"`
	Status           string    `json:"status"`
	AssignedUserID   *string   `json:"assigned_user_id"`
	AssignedUserName *string   `json:"assigned_user_name"`
	CreatedAt        time.Time `json:"created_at"`
}

type ParkingBooking struct {
	ID         string    `json:"id"`
	SpotID     string    `json:"spot_id"`
	SpotNumber string    `json:"spot_number"`
	UserID     string    `json:"user_id"`
	StartTime  time.Time `json:"start_time"`
	EndTime    time.Time `json:"end_time"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
}

type ParkingEvent struct {
	ID         string    `json:"id"`
	SpotID     string    `json:"spot_id"`
	SpotNumber string    `json:"spot_number"`
	EventType  string    `json:"event_type"`
	CreatedAt  time.Time `json:"created_at"`
}

// GET /parking/spots
func (h *ParkingHandler) ListSpots(c *gin.Context) {
	userID := c.GetString("user_id")
	rows, err := h.db.Query(c.Request.Context(), `
		SELECT id, spot_number, type, status, assigned_user_id, alert, created_at
		FROM parking_spots
		ORDER BY type DESC, spot_number ASC`)
	if err != nil {
		internalError(c, "Parking.ListSpots/query", err)
		return
	}
	defer rows.Close()
	out := []ParkingSpot{}
	for rows.Next() {
		var s ParkingSpot
		if err := rows.Scan(&s.ID, &s.SpotNumber, &s.Type, &s.Status, &s.AssignedUserID, &s.Alert, &s.CreatedAt); err != nil {
			internalError(c, "Parking.ListSpots/scan", err)
			return
		}
		if s.AssignedUserID != nil && *s.AssignedUserID == userID {
			s.IsMine = true
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		internalError(c, "Parking.ListSpots/rows", err)
		return
	}
	c.JSON(http.StatusOK, out)
}

// GET /parking/bookings/my
func (h *ParkingHandler) MyBookings(c *gin.Context) {
	userID := c.GetString("user_id")
	rows, err := h.db.Query(c.Request.Context(), `
		SELECT b.id, b.spot_id, s.spot_number, b.user_id, b.start_time, b.end_time, b.status, b.created_at
		FROM parking_bookings b
		JOIN parking_spots s ON s.id = b.spot_id
		WHERE b.user_id = $1
		ORDER BY b.created_at DESC`, userID)
	if err != nil {
		internalError(c, "Parking.MyBookings/query", err)
		return
	}
	defer rows.Close()
	out := []ParkingBooking{}
	for rows.Next() {
		var b ParkingBooking
		if err := rows.Scan(&b.ID, &b.SpotID, &b.SpotNumber, &b.UserID,
			&b.StartTime, &b.EndTime, &b.Status, &b.CreatedAt); err != nil {
			internalError(c, "Parking.MyBookings/scan", err)
			return
		}
		out = append(out, b)
	}
	c.JSON(http.StatusOK, out)
}

type createBookingReq struct {
	SpotID    string    `json:"spot_id"    binding:"required"`
	StartTime time.Time `json:"start_time" binding:"required"`
	EndTime   time.Time `json:"end_time"   binding:"required"`
}

// POST /parking/bookings
func (h *ParkingHandler) CreateBooking(c *gin.Context) {
	var req createBookingReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if !req.EndTime.After(req.StartTime) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "end_time must be after start_time"})
		return
	}
	userID := c.GetString("user_id")
	ctx := c.Request.Context()

	var spotType string
	err := h.db.QueryRow(ctx, `SELECT type FROM parking_spots WHERE id = $1`, req.SpotID).Scan(&spotType)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "spot not found"})
		return
	}
	if err != nil {
		internalError(c, "Parking.CreateBooking/spotQuery", err)
		return
	}
	if spotType != "guest" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "only guest spots can be booked"})
		return
	}

	var overlap int
	if err = h.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM parking_bookings
		WHERE spot_id = $1 AND status = 'active'
		  AND start_time < $3 AND end_time > $2`,
		req.SpotID, req.StartTime, req.EndTime,
	).Scan(&overlap); err != nil {
		internalError(c, "Parking.CreateBooking/overlapCheck", err)
		return
	}
	if overlap > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "spot already booked for this time"})
		return
	}

	var b ParkingBooking
	err = h.db.QueryRow(ctx, `
		WITH ins AS (
			INSERT INTO parking_bookings (spot_id, user_id, start_time, end_time)
			VALUES ($1, $2, $3, $4)
			RETURNING id, spot_id, user_id, start_time, end_time, status, created_at
		)
		SELECT ins.id, ins.spot_id, s.spot_number, ins.user_id,
		       ins.start_time, ins.end_time, ins.status, ins.created_at
		FROM ins
		JOIN parking_spots s ON s.id = ins.spot_id`,
		req.SpotID, userID, req.StartTime, req.EndTime,
	).Scan(&b.ID, &b.SpotID, &b.SpotNumber, &b.UserID, &b.StartTime, &b.EndTime, &b.Status, &b.CreatedAt)
	if err != nil {
		internalError(c, "Parking.CreateBooking/insert", err)
		return
	}
	_, _ = h.db.Exec(ctx, `UPDATE parking_spots SET status = 'reserved' WHERE id = $1`, req.SpotID)
	c.JSON(http.StatusCreated, b)
}

// PUT /parking/bookings/:id/cancel
func (h *ParkingHandler) CancelBooking(c *gin.Context) {
	userID := c.GetString("user_id")
	id := c.Param("id")
	ctx := c.Request.Context()

	var spotID string
	err := h.db.QueryRow(ctx, `
		UPDATE parking_bookings SET status = 'cancelled'
		WHERE id = $1 AND user_id = $2 AND status = 'active'
		RETURNING spot_id`, id, userID,
	).Scan(&spotID)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "active booking not found"})
		return
	}
	if err != nil {
		internalError(c, "Parking.CancelBooking/exec", err)
		return
	}
	_, _ = h.db.Exec(ctx,
		`UPDATE parking_spots SET status = 'free' WHERE id = $1 AND status = 'reserved'`, spotID)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// GET /admin/parking/spots
func (h *ParkingHandler) AdminListSpots(c *gin.Context) {
	if c.GetString("user_role") != "admin" {
		forbiddenAccess(c, "admin only")
		return
	}
	rows, err := h.db.Query(c.Request.Context(), `
		SELECT s.id, s.spot_number, s.type, s.status, s.assigned_user_id,
		       p.full_name, s.created_at
		FROM parking_spots s
		LEFT JOIN profiles p ON p.id = s.assigned_user_id
		ORDER BY s.type DESC, s.spot_number ASC`)
	if err != nil {
		internalError(c, "Parking.AdminListSpots/query", err)
		return
	}
	defer rows.Close()
	out := []ParkingSpotAdmin{}
	for rows.Next() {
		var s ParkingSpotAdmin
		if err := rows.Scan(&s.ID, &s.SpotNumber, &s.Type, &s.Status,
			&s.AssignedUserID, &s.AssignedUserName, &s.CreatedAt); err != nil {
			internalError(c, "Parking.AdminListSpots/scan", err)
			return
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		internalError(c, "Parking.AdminListSpots/rows", err)
		return
	}
	c.JSON(http.StatusOK, out)
}

type assignSpotReq struct {
	UserID string `json:"user_id"`
}

// POST /admin/parking/spots/:id/assign
func (h *ParkingHandler) AdminAssignSpot(c *gin.Context) {
	if c.GetString("user_role") != "admin" {
		forbiddenAccess(c, "admin only")
		return
	}
	id := c.Param("id")
	var req assignSpotReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var userIDPtr *string
	if req.UserID != "" {
		userIDPtr = &req.UserID
	}
	ct, err := h.db.Exec(c.Request.Context(),
		`UPDATE parking_spots SET assigned_user_id = $1 WHERE id = $2 AND type = 'permanent'`,
		userIDPtr, id)
	if err != nil {
		internalError(c, "Parking.AdminAssignSpot/exec", err)
		return
	}
	if ct.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "permanent spot not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// GET /admin/parking/bookings
func (h *ParkingHandler) AdminListBookings(c *gin.Context) {
	if c.GetString("user_role") != "admin" {
		forbiddenAccess(c, "admin only")
		return
	}
	rows, err := h.db.Query(c.Request.Context(), `
		SELECT b.id, b.spot_id, s.spot_number, b.user_id, b.start_time, b.end_time, b.status, b.created_at
		FROM parking_bookings b
		JOIN parking_spots s ON s.id = b.spot_id
		WHERE b.status = 'active'
		ORDER BY b.start_time ASC`)
	if err != nil {
		internalError(c, "Parking.AdminListBookings/query", err)
		return
	}
	defer rows.Close()
	out := []ParkingBooking{}
	for rows.Next() {
		var b ParkingBooking
		if err := rows.Scan(&b.ID, &b.SpotID, &b.SpotNumber, &b.UserID,
			&b.StartTime, &b.EndTime, &b.Status, &b.CreatedAt); err != nil {
			internalError(c, "Parking.AdminListBookings/scan", err)
			return
		}
		out = append(out, b)
	}
	c.JSON(http.StatusOK, out)
}

// GET /admin/parking/events
func (h *ParkingHandler) AdminListEvents(c *gin.Context) {
	if c.GetString("user_role") != "admin" {
		forbiddenAccess(c, "admin only")
		return
	}
	rows, err := h.db.Query(c.Request.Context(), `
		SELECT e.id, e.spot_id, s.spot_number, e.event_type, e.created_at
		FROM parking_events e
		JOIN parking_spots s ON s.id = e.spot_id
		ORDER BY e.created_at DESC
		LIMIT 200`)
	if err != nil {
		internalError(c, "Parking.AdminListEvents/query", err)
		return
	}
	defer rows.Close()
	out := []ParkingEvent{}
	for rows.Next() {
		var ev ParkingEvent
		if err := rows.Scan(&ev.ID, &ev.SpotID, &ev.SpotNumber, &ev.EventType, &ev.CreatedAt); err != nil {
			internalError(c, "Parking.AdminListEvents/scan", err)
			return
		}
		out = append(out, ev)
	}
	c.JSON(http.StatusOK, out)
}
