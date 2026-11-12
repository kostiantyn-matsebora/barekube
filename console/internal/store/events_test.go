package store

import (
	"context"
	"testing"
	"time"

	"github.com/luno/workflow"

	"github.com/kostiantyn-matsebora/barekube/console/internal/events"
)

func TestEventSinkSaveLoadNewestFirstWindow(t *testing.T) {
	t.Parallel()
	d, _ := open(t)
	s := d.Events()
	for i := int64(1); i <= 5; i++ {
		e := events.Event{ID: i, Time: t0.Add(time.Duration(i) * time.Second), Kind: "k", Source: "s", Node: "n", RunID: "r", Workflow: "w", Detail: "d"}
		if err := s.Save(e); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Load(3)
	if err != nil || len(got) != 3 || got[0].ID != 3 || got[2].ID != 5 {
		t.Fatalf("want the newest 3, oldest first: %+v %v", got, err)
	}
	if got[0].Kind != "k" || got[0].Detail != "d" || !got[0].Time.Equal(t0.Add(3*time.Second)) {
		t.Fatalf("fields lost: %+v", got[0])
	}
	// Saving the same id again replaces it.
	_ = s.Save(events.Event{ID: 5, Time: t0, Kind: "again"})
	if g, _ := s.Load(1); g[0].Kind != "again" {
		t.Fatal("replace")
	}
	empty, _ := open2(t).Events().Load(10)
	if empty == nil || len(empty) != 0 {
		t.Fatalf("empty load must be an empty slice: %v", empty)
	}
}

func open2(t *testing.T) *DB { d, _ := open(t); return d }

func TestEventSinkErrorsOnClosedDB(t *testing.T) {
	t.Parallel()
	d, _ := open(t)
	s := d.Events()
	_ = d.Close()
	if err := s.Save(events.Event{ID: 1}); err == nil {
		t.Fatal("save")
	}
	if _, err := s.Load(1); err == nil {
		t.Fatal("load")
	}
}

func TestPurgeDeletesOldEvents(t *testing.T) {
	t.Parallel()
	d, _ := open(t)
	s := d.Events()
	_ = s.Save(events.Event{ID: 1, Time: t0.Add(-48 * time.Hour), Kind: "old"})
	_ = s.Save(events.Event{ID: 2, Time: t0, Kind: "new"})
	p, err := d.Purge(context.Background(), t0.Add(-DefaultRetention))
	if err != nil || p.Log != 1 {
		t.Fatalf("%+v %v", p, err)
	}
	if g, _ := s.Load(10); len(g) != 1 || g[0].Kind != "new" {
		t.Fatalf("%+v", g)
	}
}

func TestAbandonLiveCancelsLiveRunsAndDropsReplayState(t *testing.T) {
	t.Parallel()
	d, clk := open(t)
	ctx := context.Background()
	rs, ts, st := d.Records(clk), d.Timeouts(clk), d.Streamer(clk)
	_ = rs.Store(ctx, rec("live", "a", workflow.RunStateRunning, 3, t0))
	_ = rs.Store(ctx, rec("init", "b", workflow.RunStateInitiated, 1, t0))
	_ = rs.Store(ctx, rec("done", "c", workflow.RunStateCompleted, 9, t0))
	_ = ts.Create(ctx, "wf", "a", "live", 3, t0.Add(time.Minute))
	snd, _ := st.NewSender(ctx, "t")
	_ = snd.Send(ctx, "x", 1, hdr("t"))
	_ = snd.Send(ctx, "y", 1, hdr("t"))
	_, _ = d.SQL().Exec("INSERT INTO stream_cursors(name,last_id) VALUES('c',0)")

	ids, err := d.AbandonLive(ctx, "restarted")
	if err != nil || len(ids) != 2 {
		t.Fatalf("%v %v", ids, err)
	}
	live, _ := rs.Lookup(ctx, "live")
	if live.RunState != workflow.RunStateCancelled || live.Meta.RunStateReason != "restarted" || live.Meta.Version != 3 {
		t.Fatalf("%+v", live)
	}
	if done, _ := rs.Lookup(ctx, "done"); done.RunState != workflow.RunStateCompleted {
		t.Fatal("a finished run must be left alone")
	}
	if n := count(t, d, "outbox"); n != 0 {
		t.Fatalf("outbox left %d", n)
	}
	if n := count(t, d, "timeouts"); n != 0 {
		t.Fatalf("pending timeouts left %d", n)
	}
	rcv, _ := st.NewReceiver(ctx, "t", "c", workflow.WithReceiverPollFrequency(5*time.Millisecond))
	cctx, cancel := context.WithTimeout(ctx, 60*time.Millisecond)
	defer cancel()
	if _, _, err := rcv.Recv(cctx); err == nil {
		t.Fatal("old events must not be replayed after a restart")
	}
	// Nothing live is left, so a second call is a no-op.
	if again, _ := d.AbandonLive(ctx, "x"); len(again) != 0 {
		t.Fatalf("%v", again)
	}
}

func TestAbandonLiveOnClosedDBFails(t *testing.T) {
	t.Parallel()
	d, _ := open(t)
	_ = d.Close()
	if _, err := d.AbandonLive(context.Background(), "x"); err == nil {
		t.Fatal("want an error")
	}
}
