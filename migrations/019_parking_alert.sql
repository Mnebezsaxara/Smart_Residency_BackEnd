-- Parking "stranger occupied" alert flag.
--
-- The app used to decide whether a parking spot was occupied by a STRANGER
-- (red alarm card + "Сообщить администратору") purely from status == 'occupied'.
-- That misfired: when the owner parked on their own permanent spot, or an
-- expected guest arrived, the spot is legitimately 'occupied' but there is no
-- intrusion. The backend already knows the difference (it checks recent
-- parking-gate entries before sending an FCM alarm), so we now persist that
-- decision on the spot itself. Flutter keys the alarm view off this flag
-- instead of guessing from status.
--
--   alert = true  → occupied by a stranger (real alarm, show "Сообщить")
--   alert = false → free / reserved / occupied by owner or expected guest
ALTER TABLE parking_spots
    ADD COLUMN IF NOT EXISTS alert BOOLEAN NOT NULL DEFAULT false;
