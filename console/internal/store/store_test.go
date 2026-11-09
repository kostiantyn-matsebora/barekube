package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/luno/workflow"
	"k8s.io/utils/clock"
	clocktesting "k8s.io/utils/clock/testing"
)

var t0 = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

func open(t *testing.T) (*DB, *clocktesting.FakeClock) {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d, clocktesting.NewFakeClock(t0)
}

func rec(run, foreign string, state workflow.RunState, status int, at time.Time) *workflow.Record {
	return &workflow.Record{
		WorkflowName: "wf", ForeignID: foreign, RunID: run, RunState: state, Status: status,
		Object: []byte(`{"node":"` + foreign + `"}`), CreatedAt: at, UpdatedAt: at,
		Meta: workflow.Meta{RunStateReason: "why", StatusDescription: "desc", Version: 3},
	}
}

func TestOpenFailsOnBadPath(t *testing.T) {
	t.Parallel()
	if _, err := Open("/nonexistent-dir/x/console.db"); err == nil {
		t.Fatal("want an error for an unwritable path")
	}
}

func TestOpenKeepsDataAcrossReopen(t *testing.T) {
	t.Parallel()
	p := filepath.Join(t.TempDir(), "c.db")
	d, _ := Open(p)
	ctx := context.Background()
	if err := d.Records(nil).Store(ctx, rec("r1", "a", workflow.RunStateRunning, 1, t0)); err != nil {
		t.Fatal(err)
	}
	_ = d.Close()
	d2, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer d2.Close()
	got, err := d2.Records(nil).Lookup(ctx, "r1")
	if err != nil || got.ForeignID != "a" {
		t.Fatalf("lost across reopen: %v %v", got, err)
	}
	if d2.SQL() == nil {
		t.Fatal("SQL handle")
	}
}

func TestRecordsStoreLookupLatest(t *testing.T) {
	t.Parallel()
	d, clk := open(t)
	s := d.Records(clk)
	ctx := context.Background()
	if _, err := s.Lookup(ctx, "none"); err != workflow.ErrRecordNotFound {
		t.Fatalf("lookup missing: %v", err)
	}
	if _, err := s.Latest(ctx, "wf", "none"); err != workflow.ErrRecordNotFound {
		t.Fatalf("latest missing: %v", err)
	}
	_ = s.Store(ctx, rec("r1", "a", workflow.RunStateRunning, 1, t0))
	_ = s.Store(ctx, rec("r2", "a", workflow.RunStateRunning, 1, t0.Add(time.Second)))
	// r1 is updated after r2 was created, so it is the latest stored.
	_ = s.Store(ctx, rec("r1", "a", workflow.RunStateRunning, 2, t0.Add(2*time.Second)))
	got, err := s.Latest(ctx, "wf", "a")
	if err != nil || got.RunID != "r1" || got.Status != 2 {
		t.Fatalf("latest = %+v %v", got, err)
	}
	l, _ := s.Lookup(ctx, "r1")
	if l.Meta.Version != 3 || l.Meta.RunStateReason != "why" || string(l.Object) != `{"node":"a"}` || !l.UpdatedAt.Equal(t0.Add(2*time.Second)) {
		t.Fatalf("round trip: %+v", l)
	}
}

