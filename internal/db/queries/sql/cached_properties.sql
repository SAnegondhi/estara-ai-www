-- Cached Properties Queries

-- name: GetCachedPropertiesSummaryByIDs :many
SELECT id, city, state, address FROM cached_properties WHERE id = ANY($1::text[]);

-- name: GetCachedPropertiesDetailByIDs :many
SELECT id, listing_id, address, city, state, zip_code, price, beds, baths, sqft,
       estimated_rent, cap_rate, listing_url, image_url
FROM cached_properties
WHERE id = ANY($1::text[]);

-- name: UpsertCachedProperty :one
INSERT INTO cached_properties (id, listing_id, provider, address, city, state, zip_code, price, beds, baths, sqft, estimated_rent, cap_rate, listing_url, image_url)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
ON CONFLICT (listing_id) DO UPDATE SET last_used_at = NOW()
RETURNING id;

-- name: CountCachedPropertiesByCity :one
SELECT COUNT(*) FROM cached_properties
WHERE LOWER(city) = LOWER($1) AND UPPER(state) = $2;
