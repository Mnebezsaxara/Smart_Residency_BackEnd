package mqtt

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	paho "github.com/eclipse/paho.mqtt.golang"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Notifier interface {
	NotifyEvent(ctx context.Context, eventID string) (int, error)
}

type Broadcaster interface {
	Broadcast(event string, data any) error
}

type ParkingNotifier interface {
	SendToUser(ctx context.Context, userID string, data map[string]string) (int, error)
	SendToAdmins(ctx context.Context, data map[string]string) (int, error)
}

type Config struct {
	URL      string
	Username string
	Password string
	ClientID string
}

type Subscriber struct {
	db                  *pgxpool.Pool
	notifier            Notifier
	bcast               Broadcaster
	parkingNotifier     ParkingNotifier
	client              paho.Client
	barrierCallback     func(ctx context.Context, plate, direction, gateID string) (string, string, error)
	parkingGateCallback func(ctx context.Context, plate, direction, gateID string) (string, string, error)
}

func New(cfg Config, db *pgxpool.Pool, notifier Notifier, bcast Broadcaster) (*Subscriber, error) {
	s := &Subscriber{db: db, notifier: notifier, bcast: bcast}

	opts := paho.NewClientOptions()
	opts.AddBroker(cfg.URL)
	opts.SetClientID(cfg.ClientID)
	opts.SetUsername(cfg.Username)
	opts.SetPassword(cfg.Password)
	opts.SetAutoReconnect(true)
	opts.SetConnectRetry(true)
	opts.SetCleanSession(true)
	opts.SetTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12})

	opts.SetOnConnectHandler(func(c paho.Client) {
		log.Println("[mqtt] connected")
		t := c.Subscribe("smartresidency/sensors/+/+/+", 1, s.handleSensor)
		t.Wait()
		if err := t.Error(); err != nil {
			log.Printf("[mqtt] subscribe sensors FAILED: %v", err)
		} else {
			log.Println("[mqtt] subscribed to smartresidency/sensors/+/+/+ (QoS 1)")
		}
		t2 := c.Subscribe("smartresidency/events/+", 1, s.handleEvent)
		t2.Wait()
		if err := t2.Error(); err != nil {
			log.Printf("[mqtt] subscribe events FAILED: %v", err)
		} else {
			log.Println("[mqtt] subscribed to smartresidency/events/+ (QoS 1)")
		}
		t3 := c.Subscribe("smartresidency/barrier/camera/plate", 1, s.handleBarrier)
		t3.Wait()
		if err := t3.Error(); err != nil {
			log.Printf("[mqtt] subscribe barrier/camera/plate FAILED: %v", err)
		} else {
			log.Println("[mqtt] subscribed to smartresidency/barrier/camera/plate (QoS 1)")
		}
		t4 := c.Subscribe("smartresidency/barrier/motion", 1, s.handleMotion)
		t4.Wait()
		if err := t4.Error(); err != nil {
			log.Printf("[mqtt] subscribe barrier/motion FAILED: %v", err)
		} else {
			log.Println("[mqtt] subscribed to smartresidency/barrier/motion (QoS 1)")
		}
		t5 := c.Subscribe("smartresidency/parking/spots/+", 1, s.handleParking)
		t5.Wait()
		if err := t5.Error(); err != nil {
			log.Printf("[mqtt] subscribe parking FAILED: %v", err)
		} else {
			log.Println("[mqtt] subscribed to smartresidency/parking/spots/+ (QoS 1)")
		}
	})
	opts.SetConnectionLostHandler(func(_ paho.Client, err error) {
		log.Printf("[mqtt] disconnected: %v", err)
	})

	s.client = paho.NewClient(opts)
	if t := s.client.Connect(); t.Wait() && t.Error() != nil {
		return nil, t.Error()
	}
	return s, nil
}

type sensorMsg struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	EntranceNum int    `json:"entrance_num"`
	Floor       int    `json:"floor"`
	Status      string `json:"status"`
}