func TestRecordsListOrderOffsetFilters(t *testing.T) {
	t.Parallel()
	d, clk := open(t)
	s := d.Records(clk)
	ctx := context.Background()
	for i, c := range []struct {
		run, foreign string
		st           workflow.RunState
		status       int
	}{{"r1", "a", workflow.RunStateRunning, 1}, {"r2", "b", workflow.RunStateCompleted, 2}, {"r3", "a", workflow.RunStateCompleted, 2}} {
		_ = s.Store(ctx, rec(c.run, c.foreign, c.st, c.status, t0.Add(time.Duration(i)*time.Minute)))
	}
	other := rec("x1", "z", workflow.RunStateRunning, 1, t0)
	other.WorkflowName = "other"
	_ = s.Store(ctx, other)

	ids := func(rs []workflow.Record) (out []string) {
		for _, r := range rs {
			out = append(out, r.RunID)
		}
		return
	}
	eq := func(got []string, want ...string) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("got %v want %v", got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("got %v want %v", got, want)
			}
		}
	}
	all, _ := s.List(ctx, "wf", 0, 0, workflow.OrderTypeAscending)
	eq(ids(all), "r1", "r2", "r3")
	desc, _ := s.List(ctx, "wf", 0, 0, workflow.OrderTypeDescending)
	eq(ids(desc), "r3", "r2", "r1")
	after, _ := s.List(ctx, "wf", 1, 0, workflow.OrderTypeAscending)
	eq(ids(after), "r2", "r3")
	before, _ := s.List(ctx, "wf", 3, 0, workflow.OrderTypeDescending)
	eq(ids(before), "r2", "r1")
	lim, _ := s.List(ctx, "wf", 0, 2, workflow.OrderTypeAscending)
	eq(ids(lim), "r1", "r2")
	every, _ := s.List(ctx, "", 0, 10, workflow.OrderTypeAscending)
	eq(ids(every), "r1", "r2", "r3", "x1")
	fa, _ := s.List(ctx, "wf", 0, 10, workflow.OrderTypeAscending, workflow.FilterByForeignID("a"))
	eq(ids(fa), "r1", "r3")
	fs, _ := s.List(ctx, "wf", 0, 10, workflow.OrderTypeAscending, workflow.FilterByStatus(2))
	eq(ids(fs), "r2", "r3")
	fr, _ := s.List(ctx, "wf", 0, 10, workflow.OrderTypeAscending, workflow.FilterByRunState(workflow.RunStateRunning))
	eq(ids(fr), "r1")
	fb, _ := s.List(ctx, "wf", 0, 10, workflow.OrderTypeAscending, workflow.FilterByCreatedAtBefore(t0.Add(30*time.Second)))
	eq(ids(fb), "r1")
	fz, _ := s.List(ctx, "wf", 0, 10, workflow.OrderTypeAscending, workflow.FilterByCreatedAtAfter(t0.Add(90*time.Second)))
	eq(ids(fz), "r3")
}

func TestOutbox(t *testing.T) {
	t.Parallel()
	d, clk := open(t)
	s := d.Records(clk)
	ctx := context.Background()
	_ = s.Store(ctx, rec("r1", "a", workflow.RunStateRunning, 1, t0))
	_ = s.Store(ctx, rec("r1", "a", workflow.RunStateRunning, 2, t0))
	ev, err := s.ListOutboxEvents(ctx, "wf", 10)
	if err != nil || len(ev) != 2 {
		t.Fatalf("outbox = %v %v", ev, err)
	}
	if !ev[0].CreatedAt.Equal(t0) || len(ev[0].Data) == 0 {
		t.Fatalf("outbox row: %+v", ev[0])
	}
	if one, _ := s.ListOutboxEvents(ctx, "wf", 1); len(one) != 1 {
		t.Fatal("limit")
	}
	if none, _ := s.ListOutboxEvents(ctx, "other", 10); len(none) != 0 {
		t.Fatal("workflow filter")
	}
	if err := s.DeleteOutboxEvent(ctx, ev[0].ID); err != nil {
		t.Fatal(err)
	}
	if left, _ := s.ListOutboxEvents(ctx, "wf", 10); len(left) != 1 {
		t.Fatalf("after delete: %v", left)
	}
}

func TestTimeouts(t *testing.T) {
	t.Parallel()
	d, clk := open(t)
	s := d.Timeouts(clk)
	ctx := context.Background()
	_ = s.Create(ctx, "wf", "a", "r1", 5, t0.Add(time.Minute))
	_ = s.Create(ctx, "wf", "b", "r2", 5, t0.Add(time.Hour))
	_ = s.Create(ctx, "wf", "c", "r3", 6, t0.Add(time.Minute))
	all, err := s.List(ctx, "wf")
	if err != nil || len(all) != 3 || all[0].ForeignID != "a" || !all[0].CreatedAt.Equal(t0) {
		t.Fatalf("list = %+v %v", all, err)
	}
	if v, _ := s.ListValid(ctx, "wf", 5, t0); len(v) != 0 {
		t.Fatal("nothing has expired yet")
	}
	v, _ := s.ListValid(ctx, "wf", 5, t0.Add(2*time.Minute))
	if len(v) != 1 || v[0].RunID != "r1" || v[0].Completed {
		t.Fatalf("valid = %+v", v)
	}
	_ = s.Complete(ctx, v[0].ID)
	if v, _ := s.ListValid(ctx, "wf", 5, t0.Add(2*time.Minute)); len(v) != 0 {
		t.Fatal("completed timeouts are not valid")
	}
	l, _ := s.List(ctx, "wf")
	if !l[0].Completed {
		t.Fatal("completed flag")
	}
	_ = s.Cancel(ctx, l[1].ID)
	if l, _ := s.List(ctx, "wf"); len(l) != 2 {
		t.Fatalf("after cancel: %d", len(l))
	}
	if err := s.Cancel(ctx, 999); err != nil {
		t.Fatalf("cancel of a missing id is a no-op: %v", err)
	}
}

func hdr(topic string) map[workflow.Header]string {
	return map[workflow.Header]string{workflow.HeaderTopic: topic}
}

