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
