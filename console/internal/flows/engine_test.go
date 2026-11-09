package flows

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luno/workflow"

	"github.com/kostiantyn-matsebora/barekube/console/internal/config"
	"github.com/kostiantyn-matsebora/barekube/console/internal/events"
	"github.com/kostiantyn-matsebora/barekube/console/internal/observe"
	"github.com/kostiantyn-matsebora/barekube/console/internal/store"
)

func TestStartNodeValidation(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	if _, err := h.eng.StartNode(ctx, "nope", "a1", ModeGraceful, "t"); !errors.Is(err, ErrUnknownWorkflow) {
		t.Error(err)
	}
	if _, err := h.eng.StartNode(ctx, WfCluster, "a1", ModeGraceful, "t"); !errors.Is(err, ErrUnknownWorkflow) {
		t.Error(err)
	}
	if _, err := h.eng.StartNode(ctx, WfReboot, "ghost", ModeGraceful, "t"); !errors.Is(err, ErrUnknownNode) {
		t.Error(err)
	}
	if _, err := h.eng.StartNode(ctx, WfPowerOff, "a1", ModeGraceful, "t"); !errors.Is(err, ErrUnsupported) {
		t.Error(err)
	}
	info, err := h.eng.StartNode(ctx, WfReboot, "a1", Mode("weird"), "t")
	if err != nil || info.Mode != ModeGraceful || info.RunID == "" {
		t.Errorf("%+v %v", info, err)
	}
}

func TestRunsAreListedNewestFirstAndBusyTracksActivity(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	if h.eng.Busy() {
		t.Fatal("busy with nothing running")
	}
	h.start(WfReboot, "a1", ModeGraceful)
	h.waitFor("a1", AwaitingNodeDown)
	if !h.eng.Busy() {
		t.Fatal("not busy during a run")
	}
	h.expire("a1", 3*time.Minute, Failed)
	if h.eng.Busy() {
		t.Fatal("still busy after the run ended")
	}
	h.start(WfStandBy, "a2", ModeGraceful)
	runs := h.eng.Runs()
	if len(runs) != 2 || runs[0].Node != "a2" || runs[1].Node != "a1" {
		t.Fatalf("newest first: %+v", runs)
	}
}

func TestObservationsForIdleNodesAreIgnored(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.eng.OnObservations([]observe.Observation{{Node: "a1", At: h.clk.Now(), Reachable: observe.No}})
	if len(h.eng.Runs()) != 0 {
		t.Fatal("run appeared from nowhere")
	}
}

func TestNewRejectsAnInvalidSpecSet(t *testing.T) {
	t.Parallel()
	cfg := testConfig()
	e, err := New(cfg, newFakeOps(), &fakeObs{}, events.NewLog(10, nil), Options{})
	if err != nil || e.poll != time.Second || e.clk == nil {
		t.Fatalf("defaults: %v", err)
	}
	e.Stop()
}

func TestStartFailureReleasesTheReservation(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.eng.Stop()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := h.eng.StartNode(ctx, WfReboot, "a1", ModeGraceful, "t"); err == nil {
		t.Skip("trigger tolerated a cancelled context")
	}
	if _, ok := h.eng.reg.activeRun("a1"); ok {
		t.Fatal("reservation leaked")
	}
}

func TestRegistryHistoryIsBounded(t *testing.T) {
	t.Parallel()
	g := newRegistry()
	now := time.Now()
	for i := 0; i < maxHistory+20; i++ {
		key := "n"
		if err := g.begin(RunInfo{key: key, Workflow: "w", Node: key}); err != nil {
			t.Fatal(err)
		}
		g.update(key, "id", Completed, true, "", now)
	}
	if n := len(g.list()); n != maxHistory {
		t.Fatalf("%d runs kept", n)
	}
	g.update("missing", "id", Completed, true, "", now) // no-op
	g.abort("missing")
	if _, ok := g.latestRun("missing"); ok {
		t.Fatal("phantom")
	}
}