func TestStreamSendRecvAckAndCursor(t *testing.T) {
	t.Parallel()
	d, clk := open(t)
	st := d.Streamer(clk)
	ctx := context.Background()
	snd, _ := st.NewSender(ctx, "t1")
	_ = snd.Send(ctx, "f1", 1, hdr("t1"))
	_ = snd.Send(ctx, "f2", 2, hdr("t2"))
	_ = snd.Send(ctx, "f3", 3, map[workflow.Header]string{}) // topic falls back to the sender's
	_ = snd.Close()

	rcv, _ := st.NewReceiver(ctx, "t1", "c1")
	e1, ack1, err := rcv.Recv(ctx)
	if err != nil || e1.ForeignID != "f1" || e1.Type != 1 || e1.Headers[workflow.HeaderTopic] != "t1" || !e1.CreatedAt.Equal(t0) {
		t.Fatalf("e1 = %+v %v", e1, err)
	}
	// Without an ack the same event comes again.
	again, _, _ := rcv.Recv(ctx)
	if again.ID != e1.ID {
		t.Fatal("unacked event must be redelivered")
	}
	_ = ack1()
	e3, ack3, _ := rcv.Recv(ctx)
	if e3.ForeignID != "f3" {
		t.Fatalf("e3 = %+v", e3)
	}
	_ = ack3()

	// The cursor persists: a new receiver with the same name resumes after it.
	rcv2, _ := st.NewReceiver(ctx, "t1", "c1", workflow.WithReceiverPollFrequency(5*time.Millisecond))
	cctx, cancel := context.WithTimeout(ctx, 60*time.Millisecond)
	defer cancel()
	if _, _, err := rcv2.Recv(cctx); err == nil {
		t.Fatal("nothing left for t1")
	}
	_ = rcv.Close()
}

