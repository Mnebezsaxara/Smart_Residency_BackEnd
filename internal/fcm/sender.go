package fcm

import (
	"context"
	"fmt"
	"log"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/messaging"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/api/option"
)

type Sender struct {
	db        *pgxpool.Pool
	messaging *messaging.Client
}

func New(ctx context.Context, credPath string, db *pgxpool.Pool) (*Sender, error) {
	app, err := firebase.NewApp(ctx, nil, option.WithCredentialsFile(credPath))
	if err != nil {
		return nil, fmt.Errorf("firebase init: %w", err)
	}
	msg, err := app.Messaging(ctx)
	if err != nil {
		return nil, fmt.Errorf("firebase messaging: %w", err)
	}
	return &Sender{db: db, messaging: msg}, nil
}

// NotifyEvent loads the event, finds approved residents of its entrance,
// sends an FCM data-message to all their tokens, and prunes invalid ones.
func (s *Sender) NotifyEvent(ctx context.Context, eventID string) (int, error) {
	var (
		sensorID, typ, status string
		threat                *string
		entrance, floor       int
	)
	err := s.db.QueryRow(ctx, `
		SELECT sensor_id, type, status, threat_type, entrance_num, floor
		FROM sensor_events WHERE id = $1`, eventID,
	).Scan(&sensorID, &typ, &status, &threat, &entrance, &floor)
	if err != nil {
		return 0, fmt.Errorf("load event: %w", err)
	}

	rows, err := s.db.Query(ctx, `
		SELECT t.token FROM fcm_tokens t
		JOIN profiles p ON p.id = t.user_id
		WHERE p.entrance = $1 AND p.verification_status = 'approved'`, entrance)
	if err != nil {
		return 0, fmt.Errorf("load tokens: %w", err)
	}
	defer rows.Close()

	var tokens []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err == nil {
			tokens = append(tokens, t)
		}
	}
	if len(tokens) == 0 {
		log.Printf("[fcm] event %s: no tokens for entrance %d", eventID, entrance)
		return 0, nil
	}

	threatStr := ""
	if threat != nil {
		threatStr = *threat
	}
	title, body := messageFor(typ, status, threatStr, entrance, floor)

	data := map[string]string{
		"kind":         "sensor_alert",
		"event_id":     eventID,
		"threat_type":  threatStr,
		"entrance_num": fmt.Sprintf("%d", entrance),
		"floor":        fmt.Sprintf("%d", floor),
		"status":       status,
		"title":        title,
		"body":         body,
	}

	sentResidents, err := s.sendMulticast(ctx, tokens, data)
	if err != nil {
		return sentResidents, err
	}

	// Notify admins via FCM
	sentAdmins, _ := s.SendToAdmins(ctx, data)

	// Save bell notification for admins
	notifData := fmt.Sprintf(`{"event_id":"%s","entrance_num":%d,"floor":%d,"type":"%s","status":"%s"}`,
		eventID, entrance, floor, typ, status)
	if _, dbErr := s.db.Exec(ctx, `
		INSERT INTO notifications (target_role, kind, title, body, data)
		VALUES ('admin', 'sensor_alert', $1, $2, $3)`,
		title, body, notifData,
	); dbErr != nil {
		log.Printf("[fcm] sensor_alert bell insert: %v", dbErr)
	}

	return sentResidents + sentAdmins, nil
}

func (s *Sender) sendMulticast(ctx context.Context, tokens []string, data map[string]string) (int, error) {
	if len(tokens) == 0 {
		return 0, nil
	}
	title, body := notificationText(data)
	multicast := &messaging.MulticastMessage{
		Tokens:       tokens,
		Data:         data,
		Notification: &messaging.Notification{Title: title, Body: body},
		Android: &messaging.AndroidConfig{
			Priority: "high",
			Notification: &messaging.AndroidNotification{
				Title:                 title,
				Body:                  body,
				ChannelID:             channelIDFor(data["kind"]),
				Priority:              messaging.PriorityHigh,
				DefaultSound:          true,
				DefaultVibrateTimings: true,
				Visibility:            messaging.VisibilityPublic,
			},
		},
		APNS: &messaging.APNSConfig{
			Payload: &messaging.APNSPayload{
				Aps: &messaging.Aps{
					Alert: &messaging.ApsAlert{
						Title: title,
						Body:  body,
					},
					Sound: "default",
				},
			},
		},
	}
	resp, err := s.messaging.SendEachForMulticast(ctx, multicast)
	if err != nil {
		return 0, fmt.Errorf("send: %w", err)
	}
	log.Printf("[fcm] multicast: success=%d failure=%d", resp.SuccessCount, resp.FailureCount)
	if resp.FailureCount > 0 {
		for i, r := range resp.Responses {
			if r.Success {
				continue
			}
			log.Printf("[fcm] token[%d] error: %v", i, r.Error)
			if messaging.IsUnregistered(r.Error) ||
				messaging.IsSenderIDMismatch(r.Error) {
				log.Printf("[fcm] pruning stale token: %s...", tokens[i][:20])
				_, _ = s.db.Exec(ctx, `DELETE FROM fcm_tokens WHERE token = $1`, tokens[i])
			}
		}
	}
	return resp.SuccessCount, nil
}