type eventMsg struct {
	SensorID    string `json:"sensor_id"`
	Type        string `json:"type"`
	EntranceNum int    `json:"entrance_num"`
	Floor       int    `json:"floor"`
	Status      string `json:"status"`
}

func (s *Subscriber) handleSensor(_ paho.Client, m paho.Message) {
	log.Printf("[mqtt] RX %s: %s", m.Topic(), string(m.Payload()))
	var msg sensorMsg
	if err := json.Unmarshal(m.Payload(), &msg); err != nil {
		log.Printf("[mqtt] sensor payload: %v", err)
		return
	}
	if msg.ID == "" || msg.Status == "" {
		log.Printf("[mqtt] sensor payload: missing id/status")
		return
	}
	ctx := context.Background()

	var prev string
	hadRow := s.db.QueryRow(ctx, `SELECT status FROM sensors WHERE id=$1`, msg.ID).Scan(&prev) == nil

	// Upsert. last_seen_at = NOW() on EVERY message including heartbeat pings,
	// so the OFFLINE sweeper can tell live from dead sensors.
	_, err := s.db.Exec(ctx, `
		INSERT INTO sensors (id, type, entrance_num, floor, status, last_updated, last_seen_at)
		VALUES ($1, $2, $3, $4, $5, NOW(), NOW())
		ON CONFLICT (id) DO UPDATE
		SET status = EXCLUDED.status,
		    last_updated = NOW(),
		    last_seen_at = NOW()`,
		msg.ID, msg.Type, msg.EntranceNum, msg.Floor, msg.Status,
	)
	if err != nil {
		log.Printf("[mqtt] sensor upsert %s: %v", msg.ID, err)
		return
	}

	// Broadcast sensor_update only on status changes — heartbeats (status
	// unchanged) would otherwise flood the SSE channel 54 times every 30 sec.
	if s.bcast != nil && (!hadRow || prev != msg.Status) {
		_ = s.bcast.Broadcast("sensor_update", map[string]any{
			"id":           msg.ID,
			"type":         msg.Type,
			"entrance_num": msg.EntranceNum,
			"floor":        msg.Floor,
			"status":       msg.Status,
		})
	}

	// Event creation rules:
	//   NORMAL→ALERT   : new event, push residents (alarm raised).
	//   OFFLINE→ALERT  : new event, push residents (sensor came back already firing).
	//   OFFLINE→NORMAL : silent recovery, no event, no push.
	//   NORMAL→NORMAL  : heartbeat, only last_seen_at is touched (above).
	//   ALERT→NORMAL   : sensor cleared itself / reset endpoint, no push.
	//   ALERT→ALERT    : duplicate, ignore.
	shouldCreateEvent := hadRow && msg.Status == "ALERT" && (prev == "NORMAL" || prev == "OFFLINE")
	if shouldCreateEvent {
		var eventID string
		err := s.db.QueryRow(ctx, `
			INSERT INTO sensor_events (id, sensor_id, type, entrance_num, floor, status)
			VALUES ('evt-' || nextval('sensor_events_seq'), $1, $2, $3, $4, 'DETECTED')
			RETURNING id`,
			msg.ID, msg.Type, msg.EntranceNum, msg.Floor,
		).Scan(&eventID)
		if err != nil {
			log.Printf("[mqtt] event insert: %v", err)
			return
		}
		log.Printf("[mqtt] %s: %s→ALERT, event %s", msg.ID, prev, eventID)
		if s.bcast != nil {
			_ = s.bcast.Broadcast("event_new", map[string]any{
				"id":           eventID,
				"sensor_id":    msg.ID,
				"type":         msg.Type,
				"entrance_num": msg.EntranceNum,
				"floor":        msg.Floor,
				"status":       "DETECTED",
			})
		}
		if s.notifier != nil {
			go func(id string) {
				if _, err := s.notifier.NotifyEvent(context.Background(), id); err != nil {
					log.Printf("[mqtt] notify %s: %v", id, err)
				}
			}(eventID)
		}
	}
}

