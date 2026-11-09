package nodeagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kostiantyn-matsebora/barekube/console/internal/config"
)

type agentStub struct {
	mu       sync.Mutex
	resp     map[string]any // path -> JSON body
	status   map[string]int
	seen     map[string]json.RawMessage
	lastAuth string
}

func newStub() *agentStub {
	return &agentStub{resp: map[string]any{}, status: map[string]int{}, seen: map[string]json.RawMessage{}}
}

func (a *agentStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.lastAuth = r.Header.Get("Authorization")
	var raw json.RawMessage
	_ = json.NewDecoder(r.Body).Decode(&raw)
	a.seen[r.URL.Path] = raw
	if st, ok := a.status[r.URL.Path]; ok && st != 200 {
		w.WriteHeader(st)
		if st == 500 {
			_ = json.NewEncoder(w).Encode(ErrorResponse{Error: "kubectl said no"})
		}
		return
	}
	_ = json.NewEncoder(w).Encode(a.resp[r.URL.Path])
}

func realFor(t *testing.T, stub *agentStub) *Real {
	t.Helper()
	srv := httptest.NewTLSServer(stub)
	t.Cleanup(srv.Close)
	host, portStr, _ := net.SplitHostPort(srv.Listener.Addr().String())
	port, _ := strconv.Atoi(portStr)
	sum := sha256.Sum256(srv.Certificate().Raw)
	return &Real{Host: host, Port: port, Token: "tok", PinnedCertSHA256: hex.EncodeToString(sum[:])}
}

