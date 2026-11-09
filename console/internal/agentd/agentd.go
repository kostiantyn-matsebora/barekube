// Package agentd is the node-agent daemon: a fixed, narrow set of endpoints,
// never a generic "run this command". It runs under the unprivileged
// barekube-agent account, so its OS privilege is fixed at deploy time and
// authentication is one bearer token checked here.
package agentd

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/kostiantyn-matsebora/barekube/console/internal/nodeagent"
)

var nodeNamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]{0,251}[a-z0-9])?$`)

// Runner runs one command and returns its combined output.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

func execRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// Config is what the daemon needs to know about its node.
type Config struct {
	Token             string
	KubectlBin        string
	KubectlSubcommand string
	KubectlContext    string
	K3sUnit           string
	// EtcdMetricsURL is the local etcd metrics endpoint, for the leader probe.
	EtcdMetricsURL string
}

// Server is the HTTP side of the daemon.
type Server struct {
	token             string
	kubectlBin        string
	kubectlSubcommand string
	kubectlContext    string
	k3sUnit           string
	etcdMetricsURL    string

	run      Runner
	httpGet  func(ctx context.Context, url string) (string, error)
	dial     func(ctx context.Context, addr string) bool
	hbClient *http.Client
}

// New builds the server with the real command runner.
func New(c Config) *Server {
	if c.EtcdMetricsURL == "" {
		c.EtcdMetricsURL = "http://127.0.0.1:2381/metrics"
	}
	return &Server{token: c.Token, kubectlBin: c.KubectlBin, kubectlSubcommand: c.KubectlSubcommand,
		kubectlContext: c.KubectlContext, k3sUnit: c.K3sUnit, etcdMetricsURL: c.EtcdMetricsURL,
		run: execRunner, httpGet: httpGetBody, dial: dialTCP, hbClient: &http.Client{Timeout: 5 * time.Second}}
}

func httpGetBody(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	_, err = buf.ReadFrom(resp.Body)
	return buf.String(), err
}

// Handler routes every endpoint behind the bearer token.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	routes := map[string]http.HandlerFunc{
		"/ping": s.handlePing, "/reboot": s.handleReboot, "/poweroff": s.handlePowerOff,
		"/k3s/stop": s.handleK3sStop, "/k3s/kill": s.handleK3sKill, "/k3s/start": s.handleK3sStart, "/k3s/status": s.handleK3sStatus,
		"/kubectl/cordon": s.handleCordon, "/kubectl/uncordon": s.handleUncordon, "/kubectl/cordoned": s.handleCordoned,
		"/kubectl/drain": s.handleDrain, "/kubectl/node-ready": s.handleNodeReady,
		"/kubectl/longhorn-scheduling": s.handleLonghornScheduling, "/kubectl/zero-replica-risk": s.handleZeroReplicaRisk,
		"/kubectl/longhorn-available": s.handleLonghornAvailable,
		"/vip/bootstrap":              s.handleVipBootstrap, "/cluster/probe": s.handleClusterProbe,
		"/kubectl/longhorn-health": s.handleLonghornHealth, "/kubectl/vault-sealed": s.handleVaultSealed,
		"/etcd-readyz": s.handleEtcdReadyz, "/etcd/is-leader": s.handleEtcdIsLeader, "/etcd/snapshot": s.handleEtcdSnapshot,
	}
	for path, h := range routes {
		mux.HandleFunc("POST "+path, s.auth(h))
	}
	return mux
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !nodeagent.ConstantTimeTokenEqual(got, s.token) {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func writeError(w http.ResponseWriter, status int, err error) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(nodeagent.ErrorResponse{Error: err.Error()})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// kubectl runs exactly `<kubectlBin> [--context X] <args...>` — args are
// always built here from already-validated, typed request fields, never
// passed through from raw client input.
func (s *Server) kubectl(ctx context.Context, args ...string) (string, error) {
	full := args
	if s.kubectlContext != "" {
		full = append([]string{"--context", s.kubectlContext}, full...)
	}
	if s.kubectlSubcommand != "" {
		full = append([]string{s.kubectlSubcommand}, full...)
	}
	out, err := s.run(ctx, s.kubectlBin, full...)
	if err != nil {
		return "", &kubectlError{out: string(out), err: err}
	}
	return string(out), nil
}

type kubectlError struct {
	out string
	err error
}

func (e *kubectlError) Error() string { return e.err.Error() + ": " + e.out }

// decode reads the JSON body. A body that does not parse is a 400, never a
// silent success: console would take an empty 200 for a cordon that happened.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return false
	}
	return true
}

func validNodeName(w http.ResponseWriter, name string) bool {
	if nodeNamePattern.MatchString(name) {
		return true
	}
	writeError(w, http.StatusBadRequest, errInvalidNodeName)
	return false
}

var errInvalidNodeName = &nodeNameError{}

type nodeNameError struct{}

func (*nodeNameError) Error() string { return "invalid node name" }

// --- handlers ------------------------------------------------------------

func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleReboot(w http.ResponseWriter, r *http.Request) {
	// Deliberately not routed through s.kubectl: this is systemctl, not
	// kubectl, and runs under barekube-agent's own polkit grant for
	// org.freedesktop.login1.reboot.
	if out, err := s.run(r.Context(), "systemctl", "reboot"); err != nil {
		writeError(w, http.StatusInternalServerError, &kubectlError{out: string(out), err: err})
		return
	}
	w.WriteHeader(http.StatusOK)
}

// handlePowerOff, like handleReboot, is systemctl, not kubectl — the
// org.freedesktop.login1.power-off[-multiple-sessions] polkit grant is only
// ever written by `barekube install node-agent` on masters, so this call
// fails closed (polkit denial) on a Raspberry Pi agent even if asked.
//
// Force (--force) skips systemd's normal unit-stop ordering/timeouts and
// force-unmounts immediately — found testing against the real master-2
// (2026-10-10): a plain `systemctl poweroff` sat for well over a minute
// mid-shutdown (network already torn down, but ACPI power-off never
// fired), consistent with its documented failing/wearing NVMe making the
// final disk-sync/unmount step slow or stuck. `--force` is a request body
// field, not inferred from Mode — it's node-agent's own concern, not the
// orchestrator's Mode type.
func (s *Server) handlePowerOff(w http.ResponseWriter, r *http.Request) {
	var req nodeagent.PowerOffRequest
	_ = json.NewDecoder(r.Body).Decode(&req) // empty/absent body = graceful, same as every other bool default
	args := []string{"poweroff"}
	if req.Force {
		args = append(args, "--force")
	}
	if out, err := s.run(r.Context(), "systemctl", args...); err != nil {
		writeError(w, http.StatusInternalServerError, &kubectlError{out: string(out), err: err})
		return
	}
	w.WriteHeader(http.StatusOK)
}

// handleK3sStop/Kill/Start/Status act on PID 1 (systemd1), not logind —
// a different polkit action family (org.freedesktop.systemd1.manage-units)
// that is NOT scoped to one unit by default. The polkit rule written by
// `barekube install node-agent` restricts it to exactly s.k3sUnit and the
// start/stop/kill verbs; this handler just issues the plain systemctl call
// and relies on that scoping, same division of responsibility as reboot.
func (s *Server) handleK3sStop(w http.ResponseWriter, r *http.Request) {
	if out, err := s.run(r.Context(), "systemctl", "stop", s.k3sUnit); err != nil {
		writeError(w, http.StatusInternalServerError, &kubectlError{out: string(out), err: err})
		return
	}
	w.WriteHeader(http.StatusOK)
}

// handleK3sKill signals the unit, then stops it. The unit has Restart=always,
// so a kill alone is undone five seconds later. A stop in the restart window
// cancels the pending restart and leaves the unit down.
func (s *Server) handleK3sKill(w http.ResponseWriter, r *http.Request) {
	if out, err := s.run(r.Context(), "systemctl", "kill", s.k3sUnit); err != nil {
		writeError(w, http.StatusInternalServerError, &kubectlError{out: string(out), err: err})
		return
	}
	if out, err := s.run(r.Context(), "systemctl", "stop", s.k3sUnit); err != nil {
		writeError(w, http.StatusInternalServerError, &kubectlError{out: string(out), err: err})
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleK3sStart(w http.ResponseWriter, r *http.Request) {
	// Idempotent: a no-op if k3s already auto-started on boot (its systemd
	// unit stays enabled through a stop/kill — only the running state
	// changes), which happens after Power Off's manual power-on.
	if out, err := s.run(r.Context(), "systemctl", "start", s.k3sUnit); err != nil {
		writeError(w, http.StatusInternalServerError, &kubectlError{out: string(out), err: err})
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleK3sStatus(w http.ResponseWriter, r *http.Request) {
	// is-active exits non-zero for every state except "active" — that's the
	// answer itself, not a failure, so the exit code is deliberately ignored.
	out, _ := s.run(r.Context(), "systemctl", "is-active", s.k3sUnit)
	writeJSON(w, nodeagent.K3sStatusResponse{Active: strings.TrimSpace(string(out)) == "active"})
}

func (s *Server) handleCordon(w http.ResponseWriter, r *http.Request) {
	var req nodeagent.CordonRequest
	if !decode(w, r, &req) || !validNodeName(w, req.Node) {
		return
	}
	if _, err := s.kubectl(r.Context(), "cordon", req.Node); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleUncordon(w http.ResponseWriter, r *http.Request) {
	var req nodeagent.CordonRequest
	if !decode(w, r, &req) || !validNodeName(w, req.Node) {
		return
	}
	if _, err := s.kubectl(r.Context(), "uncordon", req.Node); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleCordoned(w http.ResponseWriter, r *http.Request) {
	var req nodeagent.CordonedRequest
	if !decode(w, r, &req) || !validNodeName(w, req.Node) {
		return
	}
	out, err := s.kubectl(r.Context(), "get", "node", req.Node, "-o", "jsonpath={.spec.unschedulable}")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, nodeagent.CordonedResponse{Cordoned: strings.TrimSpace(out) == "true"})
}

func (s *Server) handleDrain(w http.ResponseWriter, r *http.Request) {
	var req nodeagent.DrainRequest
	if !decode(w, r, &req) || !validNodeName(w, req.Node) {
		return
	}
	args := []string{"drain", req.Node}
	if req.IgnoreDaemonSets {
		args = append(args, "--ignore-daemonsets")
	}
	if req.DeleteEmptyDirData {
		args = append(args, "--delete-emptydir-data")
	}
	if req.TimeoutSeconds > 0 {
		args = append(args, "--timeout="+time.Duration(req.TimeoutSeconds*int(time.Second)).String())
	}
	if _, err := s.kubectl(r.Context(), args...); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleNodeReady(w http.ResponseWriter, r *http.Request) {
	var req nodeagent.NodeReadyRequest
	if !decode(w, r, &req) || !validNodeName(w, req.Node) {
		return
	}
	out, err := s.kubectl(r.Context(), "get", "node", req.Node, "-o", `jsonpath={.status.conditions[?(@.type=="Ready")].status}`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, nodeagent.NodeReadyResponse{Ready: strings.TrimSpace(out) == "True"})
}

// longhornAvailable feature-detects the Longhorn CRDs rather than assuming
// them present — local dev (Rancher Desktop) and any cluster where Longhorn
// is down shouldn't hard-fail the Longhorn-specific checks, just skip them
// (matches the orchestrator's pre-node-agent design, which had the same
// graceful no-op for exactly this reason).
func (s *Server) longhornAvailable(ctx context.Context) bool {
	_, err := s.kubectl(ctx, "get", "crd", "nodes.longhorn.io")
	return err == nil
}

func (s *Server) handleLonghornScheduling(w http.ResponseWriter, r *http.Request) {
	var req nodeagent.LonghornSchedulingRequest
	if !decode(w, r, &req) || !validNodeName(w, req.Node) {
		return
	}
	if !s.longhornAvailable(r.Context()) {
		w.WriteHeader(http.StatusOK)
		return
	}
	patch := `{"spec":{"allowScheduling":false}}`
	if req.Allow {
		patch = `{"spec":{"allowScheduling":true}}`
	}
	if _, err := s.kubectl(r.Context(), "patch", "nodes.longhorn.io", req.Node, "-n", "longhorn-system", "--type=merge", "-p", patch); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

type longhornReplica struct {
	Spec struct {
		VolumeName string `json:"volumeName"`
		NodeID     string `json:"nodeID"`
	} `json:"spec"`
	Status struct {
		CurrentState string `json:"currentState"`
	} `json:"status"`
}

func (s *Server) handleZeroReplicaRisk(w http.ResponseWriter, r *http.Request) {
	var req nodeagent.ZeroReplicaRiskRequest
	if !decode(w, r, &req) || !validNodeName(w, req.Node) {
		return
	}
	if !s.longhornAvailable(r.Context()) {
		writeJSON(w, nodeagent.ZeroReplicaRiskResponse{})
		return
	}
	out, err := s.kubectl(r.Context(), "get", "replicas.longhorn.io", "-n", "longhorn-system", "-o", "json")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	var list struct {
		Items []longhornReplica `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	atRisk := map[string]bool{}
	for _, rep := range list.Items {
		if rep.Spec.NodeID != req.Node || rep.Status.CurrentState != "running" {
			continue
		}
		healthyElsewhere := false
		for _, other := range list.Items {
			if other.Spec.VolumeName == rep.Spec.VolumeName && other.Spec.NodeID != req.Node && other.Status.CurrentState == "running" {
				healthyElsewhere = true
				break
			}
		}
		if !healthyElsewhere {
			atRisk[rep.Spec.VolumeName] = true
		}
	}
	var volumes []string
	for v := range atRisk {
		volumes = append(volumes, v)
	}
	writeJSON(w, nodeagent.ZeroReplicaRiskResponse{Volumes: volumes})
}