func (s *Subscriber) handleEvent(_ paho.Client, m paho.Message) {
	var msg eventMsg
	if err := json.Unmarshal(m.Payload(), &msg); err != nil {
		log.Printf("[mqtt] event payload: %v", err)
		return
	}
	if msg.SensorID == "" {
		log.Printf("[mqtt] event payload: missing sensor_id")
		return
	}
	status := msg.Status
	if status == "" {
		status = "DETECTED"
	}

	var eventID string
	err := s.db.QueryRow(context.Background(), `
		INSERT INTO sensor_events (id, sensor_id, type, entrance_num, floor, status)
		VALUES ('evt-' || nextval('sensor_events_seq'), $1, $2, $3, $4, $5)
		RETURNING id`,
		msg.SensorID, msg.Type, msg.EntranceNum, msg.Floor, status,
	).Scan(&eventID)
	if err != nil {
		log.Printf("[mqtt] event insert: %v", err)
		return
	}
	log.Printf("[mqtt] event %s from %s", eventID, msg.SensorID)
	if s.notifier != nil {
		go func(id string) {
			if _, err := s.notifier.NotifyEvent(context.Background(), id); err != nil {
				log.Printf("[mqtt] notify %s: %v", id, err)
			}
		}(eventID)
	}
}

type barrierMsg struct {
	PlateNumber string  `json:"plate_number"`
	Direction   string  `json:"direction"`
	SensorID    string  `json:"sensor_id"`
	GateID      string  `json:"gate_id"`
	Confidence  float64 `json:"confidence"`
	Status      string  `json:"status"`
}

func (s *Subscriber) handleBarrier(_ paho.Client, m paho.Message) {
	log.Printf("[mqtt] RX %s: %s", m.Topic(), string(m.Payload()))
	var msg barrierMsg
	if err := json.Unmarshal(m.Payload(), &msg); err != nil {
		log.Printf("[mqtt] barrier payload: %v", err)
		return
	}
	if msg.Status == "UNREADABLE" || msg.PlateNumber == "" {
		log.Printf("[mqtt] barrier camera: UNREADABLE gate=%s confidence=%.2f — skipped",
			msg.GateID, msg.Confidence)
		return
	}
	if msg.GateID == "parking-gate" {
		if s.parkingGateCallback != nil {
			go func() {
				action, eventID, err := s.parkingGateCallback(context.Background(), msg.PlateNumber, msg.Direction, msg.GateID)
				if err != nil {
					log.Printf("[mqtt] parking gate callback: %v", err)
					return
				}
				logGateDecision("parking-gate", msg.PlateNumber, msg.Direction, action, eventID)
			}()
		}
		return
	}
	if s.barrierCallback != nil {
		go func() {
			gateID := msg.GateID
			if gateID == "" {
				gateID = "main-gate"
			}
			action, eventID, err := s.barrierCallback(context.Background(), msg.PlateNumber, msg.Direction, gateID)
			if err != nil {
				log.Printf("[mqtt] barrier callback: %v", err)
				return
			}
			logGateDecision(gateID, msg.PlateNumber, msg.Direction, action, eventID)
		}()
	}
}

func logGateDecision(gateID, plate, direction, action, eventID string) {
	if direction == "" {
		direction = "IN"
	}
	switch action {
	case "OPEN":
		log.Printf("[mqtt] gate decision: gate=%s plate=%s direction=%s access=GRANTED event_id=%s",
			gateID, plate, direction, eventID)
	case "REJECT":
		log.Printf("[mqtt] gate decision: gate=%s plate=%s direction=%s access=DENIED event_id=%s",
			gateID, plate, direction, eventID)
	default:
		log.Printf("[mqtt] gate decision: gate=%s plate=%s direction=%s action=%s event_id=%s",
			gateID, plate, direction, action, eventID)
	}
}

