package handler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"smartresidency/internal/sse"
)

// EventNotifier dispatches an FCM push for a sensor event. Implemented by
// internal/fcm. Kept as an interface so handlers don't depend on FCM directly
// and the server still boots when FCM is disabled (notifier may be nil).
type EventNotifier interface {
	NotifyEvent(ctx context.Context, eventID string) (sent int, err error)
}

// SensorPublisher writes MQTT messages (used by /admin/sensors/:id/reset).
// Implemented by *mqtt.Subscriber. May be nil when MQTT is disabled.
type SensorPublisher interface {
	PublishRetain(topic string, payload any) error
}

type SensorHandler struct {
	db        *pgxpool.Pool
	notifier  EventNotifier
	publisher SensorPublisher
	hub       *sse.Hub
}

func NewSensorHandler(db *pgxpool.Pool, notifier EventNotifier, publisher SensorPublisher, hub *sse.Hub) *SensorHandler {
	return &SensorHandler{db: db, notifier: notifier, publisher: publisher, hub: hub}
}

type Sensor struct {
	ID          string    `json:"id"`
	Type        string    `json:"type"`
	EntranceNum int       `json:"entrance_num"`
	Floor       int       `json:"floor"`
	Status      string    `json:"status"`
	LastUpdated time.Time `json:"last_updated"`
	LastSeenAt  time.Time `json:"last_seen_at"`
}

type SensorEvent struct {
	ID             string     `json:"id"`
	SensorID       string     `json:"sensor_id"`
	Type           string     `json:"type"`
	EntranceNum    int        `json:"entrance_num"`
	Floor          int        `json:"floor"`
	Status         string     `json:"status"`
	ThreatType     *string    `json:"threat_type"`
	AdminComment   *string    `json:"admin_comment"`
	CreatedAt      time.Time  `json:"created_at"`
	ConfirmedAt    *time.Time `json:"confirmed_at"`
	CheckingAt     *time.Time `json:"checking_at"`
	FalseAlarmedAt *time.Time `json:"false_alarmed_at"`
	ResolvedAt     *time.Time `json:"resolved_at"`
	ConfirmedBy    *string    `json:"confirmed_by"`
}

const sensorCols = `id, type, entrance_num, floor, status, last_updated, last_seen_at`
const eventCols = `id, sensor_id, type, entrance_num, floor, status, threat_type, admin_comment, created_at, confirmed_at, checking_at, false_alarmed_at, resolved_at, confirmed_by`

