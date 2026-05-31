package handler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type BarrierNotifier interface {
	SendToAdmins(ctx context.Context, data map[string]string) (int, error)
	SendToUser(ctx context.Context, userID string, data map[string]string) (int, error)
}

type BarrierV2Handler struct {
	db       *pgxpool.Pool
	notifier BarrierNotifier
	publish  func(topic, payload string) error
}

func NewBarrierV2Handler(db *pgxpool.Pool, notifier BarrierNotifier, publish func(topic, payload string) error) *BarrierV2Handler {
	return &BarrierV2Handler{db: db, notifier: notifier, publish: publish}
}

func (h *BarrierV2Handler) openBarrier(direction string) {
	if h.publish == nil {
		return
	}
	payload := fmt.Sprintf(`{"action":"OPEN","direction":"%s"}`, direction)
	if err := h.publish("smartresidency/barrier/command", payload); err != nil {
		log.Printf("[barrier] publish: %v", err)
	}
}

// ProcessScanPlate is the shared business logic for HTTP and MQTT plate recognition.
func (h *BarrierV2Handler) ProcessScanPlate(ctx context.Context, plateNumber, direction, gateID string) (action, eventID string, err error) {
	plate := strings.ToUpper(strings.TrimSpace(plateNumber))
	if direction == "" {
		direction = "IN"
	}
	if gateID == "" {
		gateID = "main-gate"
	}

	// Step 1: registered vehicle
	var vehicleID, vehicleUserID string
	err = h.db.QueryRow(ctx, `
		SELECT id, user_id FROM vehicles WHERE plate_number = $1 AND is_active = true`, plate,
	).Scan(&vehicleID, &vehicleUserID)
	if err == nil {
		h.openBarrier(direction)
		err = h.db.QueryRow(ctx, `
			INSERT INTO barrier_events (event_type, direction, plate_number, vehicle_id, status, gate_id)
			VALUES ('AUTO_RECOGNIZED', $1, $2, $3, 'OPENED', $4)
			RETURNING id`, direction, plate, vehicleID, gateID,
		).Scan(&eventID)
		if err != nil {
			return "", "", err
		}
		return "OPEN", eventID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", "", err
	}

	// Step 2: guest by car_number — atomic via transaction
	tx, txErr := h.db.Begin(ctx)
	if txErr != nil {
		return "", "", txErr
	}

	var guestPassID, guestName, residentID string
	var guestPhone, guestSpotID *string
	err = tx.QueryRow(ctx, `
		SELECT id, guest_name, guest_phone, resident_id, parking_spot_id
		FROM guest_access
		WHERE UPPER(TRIM(car_number)) = $1 AND status = 'active' AND valid_until > NOW()`, plate,
	).Scan(&guestPassID, &guestName, &guestPhone, &residentID, &guestSpotID)
	if err == nil {
		// Car guest with reserved parking spot → only territory entry (first of two scans).
		// Car guest without spot → single scan, mark used immediately.
		hasParking := guestSpotID != nil
		newStatus := "used"
		notifTitle := "Ваш гость въехал"
		notifBody := "Гость " + guestName + " прибыл на территорию ЖК"
		if hasParking {
			newStatus = "arrived"
			notifTitle = "Ваш гость на территории"
			notifBody = "Гость " + guestName + " заехал на территорию ЖК"
		}

		h.openBarrier(direction)
		if _, err = tx.Exec(ctx, `UPDATE guest_access SET status = $2 WHERE id = $1`, guestPassID, newStatus); err != nil {
			_ = tx.Rollback(ctx)
			return "", "", err
		}
		err = tx.QueryRow(ctx, `
			INSERT INTO barrier_events (event_type, direction, plate_number, guest_pass_id, status, gate_id)
			VALUES ('PLATE_SCAN_TERRITORY', $1, $2, $3, 'OPENED', $4)
			RETURNING id`, direction, plate, guestPassID, gateID,
		).Scan(&eventID)
		if err != nil {
			_ = tx.Rollback(ctx)
			return "", "", err
		}
		if err = tx.Commit(ctx); err != nil {
			return "", "", err
		}
		go func(evtID, rID, gName, dir, title, body string) {
			bgCtx := context.Background()
			jsonData := fmt.Sprintf(`{"event_id":"%s","guest_name":"%s","direction":"%s"}`, evtID, gName, dir)
			if _, dbErr := h.db.Exec(bgCtx, `
				INSERT INTO notifications (target_user_id, kind, title, body, data)
				VALUES ($1, 'guest_arrived', $2, $3, $4)`,
				rID, title, body, jsonData,
			); dbErr != nil {
				log.Printf("[barrier] guest bell: %v", dbErr)
			}
			if h.notifier != nil {
				data := map[string]string{
					"event_id":   evtID,
					"kind":       "guest_arrived",
					"guest_name": gName,
					"direction":  dir,
					"title":      title,
					"body":       body,
				}
				sent, err := h.notifier.SendToUser(bgCtx, rID, data)
				if err != nil {
					log.Printf("[barrier] guest arrived fcm: %v", err)
				} else {
					log.Printf("[barrier] guest arrived fcm: resident=%s sent=%d guest=%s", rID, sent, gName)
				}
			}
		}(eventID, residentID, guestName, direction, notifTitle, notifBody)
		return "OPEN", eventID, nil
	}
	_ = tx.Rollback(ctx)
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", "", err
	}

	// Step 3: unknown vehicle
	err = h.db.QueryRow(ctx, `
		INSERT INTO barrier_events (event_type, direction, plate_number, status, gate_id)
		VALUES ('UNKNOWN', $1, $2, 'PENDING', $3)
		RETURNING id`, direction, plate, gateID,
	).Scan(&eventID)
	if err != nil {
		return "", "", err
	}

	// Always persist notification for admin — reliable channel regardless of FCM availability.
	go func(evtID, pl, dir string) {
		body := fmt.Sprintf("Номер: %s пытается въехать", pl)
		data := fmt.Sprintf(`{"event_id":"%s","plate_number":"%s","direction":"%s"}`, evtID, pl, dir)
		if _, dbErr := h.db.Exec(context.Background(), `
			INSERT INTO notifications (target_role, kind, title, body, data)
			VALUES ('admin', 'unknown_vehicle', '⚠️ Неизвестное ТС', $1, $2)`,
			body, data,
		); dbErr != nil {
			log.Printf("[barrier] notification insert: %v", dbErr)
		}
	}(eventID, plate, direction)

	if h.notifier != nil {
		go func(evtID, pl, dir string) {
			data := map[string]string{
				"kind":         "unknown_vehicle",
				"event_id":     evtID,
				"plate_number": pl,
				"direction":    dir,
				"title":        "⚠️ Неизвестное ТС",
				"body":         fmt.Sprintf("Номер: %s пытается въехать", pl),
			}
			sent, err := h.notifier.SendToAdmins(context.Background(), data)
			if err != nil {
				log.Printf("[barrier] unknown vehicle fcm: %v", err)
			} else {
				log.Printf("[barrier] unknown vehicle fcm: admins sent=%d plate=%s", sent, pl)
			}
		}(eventID, plate, direction)
	}
	return "REJECT", eventID, nil
}