func (s *Subscriber) SetBarrierCallback(f func(ctx context.Context, plate, direction, gateID string) (string, string, error)) {
	s.barrierCallback = f
}

func (s *Subscriber) SetParkingGateCallback(f func(ctx context.Context, plate, direction, gateID string) (string, string, error)) {
	s.parkingGateCallback = f
}

type motionMsg struct {
	SensorID  string `json:"sensor_id"`
	GateID    string `json:"gate_id"`
	Status    string `json:"status"`
	Direction string `json:"direction"`
}

func (s *Subscriber) handleMotion(_ paho.Client, m paho.Message) {
	log.Printf("[mqtt] RX %s: %s", m.Topic(), string(m.Payload()))
	var msg motionMsg
	if err := json.Unmarshal(m.Payload(), &msg); err != nil {
		log.Printf("[mqtt] motion payload: %v", err)
		return
	}
	log.Printf("[mqtt] motion detected: gate=%s sensor=%s direction=%s — waiting for camera",
		msg.GateID, msg.SensorID, msg.Direction)
}

func (s *Subscriber) SetParkingNotifier(n ParkingNotifier) {
	s.parkingNotifier = n
}

type parkingMsg struct {
	SpotNumber string `json:"spot_number"`
	EventType  string `json:"event_type"`
}