func TestStreamRecvWakesOnSend(t *testing.T) {
	t.Parallel()
	d, clk := open(t)
	st := d.Streamer(clk)
	ctx := context.Background()
	rcv, _ := st.NewReceiver(ctx, "t", "c", workflow.WithReceiverPollFrequency(time.Hour))
	got := make(chan *workflow.Event, 1)
	go func() {
		e, _, _ := rcv.Recv(ctx)
		got <- e
	}()
	time.Sleep(30 * time.Millisecond)
	snd, _ := st.NewSender(ctx, "t")
	_ = snd.Send(ctx, "f", 7, hdr("t"))
	select {
	case e := <-got:
		if e == nil || e.Type != 7 {
			t.Fatalf("got %+v", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("receiver was not woken by the send")
	}
}

func TestStreamPollFallbackAndCancel(t *testing.T) {
	t.Parallel()
	d, clk := open(t)
	st := d.Streamer(clk)
	ctx := context.Background()
	rcv, _ := st.NewReceiver(ctx, "t", "c", workflow.WithReceiverPollFrequency(10*time.Millisecond))
	// Insert behind the streamer's back so only the poll can find it.
	_, _ = d.SQL().Exec("INSERT INTO stream_events(topic, foreign_id, type, headers, created_at) VALUES('t','f',1,'{}',0)")
	e, _, err := rcv.Recv(ctx)
	if err != nil || e.ForeignID != "f" {
		t.Fatalf("poll: %+v %v", e, err)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := rcv.Recv(cctx); err == nil {
		t.Fatal("a cancelled context must end Recv")
	}
}

func TestStreamFromLatest(t *testing.T) {
	t.Parallel()
	d, clk := open(t)
	st := d.Streamer(clk)
	ctx := context.Background()
	snd, _ := st.NewSender(ctx, "t")
	_ = snd.Send(ctx, "old", 1, hdr("t"))
	rcv, _ := st.NewReceiver(ctx, "t", "late", workflow.StreamFromLatest(), workflow.WithReceiverPollFrequency(5*time.Millisecond))
	_ = snd.Send(ctx, "new", 2, hdr("t"))
	e, _, _ := rcv.Recv(ctx)
	if e.ForeignID != "new" {
		t.Fatalf("StreamFromLatest delivered %q", e.ForeignID)
	}
	// An existing cursor wins over StreamFromLatest.
	_, _ = d.SQL().Exec("INSERT INTO stream_cursors(name,last_id) VALUES('resume',0)")
	r2, _ := st.NewReceiver(ctx, "t", "resume", workflow.StreamFromLatest())
	first, _, _ := r2.Recv(ctx)
	if first.ForeignID != "old" {
		t.Fatalf("resume delivered %q", first.ForeignID)
	}
}

func count(t *testing.T, d *DB, table string) int {
	t.Helper()
	var n int
	if err := d.SQL().QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPurgeDeletesOldFinishedAndKeepsLive(t *testing.T) {
	t.Parallel()
	d, clk := open(t)
	ctx := context.Background()
	rs, ts, st := d.Records(clk), d.Timeouts(clk), d.Streamer(clk)
	old := t0.Add(-48 * time.Hour)
	_ = rs.Store(ctx, rec("done-old", "a", workflow.RunStateCompleted, 9, old))
	_ = rs.Store(ctx, rec("cancel-old", "a", workflow.RunStateCancelled, 9, old))
	_ = rs.Store(ctx, rec("live-old", "b", workflow.RunStateRunning, 3, old))
	_ = rs.Store(ctx, rec("paused-old", "b", workflow.RunStatePaused, 3, old))
	_ = rs.Store(ctx, rec("done-new", "c", workflow.RunStateCompleted, 9, t0))

	clk.SetTime(old)
	_ = ts.Create(ctx, "wf", "a", "r", 1, old.Add(time.Minute)) // expired and old
	_ = ts.Create(ctx, "wf", "b", "r", 1, old.Add(time.Minute))
	_ = ts.Complete(ctx, 2)
	clk.SetTime(t0)
	_ = ts.Create(ctx, "wf", "c", "r", 1, t0.Add(time.Hour)) // fresh
	clk.SetTime(old)
	_ = ts.Create(ctx, "wf", "d", "r", 1, t0.Add(time.Hour)) // old but still pending
	snd, _ := st.NewSender(ctx, "t")
	_ = snd.Send(ctx, "old-ev", 1, hdr("t"))
	clk.SetTime(t0)
	_ = snd.Send(ctx, "new-ev", 1, hdr("t"))

	p, err := d.Purge(ctx, t0.Add(-DefaultRetention))
	if err != nil {
		t.Fatal(err)
	}
	if p.Records != 2 || p.Events != 1 || p.Timeouts != 2 || p.Outbox != 0 {
		t.Fatalf("purged = %+v", p)
	}
	if n := count(t, d, "records"); n != 3 {
		t.Fatalf("records left = %d (live and fresh must stay)", n)
	}
	for _, id := range []string{"live-old", "paused-old", "done-new"} {
		if _, err := rs.Lookup(ctx, id); err != nil {
			t.Fatalf("%s was purged: %v", id, err)
		}
	}
	if n := count(t, d, "timeouts"); n != 2 {
		t.Fatalf("timeouts left = %d", n)
	}
	if n := count(t, d, "stream_events"); n != 1 {
		t.Fatalf("events left = %d", n)
	}
}

func TestPurgeOnClosedDBFails(t *testing.T) {
	t.Parallel()
	d, _ := open(t)
	_ = d.Close()
	if _, err := d.Purge(context.Background(), t0); err == nil {
		t.Fatal("want an error")
	}
}

func TestHousekeepRunsAtStartAndOnTick(t *testing.T) {
	t.Parallel()
	d, _ := open(t)
	clk := clocktesting.NewFakeClock(t0)
	ctx, cancel := context.WithCancel(context.Background())
	reports := make(chan Purged, 4)
	done := make(chan struct{})
	go func() {
		d.Housekeep(ctx, clk, DefaultRetention, time.Hour, func(p Purged, err error) {
			if err != nil {
				t.Error(err)
			}
			reports <- p
		})
		close(done)
	}()
	<-reports // the pass at start
	for !clk.HasWaiters() {
		time.Sleep(time.Millisecond)
	}
	clk.Step(time.Hour)
	<-reports // the pass on the tick
	cancel()
	<-done
}

func TestHousekeepWithoutReporter(t *testing.T) {
	t.Parallel()
	d, _ := open(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d.Housekeep(ctx, clocktesting.NewFakeClock(t0), time.Hour, time.Hour, nil)
}

var _ clock.Clock = (*clocktesting.FakeClock)(nil)

func TestPurgeDropsStaleOutbox(t *testing.T) {
	t.Parallel()
	d, clk := open(t)
	ctx := context.Background()
	clk.SetTime(t0.Add(-48 * time.Hour))
	_ = d.Records(clk).Store(ctx, rec("r", "a", workflow.RunStateRunning, 1, t0))
	clk.SetTime(t0)
	_ = d.Records(clk).Store(ctx, rec("r", "a", workflow.RunStateRunning, 2, t0))
	p, err := d.Purge(ctx, t0.Add(-DefaultRetention))
	if err != nil || p.Outbox != 1 {
		t.Fatalf("purged = %+v %v", p, err)
	}
	if n := count(t, d, "outbox"); n != 1 {
		t.Fatalf("fresh outbox row must stay, left %d", n)
	}
}
