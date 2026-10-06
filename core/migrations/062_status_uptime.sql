-- Migration 062: service uptime history for the public status page.
--
-- One row per component (gateway, database, cache, ...) per UTC hour, holding
-- how many sampled minutes the component was operational, degraded or out.
-- The cluster gateway on the lowest-id active node adds one minute to each
-- component's current hour every minute (pkg/telemetry/hub), in a single
-- statement, and prunes rows older than the 90-day window once an hour.
-- At that shape the table stays near 2,200 rows per component.

CREATE TABLE IF NOT EXISTS status_uptime_hourly (
    component           TEXT    NOT NULL,
    hour                TEXT    NOT NULL, -- UTC, 'YYYY-MM-DDTHH'
    operational_minutes INTEGER NOT NULL DEFAULT 0,
    degraded_minutes    INTEGER NOT NULL DEFAULT 0,
    outage_minutes      INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (component, hour)
);

CREATE INDEX IF NOT EXISTS idx_status_uptime_hourly_hour ON status_uptime_hourly(hour);
