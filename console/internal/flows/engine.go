// Package flows holds console's operations as declarative workflows on
// luno/workflow. A step moves a run only on an observed event, never on the
// assumption that an action worked.
package flows

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"

	"github.com/luno/workflow"
	"github.com/luno/workflow/adapters/memrecordstore"
	"github.com/luno/workflow/adapters/memrolescheduler"
	"github.com/luno/workflow/adapters/memstreamer"
	"github.com/luno/workflow/adapters/memtimeoutstore"
	"k8s.io/utils/clock"

	"github.com/kostiantyn-matsebora/barekube/console/internal/config"
	"github.com/kostiantyn-matsebora/barekube/console/internal/events"
	"github.com/kostiantyn-matsebora/barekube/console/internal/observe"
	"github.com/kostiantyn-matsebora/barekube/console/internal/store"
)

var errQuorum = errors.New("etcd quorum not healthy")

// Workflow names.
const (
	WfReboot   = "reboot"
	WfStandBy  = "standby"
	WfPowerOff = "poweroff"
	WfRejoin   = "rejoin"
	WfCluster  = "cluster-shutdown"
	// WfClusterStart brings a cluster back from fully off.
	WfClusterStart = "cluster-start"
	// WfVipRestore restores the API VIP, a platform-level (Cilium + kube-vip)
	// repair. It runs on its own from the platform card, and the cluster start
	// runs it as a child.
	WfVipRestore = "vip-restore"
)

// platformKey is the registry key of the platform layer: one platform repair
// at a time, apart from any node's key.
const platformKey = "\x00platform"

// keyFor is the registry key a run is filed under.
func keyFor(workflow, node string) string {
	switch {
	case isClusterWf(workflow):
		return clusterKey
	case workflow == WfVipRestore:
		return platformKey
	}
	return node
}

// isClusterWf is true for the workflows that act on the whole cluster. They
// share one registry key, so only one of them runs at a time.
func isClusterWf(name string) bool { return name == WfCluster || name == WfClusterStart }

var wfOperation = map[string]config.Operation{
	WfReboot: config.OpReboot, WfStandBy: config.OpStandBy, WfPowerOff: config.OpPowerOff, WfRejoin: config.OpRejoin,
}

// Observations is the read side of the observer.
type Observations interface {
	Latest(node string) (observe.Observation, bool)
}

// Options tunes the engine for tests and deployments.
type Options struct {
	Clock clock.Clock
	// Poll is how often waits are checked for an expired timeout.
	Poll time.Duration
	// Store keeps workflow state on disk. Nil keeps it in memory, which is
	// for tests only: the in-memory adapters never free anything.
	Store *store.DB
}

type entry struct {
	spec Spec
	wf   *workflow.Workflow[Job, St]
}

// Engine builds the workflows from their specs and runs them.
type Engine struct {
	cfg  *config.Config
	ops  Ops
	obs  Observations
	log  *events.Log
	clk  clock.Clock
	poll time.Duration
	db   *store.DB
	reg  *registry
	wfs  map[string]*entry

	heldMu sync.Mutex
	held   map[string]time.Time // runID -> first observation of a condition still holding
	seen   map[string]string    // key -> last text logged under it, so a finding is logged when it changes

	ctx    context.Context
	cancel context.CancelFunc
}

// New validates every spec and builds the workflows. Call Run to start them.
func New(cfg *config.Config, ops Ops, obs Observations, log *events.Log, opts Options) (*Engine, error) {
	if opts.Clock == nil {
		opts.Clock = clock.RealClock{}
	}
	if opts.Poll <= 0 {
		opts.Poll = time.Second
	}
	e := &Engine{cfg: cfg, obs: obs, log: log, clk: opts.Clock, poll: opts.Poll, db: opts.Store, reg: newRegistry(), wfs: map[string]*entry{}, held: map[string]time.Time{}, seen: map[string]string{}}
	e.ops = loggedOps{Ops: ops, log: log}
	e.ctx, e.cancel = context.WithCancel(context.Background())
	if e.db != nil {
		if err := e.abandonLive(); err != nil {
			return nil, err
		}
	}
	for _, spec := range []Spec{e.rebootSpec(), e.standBySpec(), e.powerOffSpec(), e.rejoinSpec(), e.clusterSpec(), e.clusterStartSpec(), e.vipRestoreSpec()} {
		if err := spec.Validate(); err != nil {
			return nil, err
		}
		e.wfs[spec.Name] = &entry{spec: spec, wf: e.build(spec)}
	}
	return e, nil
}

