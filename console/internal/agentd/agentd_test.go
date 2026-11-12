package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kostiantyn-matsebora/barekube/console/internal/nodeagent"
)

// script answers commands by their joined argv prefix.
type script struct {
	mu   sync.Mutex
	out  map[string]string
	fail map[string]bool
	// failMsg fails a command with that text as its output, to play a kubectl
	// error such as NotFound.
	failMsg map[string]string
	ran     []string
}

func (s *script) run(_ context.Context, name string, args ...string) ([]byte, error) {
	cmd := strings.Join(append([]string{name}, args...), " ")
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ran = append(s.ran, cmd)
	for prefix, msg := range s.failMsg {
		if strings.HasPrefix(cmd, prefix) {
			return []byte(msg), errors.New("exit 1")
		}
	}
	for prefix := range s.fail {
		if strings.HasPrefix(cmd, prefix) {
			return []byte("denied"), errors.New("exit 1")
		}
	}
	for prefix, out := range s.out {
		if strings.HasPrefix(cmd, prefix) {
			return []byte(out), nil
		}
	}
	return nil, nil
}

func (s *script) did(prefix string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.ran {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func setup(t *testing.T) (*Server, *script, http.Handler) {
	t.Helper()
	sc := &script{out: map[string]string{}, fail: map[string]bool{}, failMsg: map[string]string{}}
	s := New(Config{Token: "tok", KubectlBin: "k3s", KubectlSubcommand: "kubectl", K3sUnit: "k3s"})
	s.run = sc.run
	return s, sc, s.Handler()
}

func call(h http.Handler, path, body string, auth bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	if auth {
		r.Header.Set("Authorization", "Bearer tok")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestEveryRouteNeedsTheToken(t *testing.T) {
	_, _, h := setup(t)
	for _, p := range []string{"/ping", "/reboot", "/poweroff", "/k3s/stop", "/k3s/kill", "/k3s/start", "/k3s/status",
		"/kubectl/cordon", "/kubectl/uncordon", "/kubectl/cordoned", "/kubectl/drain", "/kubectl/node-ready",
		"/kubectl/longhorn-scheduling", "/kubectl/zero-replica-risk", "/kubectl/longhorn-available", "/etcd-readyz", "/etcd/is-leader", "/etcd/snapshot",
		"/vip/bootstrap", "/cluster/probe", "/kubectl/longhorn-health", "/kubectl/vault-sealed"} {
		if w := call(h, p, `{"node":"n"}`, false); w.Code != 401 {
			t.Errorf("%s answered %d without a token", p, w.Code)
		}
	}
	if w := call(h, "/ping", "", true); w.Code != 200 {
		t.Fatal("ping")
	}
}

func TestSystemctlEndpoints(t *testing.T) {
	_, sc, h := setup(t)
	cases := map[string]string{
		"/reboot": "systemctl reboot", "/k3s/stop": "systemctl stop k3s", "/k3s/kill": "systemctl kill k3s", "/k3s/start": "systemctl start k3s",
	}
	for path, want := range cases {
		if w := call(h, path, "", true); w.Code != 200 || !sc.did(want) {
			t.Errorf("%s: %d ran=%v", path, w.Code, sc.ran)
		}
		sc.fail[want] = true
		if w := call(h, path, "", true); w.Code != 500 || !strings.Contains(w.Body.String(), "denied") {
			t.Errorf("%s failure: %d %s", path, w.Code, w.Body.String())
		}
		delete(sc.fail, want) // a later case must not inherit this failure
	}
	call(h, "/poweroff", "", true)
	call(h, "/poweroff", `{"force":true}`, true)
	if !sc.did("systemctl poweroff") || !sc.did("systemctl poweroff --force") {
		t.Fatalf("%v", sc.ran)
	}
	sc.fail["systemctl poweroff"] = true
	if w := call(h, "/poweroff", "", true); w.Code != 500 {
		t.Fatal("poweroff failure")
	}
}

func TestK3sStatus(t *testing.T) {
	_, sc, h := setup(t)
	sc.out["systemctl is-active k3s"] = "active\n"
	var r nodeagent.K3sStatusResponse
	_ = json.Unmarshal(call(h, "/k3s/status", "", true).Body.Bytes(), &r)
	if !r.Active {
		t.Fatal("active")
	}
	sc.out["systemctl is-active k3s"] = "inactive\n"
	_ = json.Unmarshal(call(h, "/k3s/status", "", true).Body.Bytes(), &r)
	if r.Active {
		t.Fatal("inactive")
	}
}

func TestKubectlWrapping(t *testing.T) {
	s, sc, h := setup(t)
	s.kubectlContext = "dev"
	call(h, "/kubectl/cordon", `{"node":"a1"}`, true)
	if !sc.did("k3s kubectl --context dev cordon a1") {
		t.Fatalf("%v", sc.ran)
	}
	s.kubectlContext, s.kubectlSubcommand = "", ""
	call(h, "/kubectl/uncordon", `{"node":"a1"}`, true)
	if !sc.did("k3s uncordon a1") {
		t.Fatalf("%v", sc.ran)
	}
}

func TestNodeEndpointsValidateAndReportFailures(t *testing.T) {
	_, sc, h := setup(t)
	paths := []string{"/kubectl/cordon", "/kubectl/uncordon", "/kubectl/cordoned", "/kubectl/drain", "/kubectl/node-ready", "/kubectl/longhorn-scheduling", "/kubectl/zero-replica-risk"}
	for _, p := range paths {
		if w := call(h, p, `{"node":"a1; rm -rf /"}`, true); w.Code != 400 {
			t.Errorf("%s accepted a hostile node name: %d", p, w.Code)
		}
		if w := call(h, p, `{nope`, true); w.Code != 400 {
			t.Errorf("%s bad json must be a 400, got %d", p, w.Code)
		}
	}
	sc.fail["k3s kubectl"] = true
	for _, p := range []string{"/kubectl/cordon", "/kubectl/uncordon", "/kubectl/cordoned", "/kubectl/drain", "/kubectl/node-ready"} {
		if w := call(h, p, `{"node":"a1"}`, true); w.Code != 500 {
			t.Errorf("%s: %d", p, w.Code)
		}
	}
}

func TestReadEndpoints(t *testing.T) {
	_, sc, h := setup(t)
	sc.out["k3s kubectl get node a1 -o jsonpath={.spec.unschedulable}"] = "true"
	sc.out["k3s kubectl get node a1 -o jsonpath={.status.conditions"] = "True"
	var c nodeagent.CordonedResponse
	_ = json.Unmarshal(call(h, "/kubectl/cordoned", `{"node":"a1"}`, true).Body.Bytes(), &c)
	var rd nodeagent.NodeReadyResponse
	_ = json.Unmarshal(call(h, "/kubectl/node-ready", `{"node":"a1"}`, true).Body.Bytes(), &rd)
	if !c.Cordoned || !rd.Ready {
		t.Fatalf("%v %v", c, rd)
	}
	sc.out["k3s kubectl get --raw=/readyz"] = "ok\n"
	var ok nodeagent.ReadyzResponse
	_ = json.Unmarshal(call(h, "/etcd-readyz", "", true).Body.Bytes(), &ok)
	if !ok.OK {
		t.Fatal("readyz")
	}
	sc.fail["k3s kubectl get --raw"] = true
	_ = json.Unmarshal(call(h, "/etcd-readyz", "", true).Body.Bytes(), &ok)
	if ok.OK {
		t.Fatal("readyz must be false on failure")
	}
}

func TestDrainArguments(t *testing.T) {
	_, sc, h := setup(t)
	call(h, "/kubectl/drain", `{"node":"a1","ignoreDaemonSets":true,"deleteEmptyDirData":true,"timeoutSeconds":90}`, true)
	if !sc.did("k3s kubectl drain a1 --ignore-daemonsets --delete-emptydir-data --timeout=1m30s") {
		t.Fatalf("%v", sc.ran)
	}
	call(h, "/kubectl/drain", `{"node":"a2"}`, true)
	if !sc.did("k3s kubectl drain a2") || sc.did("k3s kubectl drain a2 --") {
		t.Fatalf("flags must be opt-in: %v", sc.ran)
	}
}

func TestLonghornEndpoints(t *testing.T) {
	_, sc, h := setup(t)
	var av nodeagent.LonghornAvailableResponse
	_ = json.Unmarshal(call(h, "/kubectl/longhorn-available", "", true).Body.Bytes(), &av)
	if !av.Available {
		t.Fatal("available")
	}
	call(h, "/kubectl/longhorn-scheduling", `{"node":"a1","allow":false}`, true)
	call(h, "/kubectl/longhorn-scheduling", `{"node":"a1","allow":true}`, true)
	if !sc.did(`k3s kubectl patch nodes.longhorn.io a1 -n longhorn-system --type=merge -p {"spec":{"allowScheduling":false}}`) ||
		!sc.did(`k3s kubectl patch nodes.longhorn.io a1 -n longhorn-system --type=merge -p {"spec":{"allowScheduling":true}}`) {
		t.Fatalf("%v", sc.ran)
	}
	sc.fail["k3s kubectl patch"] = true
	if w := call(h, "/kubectl/longhorn-scheduling", `{"node":"a1","allow":true}`, true); w.Code != 500 {
		t.Fatal("patch failure")
	}
	delete(sc.fail, "k3s kubectl patch")

	sc.fail["k3s kubectl get crd"] = true // longhorn absent: everything is a quiet no-op
	_ = json.Unmarshal(call(h, "/kubectl/longhorn-available", "", true).Body.Bytes(), &av)
	if av.Available {
		t.Fatal("unavailable")
	}
	if w := call(h, "/kubectl/longhorn-scheduling", `{"node":"a1","allow":false}`, true); w.Code != 200 {
		t.Fatal("scheduling without longhorn must be a no-op")
	}
	var risk nodeagent.ZeroReplicaRiskResponse
	_ = json.Unmarshal(call(h, "/kubectl/zero-replica-risk", `{"node":"a1"}`, true).Body.Bytes(), &risk)
	if len(risk.Volumes) != 0 {
		t.Fatal("no longhorn, no risk")
	}
}

func TestZeroReplicaRisk(t *testing.T) {
	_, sc, h := setup(t)
	const list = `{"items":[
	 {"spec":{"volumeName":"solo","nodeID":"a1"},"status":{"currentState":"running"}},
	 {"spec":{"volumeName":"safe","nodeID":"a1"},"status":{"currentState":"running"}},
	 {"spec":{"volumeName":"safe","nodeID":"m1"},"status":{"currentState":"running"}},
	 {"spec":{"volumeName":"weak","nodeID":"a1"},"status":{"currentState":"running"}},
	 {"spec":{"volumeName":"weak","nodeID":"m1"},"status":{"currentState":"stopped"}},
	 {"spec":{"volumeName":"idle","nodeID":"a1"},"status":{"currentState":"stopped"}}]}`
	sc.out["k3s kubectl get replicas.longhorn.io"] = list
	var risk nodeagent.ZeroReplicaRiskResponse
	_ = json.Unmarshal(call(h, "/kubectl/zero-replica-risk", `{"node":"a1"}`, true).Body.Bytes(), &risk)
	got := strings.Join(risk.Volumes, ",")
	if len(risk.Volumes) != 2 || !strings.Contains(got, "solo") || !strings.Contains(got, "weak") || strings.Contains(got, "safe") || strings.Contains(got, "idle") {
		t.Fatalf("at risk: %v", risk.Volumes)
	}
	sc.out["k3s kubectl get replicas.longhorn.io"] = "not json"
	if w := call(h, "/kubectl/zero-replica-risk", `{"node":"a1"}`, true); w.Code != 500 {
		t.Fatal("garbage")
	}
	sc.fail["k3s kubectl get replicas"] = true
	if w := call(h, "/kubectl/zero-replica-risk", `{"node":"a1"}`, true); w.Code != 500 {
		t.Fatal("kubectl failure")
	}
}

func TestEtcdIsLeader(t *testing.T) {
	s, _, h := setup(t)
	metrics := "# HELP x\netcd_server_has_leader 1\netcd_server_is_leader 1\n"
	s.httpGet = func(context.Context, string) (string, error) { return metrics, nil }
	var r nodeagent.ReadyzResponse
	_ = json.Unmarshal(call(h, "/etcd/is-leader", "", true).Body.Bytes(), &r)
	if !r.OK {
		t.Fatal("leader")
	}
	metrics = "etcd_server_has_leader 1\netcd_server_is_leader 0\n"
	_ = json.Unmarshal(call(h, "/etcd/is-leader", "", true).Body.Bytes(), &r)
	if r.OK {
		t.Fatal("follower")
	}
	s.httpGet = func(context.Context, string) (string, error) { return "", errors.New("down") }
	if w := call(h, "/etcd/is-leader", "", true); w.Code != 500 {
		t.Fatal("unreachable metrics")
	}
}

func TestHttpGetBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("etcd_server_is_leader 1\n")) }))
	defer srv.Close()
	body, err := httpGetBody(context.Background(), srv.URL)
	if err != nil || !strings.Contains(body, "is_leader 1") {
		t.Fatal(body, err)
	}
	if _, err := httpGetBody(context.Background(), "http://127.0.0.1:1/"); err == nil {
		t.Fatal("dead url")
	}
	if _, err := httpGetBody(context.Background(), "://bad"); err == nil {
		t.Fatal("bad url")
	}
	if s := New(Config{}); s.etcdMetricsURL == "" {
		t.Fatal("default metrics url")
	}
}