func scanSensors(rows pgx.Rows) ([]Sensor, error) {
	defer rows.Close()
	out := []Sensor{}
	for rows.Next() {
		var s Sensor
		if err := rows.Scan(&s.ID, &s.Type, &s.EntranceNum, &s.Floor, &s.Status, &s.LastUpdated, &s.LastSeenAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func scanEvents(rows pgx.Rows) ([]SensorEvent, error) {
	defer rows.Close()
	out := []SensorEvent{}
	for rows.Next() {
		var e SensorEvent
		if err := rows.Scan(&e.ID, &e.SensorID, &e.Type, &e.EntranceNum, &e.Floor,
			&e.Status, &e.ThreatType, &e.AdminComment, &e.CreatedAt, &e.ConfirmedAt,
			&e.CheckingAt, &e.FalseAlarmedAt, &e.ResolvedAt, &e.ConfirmedBy); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (h *SensorHandler) ListByEntrance(c *gin.Context) {
	e, err := strconv.Atoi(c.Query("entrance"))
	if err != nil || e < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "entrance query param required"})
		return
	}
	rows, err := h.db.Query(c.Request.Context(),
		`SELECT `+sensorCols+` FROM sensors WHERE entrance_num=$1
		 ORDER BY floor ASC, type ASC`, e)
	if err != nil {
		internalError(c, "Sensor.ListByEntrance/query", err)
		return
	}
	out, err := scanSensors(rows)
	if err != nil {
		internalError(c, "Sensor.ListByEntrance/scan", err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func (h *SensorHandler) ListAll(c *gin.Context) {
	if c.GetString("user_role") != "admin" {
		forbiddenAccess(c, "admin only")
		return
	}
	rows, err := h.db.Query(c.Request.Context(),
		`SELECT `+sensorCols+` FROM sensors
		 ORDER BY entrance_num ASC, floor ASC, type ASC`)
	if err != nil {
		internalError(c, "Sensor.ListAll/query", err)
		return
	}
	out, err := scanSensors(rows)
	if err != nil {
		internalError(c, "Sensor.ListAll/scan", err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func (h *SensorHandler) ListEvents(c *gin.Context) {
	role := c.GetString("user_role")
	userID := c.GetString("user_id")
	ctx := c.Request.Context()

	var entranceFilter *int
	if role == "admin" {
		if v := c.Query("entrance"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				entranceFilter = &n
			}
		}
	} else {
		var ent *int
		if err := h.db.QueryRow(ctx,
			`SELECT entrance FROM profiles WHERE id = $1`, userID).Scan(&ent); err != nil || ent == nil {
			c.JSON(http.StatusOK, []SensorEvent{})
			return
		}
		entranceFilter = ent
	}

	statusFilter := c.Query("status")

	q := `SELECT ` + eventCols + ` FROM sensor_events WHERE 1=1`
	args := []any{}
	if entranceFilter != nil {
		args = append(args, *entranceFilter)
		q += fmt.Sprintf(" AND entrance_num = $%d", len(args))
	}
	if statusFilter != "" {
		args = append(args, statusFilter)
		q += fmt.Sprintf(" AND status = $%d", len(args))
	}
	q += " ORDER BY created_at DESC"

	rows, err := h.db.Query(ctx, q, args...)
	if err != nil {
		internalError(c, "Sensor.ListEvents/query", err)
		return
	}
	out, err := scanEvents(rows)
	if err != nil {
		internalError(c, "Sensor.ListEvents/scan", err)
		return
	}
	c.JSON(http.StatusOK, out)
}

type updateEventStatusReq struct {
	Status       string `json:"status" binding:"required"`
	ThreatType   string `json:"threat_type"`
	AdminComment string `json:"admin_comment"`
}

func (h *SensorHandler) UpdateEventStatus(c *gin.Context) {
	if c.GetString("user_role") != "admin" {
		forbiddenAccess(c, "admin only")
		return
	}
	id := c.Param("id")
	var req updateEventStatusReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	allowedStatus := map[string]bool{
		"DETECTED": true, "CHECKING": true, "CONFIRMED": true, "FALSE_ALARM": true, "RESOLVED": true,
	}
	if !allowedStatus[req.Status] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid status"})
		return
	}
	allowedThreat := map[string]bool{"WATER_LEAK": true, "FIRE": true}
	if req.Status == "CONFIRMED" && !allowedThreat[req.ThreatType] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "threat_type (WATER_LEAK|FIRE) required for CONFIRMED"})
		return
	}

	var threatPtr, commentPtr *string
	if req.ThreatType != "" {
		threatPtr = &req.ThreatType
	}
	if req.AdminComment != "" {
		commentPtr = &req.AdminComment
	}

	ctx := c.Request.Context()

	adminID := c.GetString("user_id")
	var adminPtr *string
	if adminID != "" {
		adminPtr = &adminID
	}

	var e SensorEvent
	err := h.db.QueryRow(ctx, `
		UPDATE sensor_events
		SET status           = $2,
		    threat_type      = COALESCE($3, threat_type),
		    admin_comment    = COALESCE($4, admin_comment),
		    confirmed_at     = CASE WHEN $2 = 'CONFIRMED'   THEN NOW() ELSE confirmed_at     END,
		    checking_at      = CASE WHEN $2 = 'CHECKING'    AND checking_at      IS NULL THEN NOW() ELSE checking_at      END,
		    false_alarmed_at = CASE WHEN $2 = 'FALSE_ALARM' AND false_alarmed_at IS NULL THEN NOW() ELSE false_alarmed_at END,
		    resolved_at      = CASE WHEN $2 = 'RESOLVED'    AND resolved_at      IS NULL THEN NOW() ELSE resolved_at      END,
		    confirmed_by     = COALESCE($5, confirmed_by)
		WHERE id = $1
		RETURNING `+eventCols,
		id, req.Status, threatPtr, commentPtr, adminPtr,
	).Scan(&e.ID, &e.SensorID, &e.Type, &e.EntranceNum, &e.Floor,
		&e.Status, &e.ThreatType, &e.AdminComment, &e.CreatedAt, &e.ConfirmedAt,
		&e.CheckingAt, &e.FalseAlarmedAt, &e.ResolvedAt, &e.ConfirmedBy)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "event not found"})
			return
		}
		internalError(c, "Sensor.UpdateEventStatus/exec", err)
		return
	}

	// Both terminal outcomes clear the physical sensor back to NORMAL:
	// FALSE_ALARM (it was never real) and RESOLVED (real alarm dealt with).
	if req.Status == "FALSE_ALARM" || req.Status == "RESOLVED" {
		_, _ = h.db.Exec(ctx,
			`UPDATE sensors SET status='NORMAL', last_updated=NOW() WHERE id=$1`, e.SensorID)
	}

	if h.hub != nil {
		_ = h.hub.Broadcast("event_status", e)
	}

	c.JSON(http.StatusOK, e)
}

