-- name: GetLatestMetroData :one
SELECT * FROM metro_timeseries
WHERE region_name = $1 AND region_type = $2
ORDER BY date DESC
LIMIT 1;

-- name: GetLatestMetroDataByID :one
SELECT * FROM metro_timeseries
WHERE region_id = $1
ORDER BY date DESC
LIMIT 1;

-- name: GetMetroTimeSeries :many
SELECT * FROM metro_timeseries
WHERE region_name = $1 AND region_type = $2
AND date >= $3 AND date <= $4
ORDER BY date ASC;

-- name: GetMetroTimeSeriesByID :many
SELECT * FROM metro_timeseries
WHERE region_id = $1
AND date >= $2 AND date <= $3
ORDER BY date ASC;

-- name: GetMetroDataByState :many
SELECT m.* FROM metro_timeseries m
WHERE m.state_code = $1 AND m.region_type = $2
AND m.date = (SELECT MAX(sub.date) FROM metro_timeseries sub WHERE sub.state_code = $1)
ORDER BY m.region_name;

-- name: SearchMetroByName :many
SELECT DISTINCT region_id, region_name, region_type, state_code, metro
FROM metro_timeseries
WHERE region_name ILIKE $1
ORDER BY region_name
LIMIT $2;

-- name: GetCityMetroMapping :one
SELECT * FROM city_metro_mapping
WHERE city ILIKE $1 AND state_code = $2;

-- name: GetMetroForCity :one
SELECT m.* FROM metro_timeseries m
JOIN city_metro_mapping c ON m.region_id = c.metro_region_id
WHERE c.city ILIKE $1 AND c.state_code = $2
ORDER BY m.date DESC
LIMIT 1;

-- name: ListAvailableRegions :many
SELECT DISTINCT region_id, region_name, region_type, state_code
FROM metro_timeseries
WHERE region_type = $1
ORDER BY region_name
LIMIT $2 OFFSET $3;

-- name: GetYearOverYearChange :one
WITH latest AS (
    SELECT t.zhvi, t.zori, t.date FROM metro_timeseries t
    WHERE t.region_id = $1
    ORDER BY t.date DESC
    LIMIT 1
),
year_ago AS (
    SELECT sub.zhvi, sub.zori FROM metro_timeseries sub
    WHERE sub.region_id = $1
    AND sub.date <= (SELECT latest.date - INTERVAL '1 year' FROM latest)
    ORDER BY sub.date DESC
    LIMIT 1
)
SELECT
    l.zhvi AS current_zhvi,
    l.zori AS current_zori,
    l.date AS latest_date,
    CASE WHEN y.zhvi > 0 THEN ((l.zhvi - y.zhvi) / y.zhvi * 100) ELSE 0 END AS zhvi_yoy_pct,
    CASE WHEN y.zori > 0 THEN ((l.zori - y.zori) / y.zori * 100) ELSE 0 END AS zori_yoy_pct
FROM latest l, year_ago y;
