package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"k8s.io/utils/clock"

	"github.com/luno/workflow"
)

// fallbackPoll bounds how long a receiver sleeps when no sender wakes it.
const fallbackPoll = time.Second

// Streamer is a workflow.EventStreamer on SQLite. Senders in this process wake
// receivers at once, and a slow poll covers anything else.
type Streamer struct {
	db    *sql.DB
	clock clock.Clock

	mu   sync.Mutex
	wake chan struct{}
}

var _ workflow.EventStreamer = (*Streamer)(nil)

// Streamer returns the event streamer. A nil clock means the real one.
func (d *DB) Streamer(clk clock.Clock) *Streamer {
	if clk == nil {
		clk = clock.RealClock{}
	}
	return &Streamer{db: d.sql, clock: clk, wake: make(chan struct{})}
}

func (s *Streamer) waitCh() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.wake
}

func (s *Streamer) broadcast() {
	s.mu.Lock()
	defer s.mu.Unlock()
	close(s.wake)
	s.wake = make(chan struct{})
}

func (s *Streamer) NewSender(_ context.Context, topic string) (workflow.EventSender, error) {
	return &sender{s: s, topic: topic}, nil
}

func (s *Streamer) NewReceiver(ctx context.Context, topic, name string, opts ...workflow.ReceiverOption) (workflow.EventReceiver, error) {
	var o workflow.ReceiverOptions
	for _, opt := range opts {
		opt(&o)
	}
	if o.StreamFromLatest {
		// Only when this receiver has never acked. An existing cursor wins.
		_, err := s.db.ExecContext(ctx, `
INSERT OR IGNORE INTO stream_cursors(name, last_id)
VALUES(?, (SELECT COALESCE(MAX(id),0) FROM stream_events))`, name)
		if err != nil {
			return nil, err
		}
	}
	poll := o.PollFrequency
	if poll <= 0 {
		poll = fallbackPoll
	}
	return &receiver{s: s, topic: topic, name: name, poll: poll}, nil
}

type sender struct {
	s     *Streamer
	topic string
}

func (x *sender) Send(ctx context.Context, foreignID string, statusType int, headers map[workflow.Header]string) error {
	topic := headers[workflow.HeaderTopic]
	if topic == "" {
		topic = x.topic
	}
	h, err := json.Marshal(headers)
	if err != nil {
		return err
	}
	if _, err := x.s.db.ExecContext(ctx,
		"INSERT INTO stream_events(topic, foreign_id, type, headers, created_at) VALUES(?,?,?,?,?)",
		topic, foreignID, statusType, string(h), tn(x.s.clock.Now())); err != nil {
		return err
	}
	x.s.broadcast()
	return nil
}

func (x *sender) Close() error { return nil }

type receiver struct {
	s     *Streamer
	topic string
	name  string
	poll  time.Duration
}

func (r *receiver) Recv(ctx context.Context) (*workflow.Event, workflow.Ack, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		// Take the wake channel before reading, so a send in between is not missed.
		wake := r.s.waitCh()
		ev, err := r.next(ctx)
		if err != nil {
			return nil, nil, err
		}
		if ev != nil {
			id := ev.ID
			return ev, func() error { return r.ack(id) }, nil
		}
		t := time.NewTimer(r.poll)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, nil, ctx.Err()
		case <-wake:
			t.Stop()
		case <-t.C:
		}
	}
}

func (r *receiver) next(ctx context.Context) (*workflow.Event, error) {
	var (
		cursor  int64
		ev      workflow.Event
		headers string
		at      int64
	)
	err := r.s.db.QueryRowContext(ctx, "SELECT last_id FROM stream_cursors WHERE name = ?", r.name).Scan(&cursor)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	err = r.s.db.QueryRowContext(ctx,
		"SELECT id, foreign_id, type, headers, created_at FROM stream_events WHERE topic = ? AND id > ? ORDER BY id LIMIT 1",
		r.topic, cursor).Scan(&ev.ID, &ev.ForeignID, &ev.Type, &headers, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(headers), &ev.Headers); err != nil {
		return nil, err
	}
	ev.CreatedAt = ft(at)
	return &ev, nil
}

func (r *receiver) ack(id int64) error {
	_, err := r.s.db.Exec(`
INSERT INTO stream_cursors(name, last_id) VALUES(?, ?)
ON CONFLICT(name) DO UPDATE SET last_id = excluded.last_id`, r.name, id)
	return err
}

func (r *receiver) Close() error { return nil }
