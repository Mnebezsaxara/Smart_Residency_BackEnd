-- Add a terminal RESOLVED state to sensor_events. A CONFIRMED real alarm
-- stays red on residents' dashboards until an admin presses "Тревога решена",
-- which sets status='RESOLVED' and stamps resolved_at. FALSE_ALARM remains the
-- separate "it was never real" outcome.
ALTER TABLE sensor_events
    ADD COLUMN IF NOT EXISTS resolved_at TIMESTAMPTZ;
