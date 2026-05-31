package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AdminBarrierHandler struct {
	db        *pgxpool.Pool
	barrierV2 *BarrierV2Handler
}

func NewAdminBarrierHandler(db *pgxpool.Pool, barrierV2 *BarrierV2Handler) *AdminBarrierHandler {
	return &AdminBarrierHandler{db: db, barrierV2: barrierV2}
}

func (h *AdminBarrierHandler) requireAdmin(c *gin.Context) bool {
	if !isBarrierStaff(c, h.db) {
		forbiddenAccess(c, "admin or security only")
		return false
	}
	return true
}

type adminBarrierEvent struct {
	ID          string    `json:"id"`
	EventType   string    `json:"event_type"`
	Direction   *string   `json:"direction"`
	PlateNumber *string   `json:"plate_number"`
	VehicleID   *string   `json:"vehicle_id"`
	GuestPassID *string   `json:"guest_pass_id"`
	OpenedBy    *string   `json:"opened_by"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	OwnerName   string    `json:"owner_name"`
	Brand       string    `json:"brand"`
	Color       string    `json:"color"`
	GuestName   string    `json:"guest_name"`
}

func (h *AdminBarrierHandler) ListAll(c *gin.Context) {
	if !h.requireAdmin(c) {
		return
	}
	rows, err := h.db.Query(context.Background(), `
		SELECT
			be.id, be.event_type, be.direction, be.plate_number,
			be.vehicle_id, be.guest_pass_id, be.opened_by, be.status, be.created_at,
			COALESCE(p.full_name, ''), COALESCE(v.brand, ''), COALESCE(v.color, ''),
			COALESCE(ga.guest_name, '')
		FROM barrier_events be
		LEFT JOIN vehicles v ON v.id = be.vehicle_id
		LEFT JOIN profiles p ON p.id = v.user_id
		LEFT JOIN guest_access ga ON ga.id = be.guest_pass_id
		ORDER BY be.created_at DESC
		LIMIT 100`)
	if err != nil {
		internalError(c, "AdminBarrier.ListAll/query", err)
		return
	}
	defer rows.Close()

	events := []adminBarrierEvent{}
	for rows.Next() {
		var e adminBarrierEvent
		if err := rows.Scan(
			&e.ID, &e.EventType, &e.Direction, &e.PlateNumber,
			&e.VehicleID, &e.GuestPassID, &e.OpenedBy, &e.Status, &e.CreatedAt,
			&e.OwnerName, &e.Brand, &e.Color, &e.GuestName,
		); err != nil {
			internalError(c, "AdminBarrier.ListAll/scan", err)
			return
		}
		events = append(events, e)
	}
	c.JSON(http.StatusOK, events)
}

func (h *AdminBarrierHandler) ListUnknown(c *gin.Context) {
	if !h.requireAdmin(c) {
		return
	}
	rows, err := h.db.Query(context.Background(), `
		SELECT id, event_type, direction, plate_number, vehicle_id, guest_pass_id, opened_by, status, created_at
		FROM barrier_events
		WHERE event_type = 'UNKNOWN' AND status = 'PENDING'
		ORDER BY created_at DESC`)
	if err != nil {
		internalError(c, "AdminBarrier.ListUnknown/query", err)
		return
	}
	defer rows.Close()

	type unknownEvent struct {
		ID          string    `json:"id"`
		EventType   string    `json:"event_type"`
		Direction   *string   `json:"direction"`
		PlateNumber *string   `json:"plate_number"`
		VehicleID   *string   `json:"vehicle_id"`
		GuestPassID *string   `json:"guest_pass_id"`
		OpenedBy    *string   `json:"opened_by"`
		Status      string    `json:"status"`
		CreatedAt   time.Time `json:"created_at"`
	}
	events := []unknownEvent{}
	for rows.Next() {
		var e unknownEvent
		if err := rows.Scan(
			&e.ID, &e.EventType, &e.Direction, &e.PlateNumber,
			&e.VehicleID, &e.GuestPassID, &e.OpenedBy, &e.Status, &e.CreatedAt,
		); err != nil {
			internalError(c, "AdminBarrier.ListUnknown/scan", err)
			return
		}
		events = append(events, e)
	}
	c.JSON(http.StatusOK, events)
}

func (h *AdminBarrierHandler) ApproveUnknown(c *gin.Context) {
	if !h.requireAdmin(c) {
		return
	}
	id := c.Param("id")
	adminID := c.GetString("user_id")

	var direction string
	err := h.db.QueryRow(context.Background(), `
		UPDATE barrier_events
		SET event_type = 'MANUAL', status = 'OPENED', opened_by = $2
		WHERE id = $1 AND status = 'PENDING'
		RETURNING COALESCE(direction, 'IN')`, id, adminID,
	).Scan(&direction)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "event not found or not pending"})
		return
	}

	h.barrierV2.openBarrier(direction)
	c.JSON(http.StatusOK, gin.H{"status": "approved", "direction": direction})
}

func (h *AdminBarrierHandler) RejectUnknown(c *gin.Context) {
	if !h.requireAdmin(c) {
		return
	}
	id := c.Param("id")

	tag, err := h.db.Exec(context.Background(), `
		UPDATE barrier_events SET status = 'REJECTED'
		WHERE id = $1 AND status = 'PENDING'`, id)
	if err != nil {
		internalError(c, "AdminBarrier.RejectUnknown/exec", err)
		return
	}
	if tag.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "event not found or not pending"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "rejected"})
}
