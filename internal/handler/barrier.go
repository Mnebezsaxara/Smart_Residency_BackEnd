package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type BarrierHandler struct{ db *pgxpool.Pool }

func NewBarrierHandler(db *pgxpool.Pool) *BarrierHandler { return &BarrierHandler{db: db} }

// isBarrierStaff reports whether the caller may use security/barrier endpoints:
// admins, or staff whose specialty is 'security' (formerly the standalone
// 'guard' role, collapsed into staff with specialty='security' in migration 017).
func isBarrierStaff(c *gin.Context, db *pgxpool.Pool) bool {
	switch c.GetString("user_role") {
	case "admin":
		return true
	case "staff":
		var specialty string
		err := db.QueryRow(context.Background(),
			`SELECT COALESCE(specialty, '') FROM profiles WHERE id = $1`,
			c.GetString("user_id")).Scan(&specialty)
		return err == nil && specialty == "security"
	default:
		return false
	}
}

type barrierLog struct {
	ID         string    `json:"id"`
	UserID     *string   `json:"user_id"`
	AccessType *string   `json:"access_type"`
	Direction  *string   `json:"direction"`
	CarNumber  *string   `json:"car_number"`
	Notes      *string   `json:"notes"`
	CreatedAt  time.Time `json:"created_at"`
}

func (h *BarrierHandler) OpenBarrier(c *gin.Context) {
	userID := c.GetString("user_id")
	_, err := h.db.Exec(context.Background(),
		`INSERT INTO barrier_logs (id, user_id, access_type, direction, notes)
		 VALUES ($1, $2, 'car', 'in', 'Открытие из приложения')`,
		uuid.New().String(), userID,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "server error"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"result": "success"})
}

func (h *BarrierHandler) List(c *gin.Context) {
	userID := c.GetString("user_id")
	ctx := context.Background()

	var rows interface {
		Next() bool
		Scan(...any) error
		Close()
		Err() error
	}
	var err error

	if isBarrierStaff(c, h.db) {
		rows, err = h.db.Query(ctx,
			`SELECT id, user_id, access_type, direction, car_number, notes, created_at
			 FROM barrier_logs ORDER BY created_at DESC LIMIT 200`)
	} else {
		rows, err = h.db.Query(ctx,
			`SELECT id, user_id, access_type, direction, car_number, notes, created_at
			 FROM barrier_logs WHERE user_id = $1 ORDER BY created_at DESC LIMIT 100`, userID)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "server error"})
		return
	}
	defer rows.Close()

	logs := []barrierLog{}
	for rows.Next() {
		var l barrierLog
		if err := rows.Scan(&l.ID, &l.UserID, &l.AccessType, &l.Direction,
			&l.CarNumber, &l.Notes, &l.CreatedAt); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "server error"})
			return
		}
		logs = append(logs, l)
	}
	c.JSON(http.StatusOK, logs)
}