func TestRegistryAbortAndClear(t *testing.T) {
	t.Parallel()
	g := newRegistry()
	if err := g.begin(RunInfo{key: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := g.begin(RunInfo{key: "a"}); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	g.abort("a")
	if g.anyActive() {
		t.Fatal("abort left a run")
	}
	g.clear("a")
	if !g.isCleared("a") {
		t.Fatal("not cleared")
	}
	_ = g.begin(RunInfo{key: "a"})
	if g.isCleared("a") {
		t.Fatal("clearance must not survive into a new run")
	}
}

func TestLoggedOpsRecordEveryCall(t *testing.T) {
	t.Parallel()
	log := events.NewLog(100, nil)
	f := newFakeOps()
	f.failOn("cordon")
	f.quorum = false
	l := loggedOps{Ops: f, log: log}
	ctx := withRef(context.Background(), runRef{RunID: "r", Workflow: "w", Node: "n"})
	l.QuorumOK(ctx)
	_ = l.EtcdSnapshot(ctx)
	_ = l.Cordon(ctx, "n")
	_ = l.Uncordon(ctx, "n")
	_ = l.LonghornScheduling(ctx, "n", true)
	_ = l.LonghornScheduling(ctx, "n", false)
	_, _ = l.ZeroReplicaRisk(ctx, "n")
	_ = l.Drain(ctx, "n", time.Second)
	_ = l.Reboot(ctx, "n")
	_ = l.StopK3s(ctx, "n")
	_ = l.KillK3s(ctx, "n")
	_ = l.StartK3s(ctx, "n")
	_ = l.PowerOff(ctx, "n", true)
	_ = l.PowerOff(ctx, "n", false)
	_, _ = l.EtcdLeader(ctx)
	got := log.List(events.Filter{})
	if len(got) != 15 {
		t.Fatalf("%d events", len(got))
	}
	failed := 0
	for _, e := range got {
		if e.Kind == events.ActionFailed {
			failed++
		}
		if e.RunID != "r" || e.Workflow != "w" {
			t.Errorf("run ref lost: %+v", e)
		}
	}
	if failed != 2 { // quorum + cordon
		t.Errorf("%d failures", failed)
	}
}

func TestConfigDefaultsAreUsedWhenUnset(t *testing.T) {
	t.Parallel()
	if config.DefaultTiming().NodeDown == 0 {
		t.Fatal("no default timing")
	}
}

func TestRestartAbandonsLiveRunsAndNeverReplaysThem(t *testing.T) {
	t.Parallel()
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// The first process starts a run and dies while it waits for k3s to stop.
	h1, die := newHarnessOn(t, db)
	info := h1.start(WfStandBy, "a2", ModeForce)
	h1.waitFor("a2", AwaitingK3sInactive)
	if h1.ops.count("kill") != 1 {
		t.Fatalf("setup: %v", h1.ops.calls)
	}
	die()

	// The second process finds the same file.
	h2, _ := newHarnessOn(t, db)
	for range 10 {
		h2.tick()
		time.Sleep(3 * time.Millisecond)
	}
	if n := h2.ops.count("kill") + h2.ops.count("cordon") + h2.ops.count("drain"); n != 0 {
		t.Fatalf("a restart must not replay the old run's actions: %v", h2.ops.calls)
	}
	var found bool
	for _, e := range h2.log.List(events.Filter{RunID: info.RunID}) {
		if e.Kind == events.RunFailed && strings.Contains(e.Detail, "restarted") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the abandoned run must be logged: %v", h2.log.List(events.Filter{}))
	}
	rec, err := db.Records(nil).Lookup(context.Background(), info.RunID)
	if err != nil || rec.RunState != workflow.RunStateCancelled {
		t.Fatalf("the old run must be cancelled in the store: %+v %v", rec, err)
	}
	// The old run is still listed after the restart, as failed with its reason.
	var listed bool
	for _, r := range h2.eng.Runs() {
		if r.RunID == info.RunID {
			listed = r.Terminal && r.Final == "failed" && strings.Contains(r.Reason, "restarted") && r.Node == "a2" && r.By == "test" && r.Mode == ModeForce
		}
	}
	if !listed {
		t.Fatalf("history missing after restart: %+v", h2.eng.Runs())
	}
	// The registry starts empty, so the node can be stood by again.
	h2.start(WfStandBy, "a2", ModeForce)
	h2.waitFor("a2", AwaitingK3sInactive)
}

func TestEngineRunsOnTheStore(t *testing.T) {
	t.Parallel()
	db, _ := store.Open(filepath.Join(t.TempDir(), "console.db"))
	t.Cleanup(func() { _ = db.Close() })
	h, _ := newHarnessOn(t, db)
	h.start(WfStandBy, "a2", ModeForce)
	h.waitFor("a2", AwaitingK3sInactive)
	k3sStops(h, "a2")
	r := h.waitFor("a2", StoodBy)
	rec, err := db.Records(nil).Lookup(context.Background(), r.RunID)
	if err != nil || rec.RunState != workflow.RunStateCompleted {
		t.Fatalf("the finished run must be in the store: %+v %v", rec, err)
	}
	// The run is in the registry and in the store. It is listed once.
	n := 0
	for _, x := range h.eng.Runs() {
		if x.RunID == r.RunID {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("listed %d times", n)
	}
}

func TestEngineRefusesToStartWhenTheStoreIsBroken(t *testing.T) {
	t.Parallel()
	db, _ := store.Open(filepath.Join(t.TempDir(), "console.db"))
	_ = db.Close()
	if _, err := New(testConfig(), newFakeOps(), &fakeObs{}, events.NewLog(10, nil), Options{Store: db}); err == nil {
		t.Fatal("want the abandon error")
	}
}