func TestRealCallsEveryEndpoint(t *testing.T) {
	st := newStub()
	st.resp["/ping"] = map[string]any{}
	st.resp["/k3s/status"] = K3sStatusResponse{Active: true}
	st.resp["/kubectl/cordoned"] = CordonedResponse{Cordoned: true}
	st.resp["/kubectl/node-ready"] = NodeReadyResponse{Ready: true}
	st.resp["/kubectl/zero-replica-risk"] = ZeroReplicaRiskResponse{Volumes: []string{"v1"}}
	st.resp["/kubectl/longhorn-available"] = LonghornAvailableResponse{Available: true}
	st.resp["/etcd-readyz"] = ReadyzResponse{OK: true}
	st.resp["/etcd/is-leader"] = ReadyzResponse{OK: true}
	r := realFor(t, st)
	ctx := context.Background()

	if !r.Reachable(ctx) {
		t.Error("Reachable")
	}
	for name, err := range map[string]error{
		"reboot": r.Reboot(ctx), "stop": r.StopK3s(ctx), "kill": r.KillK3s(ctx), "start": r.StartK3s(ctx),
		"poweroff": r.PowerOff(ctx, true), "cordon": r.Cordon(ctx, "n"), "uncordon": r.Uncordon(ctx, "n"),
		"drain": r.Drain(ctx, "n", true, true, 90*time.Second), "lh": r.SetLonghornScheduling(ctx, "n", false),
	} {
		if err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if v, err := r.K3sActive(ctx); !v || err != nil {
		t.Error("K3sActive", v, err)
	}
	if v, err := r.Cordoned(ctx, "n"); !v || err != nil {
		t.Error("Cordoned", v, err)
	}
	if v, err := r.NodeReady(ctx, "n"); !v || err != nil {
		t.Error("NodeReady", v, err)
	}
	if v, err := r.ZeroReplicaRisk(ctx, "n"); len(v) != 1 || err != nil {
		t.Error("ZeroReplicaRisk", v, err)
	}
	if v, err := r.LonghornAvailable(ctx); !v || err != nil {
		t.Error("LonghornAvailable", v, err)
	}
	if !r.EtcdReadyz(ctx) {
		t.Error("EtcdReadyz")
	}
	if v, err := r.EtcdIsLeader(ctx); !v || err != nil {
		t.Error("EtcdIsLeader", v, err)
	}
	if err := r.EtcdSnapshot(ctx); err != nil {
		t.Error("EtcdSnapshot", err)
	}
	if st.lastAuth != "Bearer tok" {
		t.Errorf("auth header %q", st.lastAuth)
	}
	var d DrainRequest
	_ = json.Unmarshal(st.seen["/kubectl/drain"], &d)
	if d.Node != "n" || d.TimeoutSeconds != 90 || !d.IgnoreDaemonSets {
		t.Errorf("drain body %+v", d)
	}
	var p PowerOffRequest
	_ = json.Unmarshal(st.seen["/poweroff"], &p)
	if !p.Force {
		t.Error("force flag lost")
	}
}

func TestRealErrorHandling(t *testing.T) {
	st := newStub()
	st.status["/kubectl/drain"] = 500
	st.status["/reboot"] = 503
	st.resp["/k3s/status"] = "not an object"
	r := realFor(t, st)
	ctx := context.Background()

	err := r.Drain(ctx, "n", true, true, time.Second)
	if err == nil || !strings.Contains(err.Error(), "kubectl said no") || IsUnreachable(err) {
		t.Errorf("an authoritative error must carry its message and not count as unreachable: %v", err)
	}
	if err := r.Reboot(ctx); err == nil || !strings.Contains(err.Error(), "503") {
		t.Errorf("bare status: %v", err)
	}
	if _, err := r.K3sActive(ctx); err == nil {
		t.Error("garbage body accepted")
	}
	// every reading call surfaces a failing endpoint
	st.status["/k3s/status"], st.status["/kubectl/cordoned"], st.status["/kubectl/node-ready"] = 500, 500, 500
	st.status["/kubectl/zero-replica-risk"], st.status["/kubectl/longhorn-available"], st.status["/etcd/is-leader"], st.status["/etcd-readyz"] = 500, 500, 500, 500
	if _, err := r.K3sActive(ctx); err == nil {
		t.Error("K3sActive")
	}
	if _, err := r.Cordoned(ctx, "n"); err == nil {
		t.Error("Cordoned")
	}
	if _, err := r.NodeReady(ctx, "n"); err == nil {
		t.Error("NodeReady")
	}
	if _, err := r.ZeroReplicaRisk(ctx, "n"); err == nil {
		t.Error("ZeroReplicaRisk")
	}
	if _, err := r.LonghornAvailable(ctx); err == nil {
		t.Error("LonghornAvailable")
	}
	if _, err := r.EtcdIsLeader(ctx); err == nil {
		t.Error("EtcdIsLeader")
	}
	st.status["/etcd/snapshot"] = 500
	if err := r.EtcdSnapshot(ctx); err == nil {
		t.Error("EtcdSnapshot")
	}
	if r.EtcdReadyz(ctx) {
		t.Error("EtcdReadyz must be false on error")
	}
}

func TestRealRejectsAWrongCertificate(t *testing.T) {
	r := realFor(t, newStub())
	r.PinnedCertSHA256 = strings.Repeat("0", 64)
	err := r.Reboot(context.Background())
	if !IsUnreachable(err) || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("a wrong fingerprint must fail as unreachable: %v", err)
	}
	if r.Reachable(context.Background()) {
		t.Fatal("reachable despite a pin mismatch")
	}
}

func TestRealUnreachableHost(t *testing.T) {
	r := &Real{Host: "127.0.0.1", Port: 1, Token: "t", PinnedCertSHA256: "x"}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := r.Reboot(ctx); !IsUnreachable(err) {
		t.Fatalf("%v", err)
	}
	if IsUnreachable(nil) || IsUnreachable(context.Canceled) {
		t.Fatal("IsUnreachable false positives")
	}
}

func TestDoRejectsABadRequestBody(t *testing.T) {
	r := realFor(t, newStub())
	if err := r.do(context.Background(), "/x", make(chan int), nil); err == nil {
		t.Fatal("unmarshalable body accepted")
	}
	r.Host = "bad host"
	if err := r.do(context.Background(), "/x", nil, nil); err == nil {
		t.Fatal("bad url accepted")
	}
}

func TestTokenCompare(t *testing.T) {
	if !ConstantTimeTokenEqual("a", "a") || ConstantTimeTokenEqual("a", "b") || ConstantTimeTokenEqual("a", "ab") {
		t.Fatal("token comparison")
	}
}