func (s *Server) handleLonghornAvailable(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, nodeagent.LonghornAvailableResponse{Available: s.longhornAvailable(r.Context())})
}

// runHeartbeat pushes this node's own k3s-active sample to console every
// interval, for the life of ctx (the process's own lifetime — main passes
// context.Background()) — a steady-state background reporter, not a
// one-shot wait-for-condition poll, so it keeps a ticker running
// indefinitely rather than giving up after this repo's usual 15s poll cap.
// See console/docs/workflows.md Phase 2.1: before this,
// console only ever learned a node's real state by asking for it (at
// startup, or mid-operation) — a node that died or came back in between
// left console's view of it exactly as it last was, an assumption rather
// than an observation.
//
// Deliberately a plain HTTP POST, not cert-pinned HTTPS like the opposite
// direction (internal/nodeagent.Real): console's own UI/API already serves
// plain HTTP at this address (console/docs/workflows.md,
// "Phase 1 deployed to the Chateau"), and this push carries the same
// shared bearer token already used for the reverse direction, not a new
// secret that would newly need TLS to protect it in transit beyond what
// the existing LAN-only zone placement already provides.
func (s *Server) RunHeartbeat(ctx context.Context, consoleAddr, nodeName, token string, interval time.Duration) {
	client := s.hbClient
	url := strings.TrimRight(consoleAddr, "/") + "/api/internal/heartbeat"
	lastLoggedState := "" // only log console's echoed state on change — a 5s-interval send isn't an event worth a log line every time, same "start/done, not every poll" spirit as internal/orchestrator's own event log

	send := func() {
		out, _ := s.run(ctx, "systemctl", "is-active", s.k3sUnit)
		req := nodeagent.HeartbeatRequest{
			Node:      nodeName,
			Timestamp: time.Now(),
			K3sActive: strings.TrimSpace(string(out)) == "active",
		}
		body, err := json.Marshal(req)
		if err != nil {
			log.Printf("[heartbeat] marshal: %v", err)
			return
		}
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			log.Printf("[heartbeat] build request: %v", err)
			return
		}
		httpReq.Header.Set("Authorization", "Bearer "+token)
		httpReq.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(httpReq)
		if err != nil {
			log.Printf("[heartbeat] send to %s: %v", consoleAddr, err)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			log.Printf("[heartbeat] %s: unexpected status %d", consoleAddr, resp.StatusCode)
			return
		}
		var hbResp nodeagent.HeartbeatResponse
		if err := json.NewDecoder(resp.Body).Decode(&hbResp); err == nil && hbResp.State != "" && hbResp.State != lastLoggedState {
			log.Printf("[heartbeat] console reports %s state=%s %s", nodeName, hbResp.State, hbResp.Message)
			lastLoggedState = hbResp.State
		}
	}

	send()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			send()
		}
	}
}