func (s *Subscriber) handleParking(_ paho.Client, m paho.Message) {
	log.Printf("[mqtt] RX %s: %s", m.Topic(), string(m.Payload()))
	var msg parkingMsg
	if err := json.Unmarshal(m.Payload(), &msg); err != nil {
		log.Printf("[mqtt] parking payload: %v", err)
		return
	}
	if msg.SpotNumber == "" || msg.EventType == "" {
		log.Printf("[mqtt] parking payload: missing spot_number or event_type")
		return
	}
	msg.EventType = strings.ToLower(msg.EventType)
	if msg.EventType == "motion" {
		log.Printf("[mqtt] parking motion detected at spot=%s — waiting for confirmed occupied", msg.SpotNumber)
		return
	}
	if msg.EventType != "occupied" && msg.EventType != "freed" {
		log.Printf("[mqtt] parking payload: unsupported event_type=%q", msg.EventType)
		return
	}

	ctx := context.Background()

	var spotID, spotType string
	var assignedUserID *string
	err := s.db.QueryRow(ctx,
		`SELECT id, type, assigned_user_id FROM parking_spots WHERE spot_number = $1`, msg.SpotNumber,
	).Scan(&spotID, &spotType, &assignedUserID)
	if err != nil {
		log.Printf("[mqtt] parking spot %q not found: %v", msg.SpotNumber, err)
		return
	}

	// Для гостевых мест заранее ищем активный пропуск — он влияет на newStatus
	var guestPassID, guestResidentID, guestCarNumber string
	hasActiveGuestPass := false
	if spotType == "guest" {
		err2 := s.db.QueryRow(ctx, `
			SELECT id, resident_id, COALESCE(car_number, '')
			FROM guest_access
			WHERE parking_spot_id = $1
			  AND status IN ('active', 'arrived')
			  AND valid_from <= NOW()
			  AND valid_until >= NOW()
			LIMIT 1
		`, spotID).Scan(&guestPassID, &guestResidentID, &guestCarNumber)
		hasActiveGuestPass = (err2 == nil)
	}

	// Если гостевое место освобождается, но пропуск ещё активен — оставляем reserved
	newStatus := "free"
	if msg.EventType == "occupied" {
		newStatus = "occupied"
	} else if msg.EventType == "freed" && spotType == "guest" && hasActiveGuestPass {
		newStatus = "reserved"
	}

	if _, err := s.db.Exec(ctx,
		`UPDATE parking_spots SET status = $1 WHERE id = $2`, newStatus, spotID); err != nil {
		log.Printf("[mqtt] parking update spot %s: %v", msg.SpotNumber, err)
	}
	if _, err := s.db.Exec(ctx,
		`INSERT INTO parking_events (spot_id, event_type) VALUES ($1, $2)`, spotID, msg.EventType); err != nil {
		log.Printf("[mqtt] parking insert event: %v", err)
	}

	if s.parkingNotifier == nil {
		return
	}

	// ── Постоянные места ─────────────────────────────────────────────────────
	if spotType == "permanent" && assignedUserID != nil {
		uid := *assignedUserID
		sn := msg.SpotNumber
		if msg.EventType == "occupied" {
			var ownerEntries int
			_ = s.db.QueryRow(ctx, `
				SELECT COUNT(*)
				FROM barrier_events be
				JOIN vehicles v ON v.id = be.vehicle_id
				WHERE v.user_id    = $1
				  AND be.direction = 'IN'
				  AND be.status    = 'OPENED'
				  AND be.gate_id   = 'parking-gate'
				  AND be.created_at > NOW() - INTERVAL '30 minutes'
			`, uid).Scan(&ownerEntries)

			if ownerEntries > 0 {
				log.Printf("[mqtt] parking spot=%s occupied: owner vehicle entered recently (%d event(s)) — skip fcm",
					sn, ownerEntries)
			} else {
				go func() {
					data := map[string]string{
						"kind":        "parking_alert",
						"spot_id":     spotID,
						"spot_number": sn,
						"title":       "Ваше место занято",
						"body":        "Место " + sn + " занято посторонним ТС",
					}
					sent, err := s.parkingNotifier.SendToUser(context.Background(), uid, data)
					if err != nil {
						log.Printf("[mqtt] parking fcm occupied: %v", err)
					} else {
						log.Printf("[mqtt] parking fcm occupied: user=%s sent=%d spot=%s", uid, sent, sn)
					}
				}()
			}
		} else if msg.EventType == "freed" {
			go func() {
				data := map[string]string{
					"kind":        "parking_spot_freed",
					"spot_id":     spotID,
					"spot_number": sn,
					"title":       "Ваше место освобождено",
					"body":        "Место " + sn + " снова свободно",
				}
				sent, err := s.parkingNotifier.SendToUser(context.Background(), uid, data)
				if err != nil {
					log.Printf("[mqtt] parking fcm freed: %v", err)
				} else {
					log.Printf("[mqtt] parking fcm freed: user=%s sent=%d spot=%s", uid, sent, sn)
				}
			}()
		}
	}

	// ── Гостевые места ───────────────────────────────────────────────────────
	if spotType == "guest" && hasActiveGuestPass && guestCarNumber != "" {
		sn := msg.SpotNumber

		if msg.EventType == "occupied" {
			// Въезжала ли ожидаемая машина в паркинг недавно?
			// Учитываем: распознавание номера на parking-gate ИЛИ QR-скан охранником на паркинге
			var guestEntries int
			_ = s.db.QueryRow(ctx, `
				SELECT COUNT(*)
				FROM barrier_events
				WHERE (
				    (plate_number = $1 AND gate_id = 'parking-gate')
				    OR
				    (guest_pass_id = $2 AND event_type = 'QR_SCAN_PARKING')
				)
				  AND direction = 'IN'
				  AND status    = 'OPENED'
				  AND created_at > NOW() - INTERVAL '30 minutes'
			`, guestCarNumber, guestPassID).Scan(&guestEntries)

			if guestEntries > 0 {
				// Это наш гость — помечаем пропуск как использованный
				_, _ = s.db.Exec(ctx, `UPDATE guest_access SET status = 'used' WHERE id = $1`, guestPassID)
				log.Printf("[mqtt] parking guest spot=%s: expected car %s arrived — pass marked used", sn, guestCarNumber)
				return
			}

			// Чужая машина заняла место — уведомляем
			go func(rID, cn, sn, pID, sID string) {
				bgCtx := context.Background()
				userBody := fmt.Sprintf("Гостевое место %s занято чужим автомобилем. Ожидаемый гость (%s) ещё не приехал.", sn, cn)
				adminBody := fmt.Sprintf("Гостевое место %s занято чужим ТС. Ожидается %s.", sn, cn)
				jsonData := fmt.Sprintf(`{"spot_id":"%s","spot_number":"%s","expected_car":"%s","pass_id":"%s"}`, sID, sn, cn, pID)

				_, _ = s.db.Exec(bgCtx, `
					INSERT INTO notifications (target_user_id, kind, title, body, data)
					VALUES ($1, 'parking_alert', '🚗 Гостевое место занято', $2, $3)`,
					rID, userBody, jsonData)
				_, _ = s.db.Exec(bgCtx, `
					INSERT INTO notifications (target_role, kind, title, body, data)
					VALUES ('admin', 'parking_alert', '⚠️ Гостевое место занято', $1, $2)`,
					adminBody, jsonData)

				_, _ = s.parkingNotifier.SendToUser(bgCtx, rID, map[string]string{
					"kind": "parking_alert", "spot_id": sID, "spot_number": sn,
					"title": "🚗 Гостевое место занято", "body": userBody,
				})
				_, _ = s.parkingNotifier.SendToAdmins(bgCtx, map[string]string{
					"kind": "parking_alert", "spot_id": sID, "spot_number": sn,
					"title": "⚠️ Гостевое место занято", "body": adminBody,
				})
			}(guestResidentID, guestCarNumber, sn, guestPassID, spotID)

		} else if msg.EventType == "freed" {
			// Чужая машина уехала, пропуск ещё активен — место осталось reserved, уведомляем
			go func(rID, cn, sn, pID, sID string) {
				bgCtx := context.Background()
				userBody := fmt.Sprintf("Место %s освободилось, но Ваш гость (%s) ещё не приехал. Место остаётся забронированным.", sn, cn)
				adminBody := fmt.Sprintf("Гостевое место %s освободилось. Пропуск для %s ещё активен — место забронировано.", sn, cn)
				jsonData := fmt.Sprintf(`{"spot_id":"%s","spot_number":"%s","expected_car":"%s","pass_id":"%s"}`, sID, sn, cn, pID)

				_, _ = s.db.Exec(bgCtx, `
					INSERT INTO notifications (target_user_id, kind, title, body, data)
					VALUES ($1, 'parking_spot_freed', '🅿️ Гостевое место освободилось', $2, $3)`,
					rID, userBody, jsonData)
				_, _ = s.db.Exec(bgCtx, `
					INSERT INTO notifications (target_role, kind, title, body, data)
					VALUES ('admin', 'parking_spot_freed', '🅿️ Гостевое место освободилось', $1, $2)`,
					adminBody, jsonData)

				_, _ = s.parkingNotifier.SendToUser(bgCtx, rID, map[string]string{
					"kind": "parking_spot_freed", "spot_id": sID, "spot_number": sn,
					"title": "🅿️ Гостевое место освободилось", "body": userBody,
				})
				_, _ = s.parkingNotifier.SendToAdmins(bgCtx, map[string]string{
					"kind": "parking_spot_freed", "spot_id": sID, "spot_number": sn,
					"title": "🅿️ Гостевое место освободилось", "body": adminBody,
				})
			}(guestResidentID, guestCarNumber, sn, guestPassID, spotID)
		}
	}
}

// Publish sends a pre-serialized string payload. Used by barrier and parking handlers.
func (s *Subscriber) Publish(topic, payload string) error {
	t := s.client.Publish(topic, 1, false, payload)
	t.Wait()
	return t.Error()
}

// PublishRetain marshals payload to JSON and publishes with retain=true.
// Used by POST /admin/sensors/:id/reset so the broker keeps the last sensor state.
func (s *Subscriber) PublishRetain(topic string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	t := s.client.Publish(topic, 1, true, data)
	t.Wait()
	return t.Error()
}

func (s *Subscriber) Close() {
	if s.client != nil {
		s.client.Disconnect(250)
	}
}