func TestMockTracksK3sAndCordon(t *testing.T) {
	m := &Mock{NodeName: "n"}
	ctx := context.Background()
	if v, _ := m.K3sActive(ctx); !v {
		t.Fatal("mock starts with k3s active")
	}
	_ = m.StopK3s(ctx)
	if v, _ := m.K3sActive(ctx); v {
		t.Fatal("stop")
	}
	_ = m.StartK3s(ctx)
	_ = m.KillK3s(ctx)
	if v, _ := m.K3sActive(ctx); v {
		t.Fatal("kill")
	}
	_ = m.Cordon(ctx, "n")
	if v, _ := m.Cordoned(ctx, "n"); !v {
		t.Fatal("cordon")
	}
	_ = m.Uncordon(ctx, "n")
	if v, _ := m.Cordoned(ctx, "n"); v {
		t.Fatal("uncordon")
	}
	_ = m.Reboot(ctx)
	_ = m.PowerOff(ctx, false)
	_ = m.Drain(ctx, "n", true, true, time.Second)
	_ = m.SetLonghornScheduling(ctx, "n", true)
	if !m.Reachable(ctx) || !m.EtcdReadyz(ctx) {
		t.Fatal("mock reachability")
	}
	if ready, _ := m.NodeReady(ctx, "n"); !ready {
		t.Fatal("ready")
	}
	if v, _ := m.ZeroReplicaRisk(ctx, "n"); v != nil {
		t.Fatal("risk")
	}
	if ok, _ := m.LonghornAvailable(ctx); ok {
		t.Fatal("longhorn")
	}
	if err := m.EtcdSnapshot(ctx); err != nil {
		t.Fatal(err)
	}
	if l, _ := m.EtcdIsLeader(ctx); l {
		t.Fatal("leader")
	}
}

func TestNewClients(t *testing.T) {
	nodes := []config.Node{{Name: "a", IP: "10.0.0.1"}, {Name: "b", IP: "10.0.0.2", NodeAgentPort: 1234}}
	got, err := NewClients(config.NodeAgentConfig{Mock: true}, nodes)
	if err != nil || len(got) != 2 {
		t.Fatal(err)
	}
	if _, ok := got["a"].(*Mock); !ok {
		t.Fatal("mock mode must build mocks")
	}
	if _, err := NewClients(config.NodeAgentConfig{}, nodes); err == nil {
		t.Fatal("missing token accepted")
	}
	dir := t.TempDir()
	cfg := config.NodeAgentConfig{Token: "t", CredentialsDir: dir}
	if _, err := NewClients(cfg, nodes); err == nil {
		t.Fatal("missing fingerprint accepted")
	}
	_ = os.WriteFile(filepath.Join(dir, "a"), []byte(" abc \n"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "b"), []byte("def"), 0o600)
	got, err = NewClients(cfg, nodes)
	if err != nil {
		t.Fatal(err)
	}
	a, b := got["a"].(*Real), got["b"].(*Real)
	if a.Port != 9091 || b.Port != 1234 || a.PinnedCertSHA256 != "abc" || a.Host != "10.0.0.1" {
		t.Fatalf("%+v %+v", a, b)
	}
	_ = os.WriteFile(filepath.Join(dir, "a"), []byte("  "), 0o600)
	if _, err := NewClients(cfg, nodes); err == nil {
		t.Fatal("empty fingerprint accepted")
	}
}

// The leak: every call built a new transport, so every call opened a new
// connection that nothing closed. One client per node reuses its connection.
func TestRealReusesOneConnectionAcrossManyCalls(t *testing.T) {
	st := newStub()
	st.resp["/ping"] = map[string]any{}
	var mu sync.Mutex
	conns := 0
	srv := httptest.NewUnstartedServer(st)
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			mu.Lock()
			conns++
			mu.Unlock()
		}
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	host, portStr, _ := net.SplitHostPort(srv.Listener.Addr().String())
	port, _ := strconv.Atoi(portStr)
	sum := sha256.Sum256(srv.Certificate().Raw)
	r := &Real{Host: host, Port: port, Token: "tok", PinnedCertSHA256: hex.EncodeToString(sum[:])}
	for range 40 {
		if !r.Reachable(context.Background()) {
			t.Fatal("unreachable")
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if conns != 1 {
		t.Fatalf("40 sequential calls opened %d connections, want 1", conns)
	}
	if r.httpClient() != r.httpClient() {
		t.Fatal("the client must be built once")
	}
}