// handleEtcdReadyz routes through the same authenticated kubectl path as
// every other endpoint, NOT a raw HTTP GET to the apiserver — confirmed
// during testing that /readyz requires authentication (401 unauthenticated)
// on this cluster, so a bare TLS probe can't work regardless of TLS
// settings; `kubectl get --raw=/readyz` carries the same credentials
// (k3s's local kubeconfig in production, the dev kubeconfig locally) that
// every other command here already uses.
func (s *Server) handleEtcdReadyz(w http.ResponseWriter, r *http.Request) {
	out, err := s.kubectl(r.Context(), "get", "--raw=/readyz")
	writeJSON(w, nodeagent.ReadyzResponse{OK: err == nil && strings.TrimSpace(out) == "ok"})
}

// handleEtcdIsLeader reads this member's own etcd metrics. Only the leader
// exports etcd_server_is_leader 1.
func (s *Server) handleEtcdIsLeader(w http.ResponseWriter, r *http.Request) {
	body, err := s.httpGet(r.Context(), s.etcdMetricsURL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	leader := false
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "etcd_server_is_leader ") {
			leader = strings.TrimSpace(strings.TrimPrefix(line, "etcd_server_is_leader ")) == "1"
		}
	}
	writeJSON(w, nodeagent.ReadyzResponse{OK: leader})
}

// snapshotArgs is the one privileged command. The sudoers rule installed by
// `barekube install node-agent` allows exactly this argv and nothing else.
var snapshotArgs = []string{"-n", "/usr/local/bin/k3s", "etcd-snapshot", "save", "--name", "console-shutdown"}

// handleEtcdSnapshot saves a local etcd snapshot on this master.
func (s *Server) handleEtcdSnapshot(w http.ResponseWriter, r *http.Request) {
	if out, err := s.run(r.Context(), "sudo", snapshotArgs...); err != nil {
		writeError(w, http.StatusInternalServerError, &kubectlError{out: string(out), err: err})
		return
	}
	w.WriteHeader(http.StatusOK)
}