func (h *BarrierV2Handler) ScanPlate(c *gin.Context) {
	var req struct {
		PlateNumber string `json:"plate_number" binding:"required"`
		Direction   string `json:"direction"`
		GateID      string `json:"gate_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Direction == "" {
		req.Direction = "IN"
	}

	action, eventID, err := h.ProcessScanPlate(c.Request.Context(), req.PlateNumber, req.Direction, req.GateID)
	if err != nil {
		internalError(c, "BarrierV2.ScanPlate/process", err)
		return
	}
	if action == "REJECT" {
		c.JSON(http.StatusForbidden, gin.H{"action": action, "reason": "UNKNOWN", "event_id": eventID})
		return
	}
	c.JSON(http.StatusOK, gin.H{"action": action, "event_id": eventID})
}

func (h *BarrierV2Handler) ScanQR(c *gin.Context) {
	var req struct {
		QRCode    string `json:"qr_code" binding:"required"`
		Direction string `json:"direction"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Direction == "" {
		req.Direction = "IN"
	}

	ctx := context.Background()

	var id, guestName, residentID, status, accessType string
	var guestPhone, parkingSpotID *string
	var validUntil time.Time
	err := h.db.QueryRow(ctx, `
		SELECT id, guest_name, guest_phone, resident_id, status, valid_until, access_type, parking_spot_id
		FROM guest_access WHERE qr_code = $1`, req.QRCode,
	).Scan(&id, &guestName, &guestPhone, &residentID, &status, &validUntil, &accessType, &parkingSpotID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "QR_NOT_FOUND"})
			return
		}
		internalError(c, "BarrierV2.ScanQR/query", err)
		return
	}
	if status != "active" && status != "arrived" {
		c.JSON(http.StatusForbidden, gin.H{"error": "QR_ALREADY_USED"})
		return
	}
	if time.Now().After(validUntil) {
		c.JSON(http.StatusForbidden, gin.H{"error": "QR_EXPIRED"})
		return
	}

	// Определяем: первое сканирование (территория) или второе (паркинг)
	isCarGuest := accessType == "car"
	isFirstScan := status == "active"

	var newStatus, eventType, notifTitle, notifBody string
	if isCarGuest && isFirstScan {
		newStatus = "arrived"
		eventType = "QR_SCAN_TERRITORY"
		notifTitle = "Ваш гость на территории"
		notifBody = "Гость " + guestName + " заехал на территорию ЖК"
	} else if isCarGuest {
		newStatus = "used"
		eventType = "QR_SCAN_PARKING"
		notifTitle = "Ваш гость в паркинге"
		notifBody = "Гость " + guestName + " припарковался в паркинге"
	} else {
		newStatus = "used"
		eventType = "QR_SCAN_TERRITORY"
		notifTitle = "Ваш гость прибыл"
		notifBody = "Гость " + guestName + " прибыл на территорию ЖК"
	}

	tx, err := h.db.Begin(ctx)
	if err != nil {
		internalError(c, "BarrierV2.ScanQR/begin", err)
		return
	}
	if _, err = tx.Exec(ctx, `UPDATE guest_access SET status = $2 WHERE id = $1`, id, newStatus); err != nil {
		_ = tx.Rollback(ctx)
		internalError(c, "BarrierV2.ScanQR/update", err)
		return
	}
	// When guest parks (second scan) — mark the spot as occupied.
	if newStatus == "used" && parkingSpotID != nil {
		if _, err = tx.Exec(ctx, `UPDATE parking_spots SET status = 'occupied' WHERE id = $1`, *parkingSpotID); err != nil {
			_ = tx.Rollback(ctx)
			internalError(c, "BarrierV2.ScanQR/spotUpdate", err)
			return
		}
	}
	var eventID string
	if err = tx.QueryRow(ctx, `
		INSERT INTO barrier_events (event_type, direction, guest_pass_id, status)
		VALUES ($1, $2, $3, 'OPENED')
		RETURNING id`, eventType, req.Direction, id,
	).Scan(&eventID); err != nil {
		_ = tx.Rollback(ctx)
		internalError(c, "BarrierV2.ScanQR/insert", err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		internalError(c, "BarrierV2.ScanQR/commit", err)
		return
	}

	h.openBarrier(req.Direction)

	go func(evtID, rID, gName, dir, title, body string) {
		bgCtx := context.Background()
		jsonData := fmt.Sprintf(`{"event_id":"%s","guest_name":"%s","direction":"%s"}`, evtID, gName, dir)

		if _, dbErr := h.db.Exec(bgCtx, `
			INSERT INTO notifications (target_user_id, kind, title, body, data)
			VALUES ($1, 'guest_arrived', $2, $3, $4)`,
			rID, title, body, jsonData,
		); dbErr != nil {
			log.Printf("[barrier] guest bell: %v", dbErr)
		}

		if h.notifier != nil {
			sent, err := h.notifier.SendToUser(bgCtx, rID, map[string]string{
				"event_id":   evtID,
				"kind":       "guest_arrived",
				"guest_name": gName,
				"direction":  dir,
				"title":      title,
				"body":       body,
			})
			if err != nil {
				log.Printf("[barrier] guest fcm: %v", err)
			} else {
				log.Printf("[barrier] guest fcm: resident=%s sent=%d guest=%s", rID, sent, gName)
			}
		}
	}(eventID, residentID, guestName, req.Direction, notifTitle, notifBody)

	phoneStr := ""
	if guestPhone != nil {
		phoneStr = *guestPhone
	}
	c.JSON(http.StatusOK, gin.H{
		"action":      "OPEN",
		"guest_name":  guestName,
		"guest_phone": phoneStr,
	})
}

