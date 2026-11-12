package store

import (
	"database/sql"

	"github.com/kostiantyn-matsebora/barekube/console/internal/events"
)

// EventSink persists the typed event log.
type EventSink struct{ db *sql.DB }

var _ events.Sink = (*EventSink)(nil)

// Events returns the event log sink.
func (d *DB) Events() *EventSink { return &EventSink{db: d.sql} }

// Save writes one event.
func (s *EventSink) Save(e events.Event) error {
	_, err := s.db.Exec(
		"INSERT OR REPLACE INTO events(id, at, kind, source, node, run_id, workflow, detail) VALUES(?,?,?,?,?,?,?,?)",
		e.ID, tn(e.Time), e.Kind, e.Source, e.Node, e.RunID, e.Workflow, e.Detail)
	return err
}

// Load returns the newest max events, oldest first.
func (s *EventSink) Load(max int) ([]events.Event, error) {
	rows, err := s.db.Query(
		"SELECT id, at, kind, source, node, run_id, workflow, detail FROM (SELECT * FROM events ORDER BY id DESC LIMIT ?) ORDER BY id", max)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []events.Event{}
	for rows.Next() {
		var (
			e  events.Event
			at int64
		)
		if err := rows.Scan(&e.ID, &at, &e.Kind, &e.Source, &e.Node, &e.RunID, &e.Workflow, &e.Detail); err != nil {
			return nil, err
		}
		e.Time = ft(at)
		out = append(out, e)
	}
	return out, rows.Err()
}