func (h *SensorHandler) Stream(c *gin.Context) {
	if c.GetString("user_role") != "admin" {
		c.JSON(http.StatusForbidden, gin.H{"error": "admin only"})
		return
	}
	if h.hub == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "sse hub not configured"})
		return
	}

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming unsupported"})
		return
	}

	ch := h.hub.Subscribe()
	defer h.hub.Unsubscribe(ch)

	_, _ = io.WriteString(c.Writer, ":connected\n\n")
	flusher.Flush()

	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()

	for {
		select {
		case <-c.Request.Context().Done():
			return
		case frame, ok := <-ch:
			if !ok {
				return
			}
			if _, err := c.Writer.Write(frame); err != nil {
				return
			}
			flusher.Flush()
		case <-ping.C:
			if _, err := io.WriteString(c.Writer, ":ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (h *SensorHandler) NotifyEvent(c *gin.Context) {
	if c.GetString("user_role") != "admin" {
		forbiddenAccess(c, "admin only")
		return
	}
	id := c.Param("id")

	var entrance int
	err := h.db.QueryRow(c.Request.Context(),
		`SELECT entrance_num FROM sensor_events WHERE id = $1`, id).Scan(&entrance)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "event not found"})
			return
		}
		internalError(c, "Sensor.NotifyEvent/query", err)
		return
	}

	if h.notifier == nil {
		c.JSON(http.StatusOK, gin.H{"sent": 0, "warning": "fcm not configured"})
		return
	}

	sent, err := h.notifier.NotifyEvent(c.Request.Context(), id)
	if err != nil {
		internalError(c, "Sensor.NotifyEvent/fcm", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"sent": sent})
}

// POST /api/v1/admin/sensors/:id/reset
// Publishes status=NORMAL on the sensor's MQTT topic with retain=true.
// Our subscriber receives the echo and updates the DB; if the broker is
// down nothing changes, which is the desired behaviour (clean state).
func (h *SensorHandler) Reset(c *gin.Context) {
	if c.GetString("user_role") != "admin" {
		c.JSON(http.StatusForbidden, gin.H{"error": "admin only"})
		return
	}
	if h.publisher == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "mqtt publisher not configured"})
		return
	}
	id := c.Param("id")

	var s Sensor
	err := h.db.QueryRow(c.Request.Context(),
		`SELECT `+sensorCols+` FROM sensors WHERE id = $1`, id,
	).Scan(&s.ID, &s.Type, &s.EntranceNum, &s.Floor, &s.Status, &s.LastUpdated, &s.LastSeenAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "sensor not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "server error"})
		return
	}

	topic := fmt.Sprintf("smartresidency/sensors/%d/%d/%s", s.EntranceNum, s.Floor, s.Type)
	payload := map[string]any{
		"id":           s.ID,
		"type":         s.Type,
		"entrance_num": s.EntranceNum,
		"floor":        s.Floor,
		"status":       "NORMAL",
	}
	if err := h.publisher.PublishRetain(topic, payload); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "publish failed: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "topic": topic})
}

// GET /api/v1/sensors/events/:id
// Returns the event, its sensor, and a synthetic timeline assembled from
// the event's transition timestamps. Residents may only see events of
// their own entrance.
func (h *SensorHandler) GetEventDetail(c *gin.Context) {
	ctx := c.Request.Context()
	id := c.Param("id")
	role := c.GetString("user_role")
	userID := c.GetString("user_id")

	var e SensorEvent
	err := h.db.QueryRow(ctx,
		`SELECT `+eventCols+` FROM sensor_events WHERE id = $1`, id,
	).Scan(&e.ID, &e.SensorID, &e.Type, &e.EntranceNum, &e.Floor,
		&e.Status, &e.ThreatType, &e.AdminComment, &e.CreatedAt, &e.ConfirmedAt,
		&e.CheckingAt, &e.FalseAlarmedAt, &e.ResolvedAt, &e.ConfirmedBy)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "event not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "server error"})
		return
	}

	if role != "admin" {
		var ent *int
		_ = h.db.QueryRow(ctx, `SELECT entrance FROM profiles WHERE id=$1`, userID).Scan(&ent)
		if ent == nil || *ent != e.EntranceNum {
			c.JSON(http.StatusForbidden, gin.H{"error": "not your entrance"})
			return
		}
	}

	var s Sensor
	err = h.db.QueryRow(ctx,
		`SELECT `+sensorCols+` FROM sensors WHERE id = $1`, e.SensorID,
	).Scan(&s.ID, &s.Type, &s.EntranceNum, &s.Floor, &s.Status, &s.LastUpdated, &s.LastSeenAt)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load sensor: " + err.Error()})
		return
	}

	timeline := []gin.H{
		{"status": "DETECTED", "at": e.CreatedAt},
	}
	if e.CheckingAt != nil {
		item := gin.H{"status": "CHECKING", "at": *e.CheckingAt}
		if e.ConfirmedBy != nil {
			item["by"] = *e.ConfirmedBy
		}
		timeline = append(timeline, item)
	}
	if e.ConfirmedAt != nil {
		item := gin.H{"status": "CONFIRMED", "at": *e.ConfirmedAt}
		if e.ConfirmedBy != nil {
			item["by"] = *e.ConfirmedBy
		}
		if e.ThreatType != nil {
			item["threat_type"] = *e.ThreatType
		}
		if e.AdminComment != nil {
			item["comment"] = *e.AdminComment
		}
		timeline = append(timeline, item)
	}
	if e.FalseAlarmedAt != nil {
		item := gin.H{"status": "FALSE_ALARM", "at": *e.FalseAlarmedAt}
		if e.ConfirmedBy != nil {
			item["by"] = *e.ConfirmedBy
		}
		if e.AdminComment != nil {
			item["comment"] = *e.AdminComment
		}
		timeline = append(timeline, item)
	}
	if e.ResolvedAt != nil {
		item := gin.H{"status": "RESOLVED", "at": *e.ResolvedAt}
		if e.ConfirmedBy != nil {
			item["by"] = *e.ConfirmedBy
		}
		timeline = append(timeline, item)
	}

	c.JSON(http.StatusOK, gin.H{
		"event":    e,
		"sensor":   s,
		"timeline": timeline,
	})
}

