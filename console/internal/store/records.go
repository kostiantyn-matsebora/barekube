package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"k8s.io/utils/clock"

	"github.com/luno/workflow"
)

const defaultListLimit = 25

// Records is a workflow.RecordStore on SQLite.
type Records struct {
	db    *sql.DB
	clock clock.Clock
}

var _ workflow.RecordStore = (*Records)(nil)

// Records returns the record store. A nil clock means the real one.
func (d *DB) Records(clk clock.Clock) *Records {
	if clk == nil {
		clk = clock.RealClock{}
	}
	return &Records{db: d.sql, clock: clk}
}

const recordCols = "workflow, foreign_id, run_id, run_state, status, object, created_at, updated_at, meta"

type scanner interface{ Scan(dest ...any) error }

func scanRecord(sc scanner) (workflow.Record, error) {
	var (
		r         workflow.Record
		state     int
		created   int64
		updated   int64
		metaBytes string
	)
	if err := sc.Scan(&r.WorkflowName, &r.ForeignID, &r.RunID, &state, &r.Status, &r.Object, &created, &updated, &metaBytes); err != nil {
		return r, err
	}
	r.RunState = workflow.RunState(state)
	r.CreatedAt, r.UpdatedAt = ft(created), ft(updated)
	if err := json.Unmarshal([]byte(metaBytes), &r.Meta); err != nil {
		return r, err
	}
	return r, nil
}

// Store creates or updates a record and writes its outbox event in one
// transaction, as the RecordStore contract requires.
func (s *Records) Store(ctx context.Context, rec *workflow.Record) error {
	data, err := workflow.MakeOutboxEventData(*rec)
	if err != nil {
		return err
	}
	meta, err := json.Marshal(rec.Meta)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `
INSERT INTO records(run_id, workflow, foreign_id, run_state, status, object, created_at, updated_at, meta, seq)
VALUES(?,?,?,?,?,?,?,?,?, (SELECT COALESCE(MAX(seq),0)+1 FROM records))
ON CONFLICT(run_id) DO UPDATE SET workflow=excluded.workflow, foreign_id=excluded.foreign_id,
  run_state=excluded.run_state, status=excluded.status, object=excluded.object,
  created_at=excluded.created_at, updated_at=excluded.updated_at, meta=excluded.meta, seq=excluded.seq`,
		rec.RunID, rec.WorkflowName, rec.ForeignID, int(rec.RunState), rec.Status, rec.Object,
		tn(rec.CreatedAt), tn(rec.UpdatedAt), string(meta))
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO outbox(id, workflow, data, created_at) VALUES(?,?,?,?)`,
		data.ID, data.WorkflowName, data.Data, tn(s.clock.Now())); err != nil {
		return err
	}
	return tx.Commit()
}

// Lookup returns one record by run id.
func (s *Records) Lookup(ctx context.Context, runID string) (*workflow.Record, error) {
	r, err := scanRecord(s.db.QueryRowContext(ctx, "SELECT "+recordCols+" FROM records WHERE run_id = ?", runID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, workflow.ErrRecordNotFound
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// Latest returns the most recently stored record for a workflow and foreign id.
func (s *Records) Latest(ctx context.Context, workflowName, foreignID string) (*workflow.Record, error) {
	r, err := scanRecord(s.db.QueryRowContext(ctx,
		"SELECT "+recordCols+" FROM records WHERE workflow = ? AND foreign_id = ? ORDER BY seq DESC LIMIT 1",
		workflowName, foreignID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, workflow.ErrRecordNotFound
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// List pages records by insertion id. Ascending returns ids after the offset,
// descending returns ids before it, or the newest when the offset is zero.
func (s *Records) List(ctx context.Context, workflowName string, offsetID int64, limit int, order workflow.OrderType, filters ...workflow.RecordFilter) ([]workflow.Record, error) {
	if limit == 0 {
		limit = defaultListLimit
	}
	q := "SELECT " + recordCols + " FROM records WHERE 1=1"
	var args []any
	if workflowName != "" {
		q += " AND workflow = ?"
		args = append(args, workflowName)
	}
	desc := order == workflow.OrderTypeDescending
	switch {
	case desc && offsetID > 0:
		q += " AND id < ?"
		args = append(args, offsetID)
	case !desc:
		q += " AND id > ?"
		args = append(args, offsetID)
	}
	if desc {
		q += " ORDER BY id DESC"
	} else {
		q += " ORDER BY id ASC"
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	f := workflow.MakeFilter(filters...)
	var out []workflow.Record
	for rows.Next() && len(out) < limit {
		r, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		if !matches(f, r) {
			continue
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func matches(f interface {
	ByForeignID() workflow.Filter
	ByStatus() workflow.Filter
	ByRunState() workflow.Filter
	ByCreatedAtAfter() workflow.FilterTime
	ByCreatedAtBefore() workflow.FilterTime
}, r workflow.Record) bool {
	if x := f.ByForeignID(); x.Enabled && !x.Matches(r.ForeignID) {
		return false
	}
	if x := f.ByStatus(); x.Enabled && !x.Matches(itoa(r.Status)) {
		return false
	}
	if x := f.ByRunState(); x.Enabled && !x.Matches(itoa(int(r.RunState))) {
		return false
	}
	if x := f.ByCreatedAtAfter(); x.Enabled && !x.Matches(r.CreatedAt) {
		return false
	}
	if x := f.ByCreatedAtBefore(); x.Enabled && !x.Matches(r.CreatedAt) {
		return false
	}
	return true
}

// ListOutboxEvents returns events not yet published, oldest first.
func (s *Records) ListOutboxEvents(ctx context.Context, workflowName string, limit int64) ([]workflow.OutboxEvent, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, workflow, data, created_at FROM outbox WHERE workflow = ? ORDER BY seq LIMIT ?", workflowName, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []workflow.OutboxEvent
	for rows.Next() {
		var (
			e  workflow.OutboxEvent
			at int64
		)
		if err := rows.Scan(&e.ID, &e.WorkflowName, &e.Data, &at); err != nil {
			return nil, err
		}
		e.CreatedAt = ft(at)
		out = append(out, e)
	}
	return out, rows.Err()
}

// DeleteOutboxEvent removes an event once it is published.
func (s *Records) DeleteOutboxEvent(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM outbox WHERE id = ?", id)
	return err
}