func notificationText(data map[string]string) (string, string) {
	title := data["title"]
	body := data["body"]
	if title != "" || body != "" {
		return title, body
	}
	switch data["kind"] {
	case "unknown_vehicle":
		return "Неизвестный автомобиль", fmt.Sprintf("Номер: %s требует решения", data["plate_number"])
	case "parking_alert":
		return "Ваше место занято", fmt.Sprintf("Место %s занято посторонним ТС", data["spot_number"])
	case "parking_spot_freed":
		return "Ваше место освобождено", fmt.Sprintf("Место %s снова свободно", data["spot_number"])
	case "guest_arrived":
		return "Гость въехал", "Гость прибыл на территорию ЖК"
	default:
		return "Smart Residency", "Новое уведомление"
	}
}

func channelIDFor(kind string) string {
	switch kind {
	case "unknown_vehicle", "guest_arrived":
		return "barrier_events_v2"
	case "parking_alert", "parking_spot_freed", "parking_no_permit":
		return "parking_events_v2"
	case "sensor_alert", "sensor_offline":
		return "sensor_alerts_v2"
	default:
		return "smart_residency_events"
	}
}

// NotifyOffline alerts every approved admin that a sensor stopped responding.
// Used by the OFFLINE sweeper goroutine.
func (s *Sender) NotifyOffline(ctx context.Context, sensorID, sensorType string, entrance, floor int) (int, error) {
	kindLabel := "Водяной датчик"
	if sensorType == "SMOKE" {
		kindLabel = "Датчик дыма"
	}
	title := "Датчик не на связи"
	body := fmt.Sprintf("%s %d/%d не отвечает >60 сек", kindLabel, entrance, floor)
	data := map[string]string{
		"kind":         "sensor_offline",
		"sensor_id":    sensorID,
		"sensor_type":  sensorType,
		"entrance_num": fmt.Sprintf("%d", entrance),
		"floor":        fmt.Sprintf("%d", floor),
		"title":        title,
		"body":         body,
	}
	sent, err := s.SendToAdmins(ctx, data)

	// Save bell notification for admins
	notifData := fmt.Sprintf(`{"sensor_id":"%s","sensor_type":"%s","entrance_num":%d,"floor":%d}`,
		sensorID, sensorType, entrance, floor)
	if _, dbErr := s.db.Exec(ctx, `
		INSERT INTO notifications (target_role, kind, title, body, data)
		VALUES ('admin', 'sensor_offline', $1, $2, $3)`,
		title, body, notifData,
	); dbErr != nil {
		log.Printf("[fcm] sensor_offline bell insert: %v", dbErr)
	}

	return sent, err
}

func (s *Sender) SendToAdmins(ctx context.Context, data map[string]string) (int, error) {
	rows, err := s.db.Query(ctx, `
		SELECT t.token FROM fcm_tokens t
		JOIN profiles p ON p.id = t.user_id
		WHERE p.role = 'admin' AND p.verification_status = 'approved'`)
	if err != nil {
		return 0, fmt.Errorf("load admin tokens: %w", err)
	}
	defer rows.Close()
	var tokens []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err == nil {
			tokens = append(tokens, t)
		}
	}
	if len(tokens) == 0 {
		log.Printf("[fcm] no admin tokens for kind=%s", data["kind"])
		return 0, nil
	}
	return s.sendMulticast(ctx, tokens, data)
}

func (s *Sender) SendToUser(ctx context.Context, userID string, data map[string]string) (int, error) {
	rows, err := s.db.Query(ctx, `SELECT token FROM fcm_tokens WHERE user_id = $1`, userID)
	if err != nil {
		return 0, fmt.Errorf("load user tokens: %w", err)
	}
	defer rows.Close()
	var tokens []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err == nil {
			tokens = append(tokens, t)
		}
	}
	if len(tokens) == 0 {
		log.Printf("[fcm] no tokens for user=%s kind=%s", userID, data["kind"])
		return 0, nil
	}
	return s.sendMulticast(ctx, tokens, data)
}

func messageFor(typ, status, threat string, entrance, floor int) (string, string) {
	isFire := typ == "SMOKE" || threat == "FIRE"
	isWater := typ == "WATER" || threat == "WATER_LEAK"

	switch {
	case status == "CONFIRMED" && isFire:
		return "Тревога: пожар",
			fmt.Sprintf("%d подъезд, этаж %d. Проверка подтверждена администратором.", entrance, floor)
	case status == "CONFIRMED" && isWater:
		return "Тревога: затопление",
			fmt.Sprintf("%d подъезд, этаж %d. Проверка подтверждена администратором.", entrance, floor)
	case isFire:
		return "Тревога: возможное задымление",
			fmt.Sprintf("%d подъезд, этаж %d. Сработал датчик дыма.", entrance, floor)
	case isWater:
		return "Тревога: возможное затопление",
			fmt.Sprintf("%d подъезд, этаж %d. Сработал датчик воды.", entrance, floor)
	default:
		return "Тревога", fmt.Sprintf("%d подъезд, этаж %d.", entrance, floor)
	}
}
