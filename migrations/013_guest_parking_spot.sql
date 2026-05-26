ALTER TABLE guest_access
    ADD COLUMN IF NOT EXISTS parking_spot_id UUID REFERENCES parking_spots(id) ON DELETE SET NULL;