func (e *Engine) build(spec Spec) *workflow.Workflow[Job, St] {
	b := workflow.NewBuilder[Job, St](spec.Name)
	for _, st := range spec.Statuses() {
		if fn, ok := spec.Steps[st]; ok {
			b.AddStep(st, e.wrapStep(spec, st, fn), spec.dests(st, false)...)
		}
		if w, ok := spec.Waits[st]; ok {
			b.AddCallback(st, e.wrapEvent(spec, st, w), spec.dests(st, false)...)
			b.AddTimeout(st, e.timer(w), e.wrapTimeout(spec, st, w), spec.dests(st, true)...)
		}
	}
	var (
		es workflow.EventStreamer = memstreamer.New(memstreamer.WithClock(e.clk))
		rs workflow.RecordStore   = memrecordstore.New(memrecordstore.WithClock(e.clk))
		ts workflow.TimeoutStore  = memtimeoutstore.New(memtimeoutstore.WithClock(e.clk))
	)
	if e.db != nil {
		es, rs, ts = e.db.Streamer(e.clk), e.db.Records(e.clk), e.db.Timeouts(e.clk)
	}
	return b.Build(es, rs, memrolescheduler.New(),
		workflow.WithTimeoutStore(ts), workflow.WithClock(e.clk),
		workflow.WithDefaultOptions(workflow.PollingFrequency(e.poll), workflow.ErrBackOff(e.poll)))
}

func (e *Engine) timer(w Wait) workflow.TimerFunc[Job, St] {
	return func(_ context.Context, r *Run, now time.Time) (time.Time, error) {
		return now.Add(w.Timer(e.cfg.Timing, r.Object)), nil
	}
}

func (e *Engine) ref(spec Spec, r *Run) runRef {
	node := r.Object.Node
	return runRef{RunID: r.RunID, Workflow: spec.Name, Node: node}
}

func (e *Engine) wrapStep(spec Spec, from St, fn stepFn) workflow.ConsumerFunc[Job, St] {
	return func(ctx context.Context, r *Run) (St, error) {
		next := fn(withRef(ctx, e.ref(spec, r)), r)
		e.transitioned(spec, r, from, next)
		return next, nil
	}
}

func (e *Engine) wrapEvent(spec Spec, from St, w Wait) workflow.CallbackFunc[Job, St] {
	return func(ctx context.Context, r *Run, rd io.Reader) (St, error) {
		var p payload
		_ = json.NewDecoder(rd).Decode(&p)
		if w.Fresh && p.At.Before(r.UpdatedAt) {
			return skip, nil
		}
		next := w.OnEvent(withRef(ctx, e.ref(spec, r)), r, p)
		e.transitioned(spec, r, from, next)
		return next, nil
	}
}

func (e *Engine) wrapTimeout(spec Spec, from St, w Wait) workflow.TimeoutFunc[Job, St] {
	return func(ctx context.Context, r *Run, _ time.Time) (St, error) {
		ref := e.ref(spec, r)
		next := w.OnTimeout(withRef(ctx, ref), r)
		if next == skip {
			return skip, nil
		}
		e.log.Add(events.Event{Kind: events.TimeoutFired, Source: events.SourceTimeout, Node: ref.Node, RunID: r.RunID, Workflow: spec.Name, Detail: "while " + from.String()})
		e.transitioned(spec, r, from, next)
		return next, nil
	}
}

