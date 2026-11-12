package store

import (
	"context"
	"database/sql"
	"time"

	"k8s.io/utils/clock"

	"github.com/luno/workflow"
)

// Timeouts is a workflow.TimeoutStore on SQLite.
type Timeouts struct {
	db    *sql.DB
	clock clock.Clock
}

var _ workflow.TimeoutStore = (*Timeouts)(nil)

// Timeouts returns the timeout store. A nil clock means the real one.
func (d *DB) Timeouts(clk clock.Clock) *Timeouts {
	if clk == nil {
		clk = clock.RealClock{}
	}
	return &Timeouts{db: d.sql, clock: clk}
}

func (s *Timeouts) Create(ctx context.Context, workflowName, foreignID, runID string, status int, expireAt time.Time) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO timeouts(workflow, foreign_id, run_id, status, expire_at, created_at) VALUES(?,?,?,?,?,?)",
		workflowName, foreignID, runID, status, tn(expireAt), tn(s.clock.Now()))
	return err
}

func (s *Timeouts) Complete(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, "UPDATE timeouts SET completed = 1 WHERE id = ?", id)
	return err
}

func (s *Timeouts) Cancel(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM timeouts WHERE id = ?", id)
	return err
}

const timeoutCols = "id, workflow, foreign_id, run_id, status, completed, expire_at, created_at"

func (s *Timeouts) query(ctx context.Context, q string, args ...any) ([]workflow.TimeoutRecord, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []workflow.TimeoutRecord
	for rows.Next() {
		var (
			t               workflow.TimeoutRecord
			done            int
			expire, created int64
		)
		if err := rows.Scan(&t.ID, &t.WorkflowName, &t.ForeignID, &t.RunID, &t.Status, &done, &expire, &created); err != nil {
			return nil, err
		}
		t.Completed = done == 1
		t.ExpireAt, t.CreatedAt = ft(expire), ft(created)
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Timeouts) List(ctx context.Context, workflowName string) ([]workflow.TimeoutRecord, error) {
	return s.query(ctx, "SELECT "+timeoutCols+" FROM timeouts WHERE workflow = ? ORDER BY id", workflowName)
}

func (s *Timeouts) ListValid(ctx context.Context, workflowName string, status int, now time.Time) ([]workflow.TimeoutRecord, error) {
	return s.query(ctx, "SELECT "+timeoutCols+" FROM timeouts WHERE workflow = ? AND status = ? AND completed = 0 AND expire_at <= ? ORDER BY id",
		workflowName, status, tn(now))
}
