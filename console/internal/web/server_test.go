package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/kostiantyn-matsebora/barekube/console/internal/app"
	"github.com/kostiantyn-matsebora/barekube/console/internal/events"
	"github.com/kostiantyn-matsebora/barekube/console/internal/flows"
)

type fakeBackend struct {
	startErr error
	started  []string
	filter   events.Filter
	hbErr    error
	hb       []string
}

func (f *fakeBackend) Nodes() []app.NodeView { return []app.NodeView{{Name: "a1", State: "healthy"}} }
func (f *fakeBackend) Runs() []flows.RunInfo {
	return []flows.RunInfo{{RunID: "r1", Workflow: "reboot"}}
}
func (f *fakeBackend) Workflows() []flows.WorkflowInfo {
	return []flows.WorkflowInfo{{Name: "reboot", Mermaid: "flowchart TD"}}
}
func (f *fakeBackend) Events(fl events.Filter) []events.Event {
	f.filter = fl
	return []events.Event{{ID: 1, Kind: "run.status"}}
}
func (f *fakeBackend) StartNode(_ context.Context, wf, node string, mode flows.Mode, by string) (flows.RunInfo, error) {
	f.started = append(f.started, wf+" "+node+" "+string(mode)+" "+by)
	return flows.RunInfo{RunID: "r9", Workflow: wf, Node: node}, f.startErr
}
func (f *fakeBackend) StartCluster(_ context.Context, by string) (flows.RunInfo, error) {
	f.started = append(f.started, "cluster "+by)
	return flows.RunInfo{RunID: "c1"}, f.startErr
}
func (f *fakeBackend) StartClusterStart(_ context.Context, by string) (flows.RunInfo, error) {
	f.started = append(f.started, "cluster-start "+by)
	return flows.RunInfo{RunID: "c2"}, f.startErr
}
func (f *fakeBackend) Platform(context.Context) app.PlatformView {
	return app.PlatformView{Shown: true, State: "deadlock", CanRestore: true, Components: []app.PlatformComponent{{Name: "Cilium", State: "down"}}}
}
func (f *fakeBackend) StartVipRestore(_ context.Context, by string) (flows.RunInfo, error) {
	f.started = append(f.started, "vip-restore "+by)
	return flows.RunInfo{RunID: "p1"}, f.startErr
}
func (f *fakeBackend) Heartbeat(node string, k3s bool) error {
	f.hb = append(f.hb, node)
	return f.hbErr
}