// transitioned records a status change in the registry and the event log.
func (e *Engine) transitioned(spec Spec, r *Run, from, next St) {
	if next == skip {
		return
	}
	j := r.Object
	key := keyFor(spec.Name, j.Node)
	ev := func(kind, source, detail string) {
		e.log.Add(events.Event{Kind: kind, Source: source, Node: j.Node, RunID: r.RunID, Workflow: spec.Name, Detail: detail})
	}
	if from == spec.Start() {
		detail := "by " + j.By
		if j.Parent != "" {
			detail = "by parent run " + j.Parent
		}
		ev(events.RunTriggered, events.SourceRun, detail)
	}
	ev(events.RunStatus, events.SourceRun, from.String()+" -> "+next.String())
	terminal := spec.Terminal(next)
	e.reg.update(key, r.RunID, next, terminal, j.Reason, e.clk.Now())
	if next == AwaitingClearance && j.Parent != "" {
		ev(events.ChildAwaiting, events.SourceChild, "waiting for the parent to release it")
	}
	if !terminal {
		return
	}
	kind := map[string]string{"completed": events.RunCompleted, "failed": events.RunFailed, "refused": events.RunRefused}[finalKind(next)]
	ev(kind, events.SourceRun, next.String()+reasonSuffix(j.Reason))
	if j.Parent != "" {
		ev(events.ChildCompleted, events.SourceChild, next.String())
	}
}

func reasonSuffix(reason string) string {
	if reason == "" {
		return ""
	}
	return ": " + reason
}

// Run starts every workflow's consumers. It returns immediately.
func (e *Engine) Run(ctx context.Context) {
	e.cancel()
	e.ctx, e.cancel = context.WithCancel(ctx)
	for _, en := range e.wfs {
		en.wf.Run(e.ctx)
	}
}

// Stop ends every workflow.
func (e *Engine) Stop() {
	e.cancel()
	for _, en := range e.wfs {
		en.wf.Stop()
	}
}

// Busy reports whether any run is active, so the observer can poll faster.
func (e *Engine) Busy() bool { return e.reg.anyActive() }

// Runs lists every known run, newest first.
func (e *Engine) Runs() []RunInfo {
	live := e.reg.list()
	if e.db == nil {
		return live
	}
	return e.withHistory(live)
}

// historyLimit caps how many stored runs one call reads.
const historyLimit = 200

