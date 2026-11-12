// Package store keeps the console's workflow state in one SQLite file instead
// of in memory. The library's in-memory adapters append every record version
// and every event for the life of the process. On a router with 1 GiB of RAM
// that is a leak, so state lives on disk and a housekeeper deletes what is
// older than the retention window.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"k8s.io/utils/clock"
	_ "modernc.org/sqlite" // pure Go driver, so the arm64 build needs no CGO
)

// DefaultRetention is how long finished runs, events and timeouts are kept.
const DefaultRetention = 24 * time.Hour

// Run states after which a record is finished. Mirrors workflow.RunState
// Cancelled, Completed and DataDeleted. A record in any other state is live
// and is never purged.
const finishedStates = "4,5,6"

const schema = `
CREATE TABLE IF NOT EXISTS records (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  run_id     TEXT NOT NULL UNIQUE,
  workflow   TEXT NOT NULL,
  foreign_id TEXT NOT NULL,
  run_state  INTEGER NOT NULL,
  status     INTEGER NOT NULL,
  object     BLOB,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  meta       TEXT NOT NULL,
  seq        INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS records_latest ON records(workflow, foreign_id, seq);
CREATE INDEX IF NOT EXISTS records_purge ON records(run_state, updated_at);
CREATE TABLE IF NOT EXISTS outbox (
  seq        INTEGER PRIMARY KEY AUTOINCREMENT,
  id         TEXT NOT NULL UNIQUE,
  workflow   TEXT NOT NULL,
  data       BLOB NOT NULL,
  created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS timeouts (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  workflow   TEXT NOT NULL,
  foreign_id TEXT NOT NULL,
  run_id     TEXT NOT NULL,
  status     INTEGER NOT NULL,
  completed  INTEGER NOT NULL DEFAULT 0,
  expire_at  INTEGER NOT NULL,
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS timeouts_valid ON timeouts(workflow, status, completed, expire_at);
CREATE TABLE IF NOT EXISTS stream_events (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  topic      TEXT NOT NULL,
  foreign_id TEXT NOT NULL,
  type       INTEGER NOT NULL,
  headers    TEXT NOT NULL,
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS stream_topic ON stream_events(topic, id);
CREATE TABLE IF NOT EXISTS events (
  id       INTEGER PRIMARY KEY,
  at       INTEGER NOT NULL,
  kind     TEXT NOT NULL,
  source   TEXT NOT NULL,
  node     TEXT NOT NULL,
  run_id   TEXT NOT NULL,
  workflow TEXT NOT NULL,
  detail   TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS events_at ON events(at);
CREATE TABLE IF NOT EXISTS node_observations (
  node      TEXT PRIMARY KEY,
  at        INTEGER NOT NULL,
  reachable INTEGER NOT NULL,
  k3s       INTEGER NOT NULL,
  ready     INTEGER NOT NULL,
  cordoned  INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS stream_cursors (
  name    TEXT PRIMARY KEY,
  last_id INTEGER NOT NULL
);
`

// DB is the shared SQLite handle.
type DB struct {
	sql *sql.DB
}

// Open creates or opens the file and applies the schema. The pragmas keep
// memory small: a 2 MiB page cache and no memory mapping.
func Open(path string) (*DB, error) {
	dsn := "file:" + path +
		"?_pragma=busy_timeout(5000)&_pragma=auto_vacuum(2)&_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(NORMAL)&_pragma=cache_size(-2048)&_pragma=mmap_size(0)"
	h, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	// One connection: every statement is short and this removes lock contention.
	h.SetMaxOpenConns(1)
	if _, err := h.Exec(schema); err != nil {
		_ = h.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &DB{sql: h}, nil
}

// SQL exposes the handle to the other packages that persist into the same file.
func (d *DB) SQL() *sql.DB { return d.sql }

// Close closes the file.
func (d *DB) Close() error { return d.sql.Close() }

// Purged counts the rows one housekeeping pass deleted.
type Purged struct {
	Records, Outbox, Timeouts, Events, Log int64
}

// Purge deletes what is older than before. A live record is never deleted.
func (d *DB) Purge(ctx context.Context, before time.Time) (Purged, error) {
	var p Purged
	cut := tn(before)
	tx, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return p, err
	}
	defer func() { _ = tx.Rollback() }()
	steps := []struct {
		dst *int64
		sql string
	}{
		{&p.Records, "DELETE FROM records WHERE run_state IN (" + finishedStates + ") AND updated_at < ?"},
		{&p.Timeouts, "DELETE FROM timeouts WHERE created_at < ? AND (completed = 1 OR expire_at < ?)"},
		{&p.Events, "DELETE FROM stream_events WHERE created_at < ?"},
		{&p.Outbox, "DELETE FROM outbox WHERE created_at < ?"},
		{&p.Log, "DELETE FROM events WHERE at < ?"},
	}
	for _, s := range steps {
		var res sql.Result
		if s.dst == &p.Timeouts {
			res, err = tx.ExecContext(ctx, s.sql, cut, cut)
		} else {
			res, err = tx.ExecContext(ctx, s.sql, cut)
		}
		if err != nil {
			return p, err
		}
		*s.dst, _ = res.RowsAffected()
	}
	if err := tx.Commit(); err != nil {
		return p, err
	}
	// Hand the freed pages back so the file shrinks.
	_, err = d.sql.ExecContext(ctx, "PRAGMA incremental_vacuum")
	return p, err
}

// Housekeep purges once now and then every interval until ctx ends.
func (d *DB) Housekeep(ctx context.Context, clk clock.WithTicker, retention, interval time.Duration, report func(Purged, error)) {
	run := func() {
		p, err := d.Purge(ctx, clk.Now().Add(-retention))
		if report != nil {
			report(p, err)
		}
	}
	run()
	t := clk.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C():
			run()
		}
	}
}

func tn(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixNano()
}

func ft(n int64) time.Time {
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n).UTC()
}

// AbandonLive is the restart rule. A run that was live when the process died
// must not be resumed, because the library would repeat its actions and the
// console would not know it was running. Every unfinished record becomes
// Cancelled with the reason, pending outbox events and timeouts are dropped,
// and every cursor jumps to the newest event so nothing is replayed.
// It returns the run ids it cancelled.
func (d *DB) AbandonLive(ctx context.Context, reason string) ([]string, error) {
	tx, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, "SELECT run_id FROM records WHERE run_state NOT IN ("+finishedStates+")")
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	stmts := []string{
		"UPDATE records SET run_state = 4, meta = json_set(meta, '$.RunStateReason', ?) WHERE run_state NOT IN (" + finishedStates + ")",
	}
	if _, err := tx.ExecContext(ctx, stmts[0], reason); err != nil {
		return nil, err
	}
	for _, q := range []string{
		"DELETE FROM outbox",
		"DELETE FROM timeouts WHERE completed = 0",
		"UPDATE stream_cursors SET last_id = (SELECT COALESCE(MAX(id),0) FROM stream_events)",
	} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return nil, err
		}
	}
	return ids, tx.Commit()
}