func setup(t *testing.T) (*Server, *fakeBackend, http.Handler) {
	t.Helper()
	be := &fakeBackend{}
	s, err := New(be, Tokens{UI: "ui", Node: "node", Automation: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	return s, be, s.Handler()
}

func do(h http.Handler, method, path string, body string, mod func(*http.Request)) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if method == http.MethodPost && body != "" && !strings.HasPrefix(body, "{") {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if mod != nil {
		mod(r)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func login(t *testing.T, h http.Handler) *http.Cookie {
	t.Helper()
	w := do(h, "POST", "/login", url.Values{"token": {"ui"}}.Encode(), nil)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("login %d", w.Code)
	}
	return w.Result().Cookies()[0]
}

func withCookie(c *http.Cookie) func(*http.Request) { return func(r *http.Request) { r.AddCookie(c) } }

func TestLoginFlow(t *testing.T) {
	_, _, h := setup(t)
	if w := do(h, "GET", "/login", "", nil); w.Code != 200 || !strings.Contains(w.Body.String(), "access token") {
		t.Fatal("login page")
	}
	if w := do(h, "POST", "/login", url.Values{"token": {"wrong"}}.Encode(), nil); w.Code != 401 || !strings.Contains(w.Body.String(), "invalid token") {
		t.Fatalf("bad login %d", w.Code)
	}
	c := login(t, h)
	w := do(h, "GET", "/", "", withCookie(c))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "<title>barekube console</title>") {
		t.Fatalf("index %d", w.Code)
	}
}

func TestEmptyUITokenNeverLogsIn(t *testing.T) {
	s, _ := New(&fakeBackend{}, Tokens{})
	if w := do(s.Handler(), "POST", "/login", "token=", nil); w.Code != 401 {
		t.Fatalf("%d", w.Code)
	}
}

func TestUnauthenticatedAccess(t *testing.T) {
	_, _, h := setup(t)
	if w := do(h, "GET", "/", "", nil); w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login" {
		t.Fatalf("page must redirect: %d", w.Code)
	}
	for _, p := range []string{"/api/nodes", "/api/runs", "/api/workflows", "/api/events"} {
		if w := do(h, "GET", p, "", nil); w.Code != 401 {
			t.Errorf("%s: %d", p, w.Code)
		}
	}
	if w := do(h, "POST", "/api/nodes/a1/reboot?mode=graceful", "", nil); w.Code != 401 {
		t.Fatalf("action without a session: %d", w.Code)
	}
	bad := withCookie(&http.Cookie{Name: sessionCookie, Value: "forged"})
	if w := do(h, "GET", "/api/nodes", "", bad); w.Code != 401 {
		t.Fatal("forged cookie accepted")
	}
}

func TestReadAPIs(t *testing.T) {
	_, be, h := setup(t)
	c := login(t, h)
	for path, want := range map[string]string{
		"/api/nodes": `"name":"a1"`, "/api/runs": `"runId":"r1"`, "/api/workflows": `"mermaid"`, "/api/events": `"run.status"`,
	} {
		w := do(h, "GET", path, "", withCookie(c))
		if w.Code != 200 || !strings.Contains(w.Body.String(), want) || w.Header().Get("Content-Type") != "application/json" {
			t.Errorf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
	do(h, "GET", "/api/events?node=a1&run=r1&workflow=reboot&after=42", "", withCookie(c))
	if be.filter != (events.Filter{Node: "a1", RunID: "r1", Workflow: "reboot", After: 42}) {
		t.Fatalf("filter %+v", be.filter)
	}
	if w := do(h, "GET", "/static/mermaid.min.js", "", nil); w.Code != 200 || w.Body.Len() < 1000 {
		t.Fatal("static asset")
	}
}

func TestNodeActions(t *testing.T) {
	_, be, h := setup(t)
	c := login(t, h)
	for _, wf := range []string{"reboot", "standby", "poweroff"} {
		w := do(h, "POST", "/api/nodes/a1/"+wf+"?mode=force", "", withCookie(c))
		if w.Code != http.StatusAccepted {
			t.Fatalf("%s: %d", wf, w.Code)
		}
		var info flows.RunInfo
		if json.Unmarshal(w.Body.Bytes(), &info) != nil || info.RunID != "r9" {
			t.Fatalf("body %s", w.Body.String())
		}
	}
	// rejoin needs no mode
	if w := do(h, "POST", "/api/nodes/a1/rejoin", "", withCookie(c)); w.Code != http.StatusAccepted {
		t.Fatalf("rejoin %d", w.Code)
	}
	want := []string{"reboot a1 force ui", "standby a1 force ui", "poweroff a1 force ui", "rejoin a1 graceful ui"}
	if strings.Join(be.started, "|") != strings.Join(want, "|") {
		t.Fatalf("%v", be.started)
	}
	if w := do(h, "POST", "/api/nodes/a1/reboot", "", withCookie(c)); w.Code != 400 {
		t.Fatalf("missing mode: %d", w.Code)
	}
	if w := do(h, "POST", "/api/nodes/a1/reboot?mode=yolo", "", withCookie(c)); w.Code != 400 {
		t.Fatalf("bad mode: %d", w.Code)
	}
}

func TestStartErrorsMapToStatuses(t *testing.T) {
	_, be, h := setup(t)
	c := login(t, h)
	for err, want := range map[error]int{
		flows.ErrUnknownNode: 404, flows.ErrUnsupported: 400, flows.ErrBusy: 409, flows.ErrClusterRunning: 409, flows.ErrNothingToDo: 409,
	} {
		be.startErr = err
		if w := do(h, "POST", "/api/nodes/a1/reboot?mode=force", "", withCookie(c)); w.Code != want {
			t.Errorf("%v: %d want %d", err, w.Code, want)
		}
	}
}

func TestClusterShutdownDoors(t *testing.T) {
	_, be, h := setup(t)
	if w := do(h, "POST", "/api/cluster/shutdown", "", nil); w.Code != 401 {
		t.Fatalf("anonymous: %d", w.Code)
	}
	if w := do(h, "POST", "/api/cluster/shutdown", "", func(r *http.Request) { r.Header.Set("Authorization", "Bearer node") }); w.Code != 401 {
		t.Fatal("the node token must not open the shutdown door")
	}
	auto := func(r *http.Request) { r.Header.Set("Authorization", "Bearer auto") }
	if w := do(h, "POST", "/api/cluster/shutdown", "", auto); w.Code != http.StatusAccepted {
		t.Fatalf("automation: %d", w.Code)
	}
	c := login(t, h)
	if w := do(h, "POST", "/api/cluster/shutdown", "", withCookie(c)); w.Code != http.StatusAccepted {
		t.Fatalf("ui: %d", w.Code)
	}
	if strings.Join(be.started, "|") != "cluster automation|cluster ui" {
		t.Fatalf("%v", be.started)
	}
	be.startErr = flows.ErrClusterActive
	if w := do(h, "POST", "/api/cluster/shutdown", "", auto); w.Code != 409 {
		t.Fatalf("already running: %d", w.Code)
	}
}

func TestAutomationTokenStartsEveryWorkflowAndReadsNothing(t *testing.T) {
	_, be, h := setup(t)
	auto := func(r *http.Request) { r.Header.Set("Authorization", "Bearer auto") }
	for _, wf := range []string{"reboot", "standby", "poweroff", "rejoin"} {
		if w := do(h, "POST", "/api/nodes/a1/"+wf+"?mode=force", "", auto); w.Code != http.StatusAccepted {
			t.Errorf("%s: %d", wf, w.Code)
		}
	}
	if w := do(h, "POST", "/api/cluster/shutdown", "", auto); w.Code != http.StatusAccepted {
		t.Errorf("cluster: %d", w.Code)
	}
	for _, s := range be.started {
		if !strings.HasSuffix(s, "automation") {
			t.Errorf("not recorded as the automation: %q", s)
		}
	}
	for _, p := range []string{"/api/nodes", "/api/runs", "/api/workflows", "/api/events"} {
		if w := do(h, "GET", p, "", auto); w.Code != 401 {
			t.Errorf("%s readable with the automation token", p)
		}
	}
	if w := do(h, "GET", "/", "", auto); w.Code != http.StatusSeeOther {
		t.Error("the page must not open for the automation token")
	}
	if w := do(h, "POST", "/api/internal/heartbeat", `{"node":"a1"}`, auto); w.Code != 401 {
		t.Error("heartbeat reachable with the automation token")
	}
	node := func(r *http.Request) { r.Header.Set("Authorization", "Bearer node") }
	if w := do(h, "POST", "/api/nodes/a1/reboot?mode=force", "", node); w.Code != 401 {
		t.Error("the node token must not start a workflow")
	}
	if w := do(h, "POST", "/api/nodes/a1/reboot?mode=force", "", nil); w.Code != 401 {
		t.Error("anonymous")
	}
}

func TestEmptyAutomationTokenClosesTheDoor(t *testing.T) {
	s, _ := New(&fakeBackend{}, Tokens{UI: "ui"})
	empty := func(r *http.Request) { r.Header.Set("Authorization", "Bearer ") }
	for _, p := range []string{"/api/cluster/shutdown", "/api/nodes/a1/reboot?mode=force"} {
		if w := do(s.Handler(), "POST", p, "", empty); w.Code != 401 {
			t.Fatalf("%s: %d", p, w.Code)
		}
	}
}

func TestHeartbeat(t *testing.T) {
	_, be, h := setup(t)
	node := func(r *http.Request) { r.Header.Set("Authorization", "Bearer node") }
	if w := do(h, "POST", "/api/internal/heartbeat", `{"node":"a1","k3sActive":true}`, nil); w.Code != 401 {
		t.Fatal("no token")
	}
	if w := do(h, "POST", "/api/internal/heartbeat", `{"node":"a1","k3sActive":true}`, node); w.Code != 200 || len(be.hb) != 1 {
		t.Fatalf("%d", w.Code)
	}
	if w := do(h, "POST", "/api/internal/heartbeat", `{not json`, node); w.Code != 400 {
		t.Fatalf("%d", w.Code)
	}
	be.hbErr = app.ErrUnknownNode
	if w := do(h, "POST", "/api/internal/heartbeat", `{"node":"ghost"}`, node); w.Code != 404 {
		t.Fatalf("%d", w.Code)
	}
	empty, _ := New(&fakeBackend{}, Tokens{})
	if w := do(empty.Handler(), "POST", "/api/internal/heartbeat", `{"node":"a1"}`, func(r *http.Request) { r.Header.Set("Authorization", "Bearer ") }); w.Code != 401 {
		t.Fatal("an empty node token must authenticate nothing")
	}
}

func TestClusterStartIsForAPersonAtTheUI(t *testing.T) {
	_, be, h := setup(t)
	for name, mod := range map[string]func(*http.Request){
		"anonymous":  nil,
		"automation": func(r *http.Request) { r.Header.Set("Authorization", "Bearer auto") },
		"node":       func(r *http.Request) { r.Header.Set("Authorization", "Bearer node") },
	} {
		if w := do(h, "POST", "/api/cluster/start", "", mod); w.Code != 401 {
			t.Errorf("%s: %d, a cold start is the UI's alone", name, w.Code)
		}
	}
	if len(be.started) != 0 {
		t.Fatalf("nothing may have started: %v", be.started)
	}
	c := login(t, h)
	if w := do(h, "POST", "/api/cluster/start", "", withCookie(c)); w.Code != http.StatusAccepted {
		t.Fatalf("ui: %d", w.Code)
	}
	if strings.Join(be.started, "|") != "cluster-start ui" {
		t.Fatalf("%v", be.started)
	}
	be.startErr = flows.ErrNothingToDo
	if w := do(h, "POST", "/api/cluster/start", "", withCookie(c)); w.Code != 409 {
		t.Fatalf("nothing to do: %d", w.Code)
	}
}

func TestPlatformIsReadAndRepairedByAPersonAtTheUIOnly(t *testing.T) {
	_, be, h := setup(t)
	for name, mod := range map[string]func(*http.Request){
		"anonymous":  nil,
		"automation": func(r *http.Request) { r.Header.Set("Authorization", "Bearer auto") },
		"node":       func(r *http.Request) { r.Header.Set("Authorization", "Bearer node") },
	} {
		if w := do(h, "GET", "/api/platform", "", mod); w.Code != 401 {
			t.Errorf("read, %s: %d", name, w.Code)
		}
		if w := do(h, "POST", "/api/platform/vip-restore", "", mod); w.Code != 401 {
			t.Errorf("repair, %s: %d", name, w.Code)
		}
	}
	if len(be.started) != 0 {
		t.Fatalf("nothing may have started: %v", be.started)
	}
	c := login(t, h)
	w := do(h, "GET", "/api/platform", "", withCookie(c))
	var v app.PlatformView
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &v) != nil || v.State != "deadlock" || len(v.Components) != 1 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := do(h, "POST", "/api/platform/vip-restore", "", withCookie(c)); w.Code != http.StatusAccepted {
		t.Fatalf("ui: %d", w.Code)
	}
	if strings.Join(be.started, "|") != "vip-restore ui" {
		t.Fatalf("%v", be.started)
	}
	be.startErr = flows.ErrBusy
	if w := do(h, "POST", "/api/platform/vip-restore", "", withCookie(c)); w.Code != 409 {
		t.Fatalf("busy: %d", w.Code)
	}
}