// GET /api/v1/admin/sensors/stats?period=today|week
// Aggregates sensor_events + sensor counts for the admin dashboard.
func (h *SensorHandler) Stats(c *gin.Context) {
	if c.GetString("user_role") != "admin" {
		c.JSON(http.StatusForbidden, gin.H{"error": "admin only"})
		return
	}
	ctx := c.Request.Context()
	period := c.DefaultQuery("period", "today")
	now := time.Now()
	var since time.Time
	switch period {
	case "week":
		since = now.AddDate(0, 0, -7)
	default:
		period = "today"
		since = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	}

	byStatus := map[string]int{}
	total := 0
	if rows, err := h.db.Query(ctx,
		`SELECT status, COUNT(*) FROM sensor_events WHERE created_at >= $1 GROUP BY status`, since); err == nil {
		for rows.Next() {
			var st string
			var n int
			if err := rows.Scan(&st, &n); err == nil {
				byStatus[st] = n
				total += n
			}
		}
		rows.Close()
	}

	byThreat := map[string]int{}
	if rows, err := h.db.Query(ctx,
		`SELECT threat_type, COUNT(*) FROM sensor_events
		 WHERE created_at >= $1 AND threat_type IS NOT NULL
		 GROUP BY threat_type`, since); err == nil {
		for rows.Next() {
			var t string
			var n int
			if err := rows.Scan(&t, &n); err == nil {
				byThreat[t] = n
			}
		}
		rows.Close()
	}

	var avgSec *float64
	_ = h.db.QueryRow(ctx,
		`SELECT AVG(EXTRACT(EPOCH FROM (confirmed_at - created_at)))
		 FROM sensor_events
		 WHERE confirmed_at IS NOT NULL AND created_at >= $1`, since).Scan(&avgSec)
	avgVal := 0.0
	if avgSec != nil {
		avgVal = *avgSec
	}

	falseRate := 0.0
	if total > 0 {
		falseRate = float64(byStatus["FALSE_ALARM"]) / float64(total)
	}

	type topRow struct {
		EntranceNum int `json:"entrance_num"`
		Floor       int `json:"floor"`
		Count       int `json:"count"`
	}
	topFloors := []topRow{}
	if rows, err := h.db.Query(ctx,
		`SELECT entrance_num, floor, COUNT(*) AS c
		 FROM sensor_events WHERE created_at >= $1
		 GROUP BY entrance_num, floor ORDER BY c DESC LIMIT 5`, since); err == nil {
		for rows.Next() {
			var t topRow
			if err := rows.Scan(&t.EntranceNum, &t.Floor, &t.Count); err == nil {
				topFloors = append(topFloors, t)
			}
		}
		rows.Close()
	}

	var offline, totalSensors int
	_ = h.db.QueryRow(ctx,
		`SELECT COUNT(*) FILTER (WHERE status='OFFLINE'), COUNT(*) FROM sensors`,
	).Scan(&offline, &totalSensors)

	c.JSON(http.StatusOK, gin.H{
		"period":               period,
		"total_events":         total,
		"by_status":            byStatus,
		"by_threat":            byThreat,
		"avg_response_seconds": avgVal,
		"false_alarm_rate":     falseRate,
		"top_floors":           topFloors,
		"offline_sensors":      offline,
		"total_sensors":        totalSensors,
	})
}