func TestExecRunner(t *testing.T) {
	out, err := execRunner(context.Background(), "echo", "hi")
	if err != nil || strings.TrimSpace(string(out)) != "hi" {
		t.Fatal(string(out), err)
	}
}

func TestHeartbeat(t *testing.T) {
	var mu sync.Mutex
	var got []nodeagent.HeartbeatRequest
	auth := ""
	status := 200
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		auth = r.Header.Get("Authorization")
		var hb nodeagent.HeartbeatRequest
		_ = json.NewDecoder(r.Body).Decode(&hb)
		got = append(got, hb)
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(nodeagent.HeartbeatResponse{State: "ok"})
	}))
	defer srv.Close()
	s, sc, _ := setup(t)
	sc.out["systemctl is-active k3s"] = "active\n"
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.RunHeartbeat(ctx, srv.URL+"/", "a1", "tok", 10*time.Millisecond); close(done) }()
	time.Sleep(60 * time.Millisecond)
	mu.Lock()
	status = 500 // an unhappy console is logged, not fatal
	mu.Unlock()
	time.Sleep(30 * time.Millisecond)
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	if len(got) < 3 || got[0].Node != "a1" || !got[0].K3sActive || auth != "Bearer tok" {
		t.Fatalf("%d heartbeats %+v auth=%q", len(got), got, auth)
	}
}

