-- Staff profile fields: work schedule and description, shown on the resident "Services" page.
ALTER TABLE profiles
    ADD COLUMN IF NOT EXISTS work_schedule VARCHAR(255) DEFAULT '',
    ADD COLUMN IF NOT EXISTS description   TEXT         DEFAULT '';
