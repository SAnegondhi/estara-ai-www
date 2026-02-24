-- name: SearchLocationsByCity :many
-- Search cities by prefix, ordered by population
SELECT
    id,
    city,
    state_id,
    state_name,
    population
FROM city_states
WHERE city_lower LIKE $1 || '%'
ORDER BY population DESC, city
LIMIT $2;

-- name: SearchLocationsByCityAndState :many
-- Search cities by prefix and state filter, ordered by population
SELECT
    id,
    city,
    state_id,
    state_name,
    population
FROM city_states
WHERE city_lower LIKE $1 || '%' AND state_id = $2
ORDER BY population DESC, city
LIMIT $3;

-- name: FindCitiesNear :many
-- Find cities within radius_miles of (lat, lng), excluding origin city, by population desc.
SELECT city, state_id
FROM city_states
WHERE (
    6371.0 * acos(
        LEAST(1.0,
            cos(radians(sqlc.arg('lat')::float8)) * cos(radians(latitude))
            * cos(radians(longitude) - radians(sqlc.arg('lng')::float8))
            + sin(radians(sqlc.arg('lat')::float8)) * sin(radians(latitude))
        )
    ) * 0.621371
) <= sqlc.arg('radius_miles')::float8
  AND NOT (LOWER(city) = LOWER(sqlc.arg('origin_city')::text) AND state_id = sqlc.arg('origin_state')::text)
  AND population >= sqlc.arg('min_population')::int4
ORDER BY population DESC NULLS LAST
LIMIT sqlc.arg('max_results')::int4;

-- name: GetCityByNameAndState :one
-- Get exact city by name and state for coordinate lookup
SELECT
    id,
    city,
    state_id,
    state_name,
    latitude,
    longitude,
    population
FROM city_states
WHERE LOWER(city) = LOWER(sqlc.arg('city'))
  AND state_id = sqlc.arg('state_id')
ORDER BY population DESC
LIMIT 1;