func TestHeartbeatSurvivesBadTargets(t *testing.T) {
	s, _, _ := setup(t)
	for _, addr := range []string{"http://127.0.0.1:1", "://bad"} {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
		s.RunHeartbeat(ctx, addr, "a1", "tok", 10*time.Millisecond)
		cancel()
	}
}

func TestEtcdSnapshotRunsExactlyTheSudoersCommand(t *testing.T) {
	_, sc, h := setup(t)
	if w := call(h, "/etcd/snapshot", "", true); w.Code != 200 {
		t.Fatalf("%d", w.Code)
	}
	if !sc.did("sudo -n /usr/local/bin/k3s etcd-snapshot save --name console-shutdown") || len(sc.ran) != 1 {
		t.Fatalf("%v", sc.ran)
	}
	sc.fail["sudo"] = true
	if w := call(h, "/etcd/snapshot", "", true); w.Code != 500 || !strings.Contains(w.Body.String(), "denied") {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

// systemd restarts a killed k3s (Restart=always, RestartSec=5s). A stop after
// the kill cancels the pending restart, so the unit stays down.
func TestK3sKillAlsoStopsSoSystemdDoesNotRestartIt(t *testing.T) {
	_, sc, h := setup(t)
	if w := call(h, "/k3s/kill", "", true); w.Code != 200 {
		t.Fatalf("%d", w.Code)
	}
	kill, stop := -1, -1
	for i, c := range sc.ran {
		switch c {
		case "systemctl kill k3s":
			kill = i
		case "systemctl stop k3s":
			stop = i
		}
	}
	if kill < 0 || stop < 0 || stop < kill {
		t.Fatalf("want kill then stop, ran %v", sc.ran)
	}
	sc.fail["systemctl stop k3s"] = true
	if w := call(h, "/k3s/kill", "", true); w.Code != 500 {
		t.Fatalf("a failed stop after the kill must be reported: %d", w.Code)
	}
}
