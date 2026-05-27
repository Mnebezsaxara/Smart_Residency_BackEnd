-- Fix 6: news/announcements posted by admin (УК), shown on resident dashboard.
-- entrance NULL = visible to the whole complex; otherwise targeted at one подъезд.
CREATE TABLE IF NOT EXISTS news (
    id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    title      VARCHAR(255) NOT NULL,
    body       TEXT         NOT NULL,
    author_id  UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    entrance   INT,
    is_pinned  BOOLEAN      DEFAULT FALSE,
    created_at TIMESTAMPTZ  DEFAULT NOW(),
    updated_at TIMESTAMPTZ  DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_news_entrance ON news(entrance);
CREATE INDEX IF NOT EXISTS idx_news_created  ON news(created_at DESC);
