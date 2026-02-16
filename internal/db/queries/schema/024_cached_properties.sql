-- Cached Properties Schema (for sqlc validation)
-- This table stores cached property data from discovery sessions

CREATE TABLE IF NOT EXISTS cached_properties (
    id TEXT PRIMARY KEY,
    listing_id TEXT UNIQUE NOT NULL,
    provider TEXT NOT NULL,
    address TEXT NOT NULL,
    city TEXT NOT NULL,
    state TEXT NOT NULL,
    zip_code TEXT,
    price INTEGER NOT NULL,
    beds INTEGER,
    baths DOUBLE PRECISION,
    sqft INTEGER,
    estimated_rent INTEGER,
    cap_rate DOUBLE PRECISION,
    listing_url TEXT,
    image_url TEXT,
    created_at TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_used_at TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP
);