func (h *BarrierV2Handler) OpenManual(c *gin.Context) {
	if !isBarrierStaff(c, h.db) {
		forbiddenAccess(c, "admin or security only")
		return
	}
	var req struct {
		Direction string `json:"direction"`
		GateID    string `json:"gate_id"`
	}
	_ = c.ShouldBindJSON(&req)
	if req.Direction == "" {
		req.Direction = "IN"
	}
	if req.GateID == "" {
		req.GateID = "main-gate"
	}

	userID := c.GetString("user_id")
	h.openBarrier(req.Direction)

	var eventID string
	err := h.db.QueryRow(context.Background(), `
		INSERT INTO barrier_events (event_type, direction, opened_by, status, gate_id)
		VALUES ('MANUAL', $1, $2, 'OPENED', $3)
		RETURNING id`, req.Direction, userID, req.GateID,
	).Scan(&eventID)
	if err != nil {
		internalError(c, "BarrierV2.OpenManual/insert", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"action": "OPENED", "direction": req.Direction, "event_id": eventID})
}

func (h *BarrierV2Handler) ListEvents(c *gin.Context) {
	userID := c.GetString("user_id")
	ctx := context.Background()

	type barrierEvent struct {
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

	var query string
	var args []any

	if isBarrierStaff(c, h.db) {
		query = `SELECT id, event_type, direction, plate_number, vehicle_id, guest_pass_id, opened_by, status, created_at
		         FROM barrier_events
		         ORDER BY created_at DESC LIMIT 100`
	} else {
		query = `SELECT be.id, be.event_type, be.direction, be.plate_number, be.vehicle_id, be.guest_pass_id, be.opened_by, be.status, be.created_at
		         FROM barrier_events be
		         LEFT JOIN vehicles v ON v.id = be.vehicle_id
		         LEFT JOIN guest_access ga ON ga.id = be.guest_pass_id
		         WHERE v.user_id = $1 OR ga.resident_id = $1 OR be.opened_by = $1
		         ORDER BY be.created_at DESC LIMIT 50`
		args = []any{userID}
	}

	rows, err := h.db.Query(ctx, query, args...)
	if err != nil {
		internalError(c, "BarrierV2.ListEvents/query", err)
		return
	}
	defer rows.Close()

	events := []barrierEvent{}
	for rows.Next() {
		var e barrierEvent
		if err := rows.Scan(&e.ID, &e.EventType, &e.Direction, &e.PlateNumber, &e.VehicleID, &e.GuestPassID, &e.OpenedBy, &e.Status, &e.CreatedAt); err != nil {
			internalError(c, "BarrierV2.ListEvents/scan", err)
			return
		}
		events = append(events, e)
	}
	c.JSON(http.StatusOK, events)
}
