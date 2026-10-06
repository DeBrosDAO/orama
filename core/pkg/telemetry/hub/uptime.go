package hub

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
)

// Uptime is recorded as minutes per component per UTC hour: one row per
// component per hour, whatever the traffic, so ninety days of history is a
// couple of thousand rows per component and one write a minute for the whole
// cluster.

// HistoryDays is how much history the status page shows and the table keeps.
const HistoryDays = 90

// hourLayout keys a row by its UTC hour.
const hourLayout = "2006-01-02T15"

// UptimeStore reads and writes status_uptime_hourly in the cluster registry.
type UptimeStore struct {
	DB *sql.DB
}

// Record adds one minute to each component's current hour, counted under its
// state. An unknown state is not recorded: nothing was observed.
func (u UptimeStore) Record(ctx context.Context, now time.Time, comps []cluster.Component) error {
	hour := now.UTC().Format(hourLayout)
	var values []string
	var args []any
	for _, c := range comps {
		op, deg, out := stateMinutes(c.State)
		if op+deg+out == 0 {
			continue
		}
		values = append(values, "(?, ?, ?, ?, ?)")
		args = append(args, c.ID, hour, op, deg, out)
	}
	if len(values) == 0 {
		return nil
	}
	// One statement, so one raft entry per minute however many components.
	stmt := `INSERT INTO status_uptime_hourly
	           (component, hour, operational_minutes, degraded_minutes, outage_minutes)
	         VALUES ` + strings.Join(values, ", ") + `
	         ON CONFLICT(component, hour) DO UPDATE SET
	           operational_minutes = operational_minutes + excluded.operational_minutes,
	           degraded_minutes    = degraded_minutes + excluded.degraded_minutes,
	           outage_minutes      = outage_minutes + excluded.outage_minutes`
	if _, err := rqlite.SafeExecContext(u.DB, ctx, stmt, args...); err != nil {
		return fmt.Errorf("record service uptime in status_uptime_hourly: %w", err)
	}
	return nil
}

func stateMinutes(s cluster.State) (op, deg, out int) {
	switch s {
	case cluster.StateOperational:
		return 1, 0, 0
	case cluster.StateDegraded:
		return 0, 1, 0
	case cluster.StateOutage:
		return 0, 0, 1
	default:
		return 0, 0, 0
	}
}

// Prune deletes hours older than the history window.
func (u UptimeStore) Prune(ctx context.Context, now time.Time) error {
	cutoff := now.UTC().AddDate(0, 0, -HistoryDays).Format(hourLayout)
	if _, err := rqlite.SafeExecContext(u.DB, ctx, `DELETE FROM status_uptime_hourly WHERE hour < ?`, cutoff); err != nil {
		return fmt.Errorf("prune status_uptime_hourly: %w", err)
	}
	return nil
}

// History returns each component's daily uptime over the window, oldest day
// first. A degraded minute counts as up: the service was serving.
func (u UptimeStore) History(ctx context.Context, now time.Time) (cluster.UptimeHistory, error) {
	cutoff := now.UTC().AddDate(0, 0, -HistoryDays+1).Format("2006-01-02")
	rows, err := rqlite.SafeQueryContext(u.DB, ctx, `
		SELECT component, substr(hour, 1, 10) AS day,
		       SUM(operational_minutes), SUM(degraded_minutes), SUM(outage_minutes)
		  FROM status_uptime_hourly
		 WHERE hour >= ?
		 GROUP BY component, day
		 ORDER BY day`, cutoff)
	if err != nil {
		return nil, fmt.Errorf("read service uptime history: %w", err)
	}
	defer rows.Close()
	history := cluster.UptimeHistory{}
	for rows.Next() {
		var comp, day string
		var op, deg, out int64
		if err := rows.Scan(&comp, &day, &op, &deg, &out); err != nil {
			return nil, fmt.Errorf("read a status_uptime_hourly row: %w", err)
		}
		total := op + deg + out
		if total == 0 {
			continue
		}
		pct := float64(op+deg) * 100 / float64(total)
		history[comp] = append(history[comp], cluster.DayUptime{Date: day, UptimePct: pct, Minutes: total})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read service uptime history: %w", err)
	}
	return history, nil
}