// withHistory adds the finished runs kept in the store, so the list survives a
// restart. A run the registry already knows is not read twice.
func (e *Engine) withHistory(live []RunInfo) []RunInfo {
	recs, err := e.db.Records(e.clk).List(e.ctx, "", 0, historyLimit, workflow.OrderTypeDescending)
	if err != nil {
		return live
	}
	seen := make(map[string]bool, len(live))
	for _, r := range live {
		seen[r.RunID] = true
	}
	out := live
	for _, rec := range recs {
		if seen[rec.RunID] {
			continue
		}
		out = append(out, runFromRecord(rec))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out
}

// runFromRecord rebuilds a run's row from what the store holds. A record the
// library has closed without a terminal status, which is what a restart does
// to a live run, shows as failed with the reason it was closed.
func runFromRecord(rec workflow.Record) RunInfo {
	var j Job
	_ = json.Unmarshal(rec.Object, &j)
	st := St(rec.Status)
	finished := rec.RunState == workflow.RunStateCompleted || rec.RunState == workflow.RunStateCancelled || rec.RunState == workflow.RunStateDataDeleted
	info := RunInfo{
		RunID: rec.RunID, Workflow: rec.WorkflowName, Node: j.Node, Mode: j.Mode, Parent: j.Parent, By: j.By,
		Status: st.String(), Terminal: finished, Reason: j.Reason, StartedAt: rec.CreatedAt, UpdatedAt: rec.UpdatedAt, st: st,
	}
	if finished {
		info.Final = finalKind(st)
		if rec.RunState == workflow.RunStateCancelled {
			info.Final = "failed"
			if rec.Meta.RunStateReason != "" {
				info.Reason = rec.Meta.RunStateReason
			}
		}
	}
	return info
}

// WorkflowInfo describes one workflow for the UI.
type WorkflowInfo struct {
	Name    string `json:"name"`
	Mermaid string `json:"mermaid"`
}

// Workflows returns each workflow's diagram, in a stable order.
func (e *Engine) Workflows() []WorkflowInfo {
	var out []WorkflowInfo
	for _, n := range []string{WfReboot, WfStandBy, WfPowerOff, WfRejoin, WfCluster, WfClusterStart, WfVipRestore} {
		out = append(out, WorkflowInfo{Name: n, Mermaid: e.wfs[n].spec.Mermaid()})
	}
	return out
}

// StartNode starts a node workflow. by says who asked, for the event log.
func (e *Engine) StartNode(ctx context.Context, name, node string, mode Mode, by string) (RunInfo, error) {
	en, ok := e.wfs[name]
	if !ok || isClusterWf(name) || name == WfVipRestore {
		return RunInfo{}, fmt.Errorf("%w %q", ErrUnknownWorkflow, name)
	}
	n, ok := e.cfg.Node(node)
	if !ok {
		return RunInfo{}, fmt.Errorf("%w %q", ErrUnknownNode, node)
	}
	if !n.Supports(wfOperation[name]) {
		return RunInfo{}, fmt.Errorf("%w: %s on %s", ErrUnsupported, name, node)
	}
	if mode != ModeForce {
		mode = ModeGraceful
	}
	if err := e.alreadyDone(name, node); err != nil {
		return RunInfo{}, err
	}
	return e.start(ctx, en, node, &Job{Node: node, Role: string(n.Role), Mode: mode, By: by})
}

// isParked is true for a node that is stood by or off, from observation only.
func isParked(o observe.Observation) bool {
	st := observe.Derive(o)
	return st == observe.StateStoodBy || st == observe.StateOff
}

// alreadyDone refuses a stand by or power off of a node already seen parked.
// An unobserved node is never refused, since not knowing proves nothing.
func (e *Engine) alreadyDone(name, node string) error {
	if name != WfStandBy && name != WfPowerOff {
		return nil
	}
	if o, ok := e.obs.Latest(node); ok && isParked(o) {
		return fmt.Errorf("%w: %s is already %s", ErrNothingToDo, node, observe.Derive(o))
	}
	return nil
}

// StartCluster starts the cluster shutdown.
func (e *Engine) StartCluster(ctx context.Context, by string) (RunInfo, error) {
	// A shutdown that finds every node parked has nothing left to do.
	all := len(e.cfg.Nodes) > 0
	for _, n := range e.cfg.Nodes {
		if o, ok := e.obs.Latest(n.Name); !ok || !isParked(o) {
			all = false
		}
	}
	if all {
		return RunInfo{}, fmt.Errorf("%w: every node is already stood by or off", ErrNothingToDo)
	}
	return e.start(ctx, e.wfs[WfCluster], "cluster", &Job{By: by})
}

// StartClusterStart starts the cold start. It never refuses on what the
// observer sees of the nodes: a node reports Ready for as long as its kubelet
// runs, which is exactly what it does while Cilium and kube-vip are deadlocked
// and the API VIP is dead. Whether anything needs doing is the workflow's own
// finding, from probing Cilium, kube-vip and the VIP, and every step is a no-op
// when nothing is wrong.
func (e *Engine) StartClusterStart(ctx context.Context, by string) (RunInfo, error) {
	return e.start(ctx, e.wfs[WfClusterStart], "cluster", &Job{By: by})
}

// StartVipRestore starts the platform-level repair of the API VIP on its own.
// Like the cold start it never refuses on what the nodes look like: the
// workflow probes Cilium, kube-vip and the VIP and finds for itself whether
// anything needs doing.
func (e *Engine) StartVipRestore(ctx context.Context, by string) (RunInfo, error) {
	return e.start(ctx, e.wfs[WfVipRestore], "platform", &Job{By: by})
}

func (e *Engine) start(ctx context.Context, en *entry, foreignID string, job *Job) (RunInfo, error) {
	key := keyFor(en.spec.Name, job.Node)
	info := RunInfo{Workflow: en.spec.Name, Node: job.Node, Mode: job.Mode, Parent: job.Parent, By: job.By,
		Status: en.spec.Start().String(), StartedAt: e.clk.Now(), UpdatedAt: e.clk.Now(), key: key, st: en.spec.Start()}
	if err := e.reg.begin(info); err != nil {
		return RunInfo{}, err
	}
	runID, err := en.wf.Trigger(ctx, foreignID, workflow.WithInitialValue[Job, St](job))
	if err != nil {
		e.reg.abort(key)
		return RunInfo{}, err
	}
	info.RunID = runID
	return info, nil
}

// OnObservations delivers fresh observations to the active run of each node,
// then lets a running cluster workflow re-evaluate its children and the cluster.
func (e *Engine) OnObservations(batch []observe.Observation) {
	for _, obs := range batch {
		run, ok := e.reg.activeRun(obs.Node)
		if !ok {
			continue
		}
		e.deliver(run.Workflow, obs.Node, payload{Observation: obs})
	}
	if run, ok := e.reg.activeRun(clusterKey); ok {
		name := run.Workflow
		if _, known := e.wfs[name]; !known {
			name = WfCluster
		}
		e.deliver(name, "cluster", payload{})
	}
	if run, ok := e.reg.activeRun(platformKey); ok && e.wfs[run.Workflow] != nil {
		e.deliver(run.Workflow, "platform", payload{})
	}
}

func (e *Engine) deliver(name, foreignID string, p payload) {
	en := e.wfs[name]
	body, _ := json.Marshal(p)
	for _, st := range en.spec.WaitStatuses() {
		// A callback that finds the run at another status does nothing, and a
		// version conflict is retried by the next observation.
		_ = en.wf.Callback(e.ctx, foreignID, st, bytes.NewReader(body))
	}
}

func clusterEvent(ctx context.Context, r *Run, node string) events.Event {
	ref := refOf(ctx)
	return events.Event{Kind: events.Clearance, Source: events.SourceChild, Node: node, RunID: r.RunID, Workflow: ref.Workflow, Detail: "released to power off"}
}

// settled resolves a wait only once cond has held on every observation for at
// least Timing.Settle. One sample is a transient: systemd restarts a killed
// k3s five seconds later, so a single "inactive" proved nothing. A reading
// where cond fails forgets the clock and the wait starts over.
func (e *Engine) settled(cond func(observe.Observation) bool, next St) eventFn {
	return e.settledTo(cond, func(*Run) St { return next })
}

func (e *Engine) settledTo(cond func(observe.Observation) bool, next func(*Run) St) eventFn {
	return func(ctx context.Context, r *Run, p payload) St {
		e.heldMu.Lock()
		defer e.heldMu.Unlock()
		if !cond(p.Observation) {
			delete(e.held, r.RunID)
			return skip
		}
		since, ok := e.held[r.RunID]
		if !ok {
			e.held[r.RunID] = p.Observation.At
			return skip
		}
		if p.Observation.At.Sub(since) < e.cfg.Timing.Settle {
			return skip
		}
		delete(e.held, r.RunID)
		return next(r)
	}
}

// abandonLive is the restart rule: a run that was live when the process died
// is cancelled and logged, never resumed. See store.DB.AbandonLive.
func (e *Engine) abandonLive() error {
	const reason = "console restarted while this run was live"
	ids, err := e.db.AbandonLive(e.ctx, reason)
	if err != nil {
		return fmt.Errorf("abandon live runs: %w", err)
	}
	for _, id := range ids {
		e.log.Add(events.Event{Kind: events.RunFailed, Source: events.SourceRun, RunID: id, Detail: reason})
	}
	return nil
}
