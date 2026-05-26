package sensors

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// OfflineNotifier delivers a "sensor is offline" push to admins.
// Implemented by *fcm.Sender. May be nil if FCM is disabled at boot.
type OfflineNotifier interface {
	NotifyOffline(ctx context.Context, sensorID, sensorType string, entrance, floor int) (int, error)
}

// Broadcaster pushes SSE frames. Optional — may be nil.
type Broadcaster interface {
	Broadcast(event string, data any) error
}

// OfflineSweeper periodically marks sensors as OFFLINE when their last MQTT
// message is older than OfflineAfter, and fires a one-shot push for each new
// transition. ONLINE→OFFLINE transitions are detected by the WHERE clause
// `status != 'OFFLINE'`, so a sensor is only announced once per outage.
type OfflineSweeper struct {
	db           *pgxpool.Pool
	notifier     OfflineNotifier
	bcast        Broadcaster
	OfflineAfter time.Duration // sensor considered offline after this gap
	Interval     time.Duration // how often the sweeper runs
}

func NewOfflineSweeper(db *pgxpool.Pool, notifier OfflineNotifier, bcast Broadcaster) *OfflineSweeper {
	return &OfflineSweeper{
		db:           db,
		notifier:     notifier,
		bcast:        bcast,
		OfflineAfter: 60 * time.Second,
		Interval:     15 * time.Second,
	}
}

// Run blocks until ctx is cancelled. Designed to be launched as `go sweeper.Run(ctx)`.
func (s *OfflineSweeper) Run(ctx context.Context) {
	t := time.NewTicker(s.Interval)
	defer t.Stop()
	log.Printf("[sweeper] started, threshold=%s interval=%s", s.OfflineAfter, s.Interval)
	for {
		select {
		case <-ctx.Done():
			log.Println("[sweeper] stopped")
			return
		case <-t.C:
			s.sweepOnce(ctx)
		}
	}
}

func (s *OfflineSweeper) sweepOnce(ctx context.Context) {
	rows, err := s.db.Query(ctx, `
		UPDATE sensors
		SET status = 'OFFLINE', last_updated = NOW()
		WHERE status != 'OFFLINE'
		  AND last_seen_at < NOW() - make_interval(secs => $1)
		RETURNING id, type, entrance_num, floor`,
		int(s.OfflineAfter.Seconds()),
	)
	if err != nil {
		log.Printf("[sweeper] update: %v", err)
		return
	}
	defer rows.Close()

	type offline struct {
		id, typ      string
		entrance, fl int
	}
	var batch []offline
	for rows.Next() {
		var o offline
		if err := rows.Scan(&o.id, &o.typ, &o.entrance, &o.fl); err != nil {
			log.Printf("[sweeper] scan: %v", err)
			continue
		}
		batch = append(batch, o)
	}
	if err := rows.Err(); err != nil {
		log.Printf("[sweeper] rows: %v", err)
	}

	for _, o := range batch {
		log.Printf("[sweeper] %s went OFFLINE (entrance=%d floor=%d)", o.id, o.entrance, o.fl)
		if s.bcast != nil {
			_ = s.bcast.Broadcast("sensor_offline", map[string]any{
				"id":           o.id,
				"type":         o.typ,
				"entrance_num": o.entrance,
				"floor":        o.fl,
				"status":       "OFFLINE",
			})
		}
		if s.notifier == nil {
			continue
		}
		go func(o offline) {
			if _, err := s.notifier.NotifyOffline(context.Background(), o.id, o.typ, o.entrance, o.fl); err != nil {
				log.Printf("[sweeper] notify offline %s: %v", o.id, err)
			}
		}(o)
	}
}
