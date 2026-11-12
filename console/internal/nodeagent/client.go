package nodeagent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Client is the interface the orchestrator uses — real or mock. Every
// method maps to exactly one fixed endpoint on the daemon; there is no
// generic "run a command" method, by design.
type Client interface {
	Reboot(ctx context.Context) error
	Reachable(ctx context.Context) bool

	// StopK3s/KillK3s/StartK3s/PowerOff/K3sActive act on the node's own k3s
	// service and power state — direct per-node calls, same as Reboot, never
	// routed through MasterPool. See console/docs/workflows.md
	// Phase 2 for why these go through two different polkit action families
	// (systemd1.manage-units vs login1.*).
	StopK3s(ctx context.Context) error
	KillK3s(ctx context.Context) error
	StartK3s(ctx context.Context) error
	K3sActive(ctx context.Context) (bool, error)
	PowerOff(ctx context.Context, force bool) error

	Cordon(ctx context.Context, node string) error
	Uncordon(ctx context.Context, node string) error
	Cordoned(ctx context.Context, node string) (bool, error)
	Drain(ctx context.Context, node string, ignoreDaemonSets, deleteEmptyDirData bool, timeout time.Duration) error
	NodeReady(ctx context.Context, node string) (bool, error)
	SetLonghornScheduling(ctx context.Context, node string, allow bool) error
	ZeroReplicaRisk(ctx context.Context, node string) ([]string, error)
	LonghornAvailable(ctx context.Context) (bool, error)
	EtcdReadyz(ctx context.Context) bool
	// EtcdIsLeader reports whether this node's etcd member is the raft leader.
	EtcdIsLeader(ctx context.Context) (bool, error)
	// EtcdSnapshot saves a local etcd snapshot on this master.
	EtcdSnapshot(ctx context.Context) error

	// VipBootstrap starts or stops this master's fixed vip-bootstrap unit,
	// which holds the API VIP by hand while Cilium and kube-vip wait on each
	// other after a cold start. Direct per-node call, never through the pool.
	VipBootstrap(ctx context.Context, on bool) error
	ClusterProbe(ctx context.Context, vip string) (ClusterProbeResponse, error)
	LonghornHealth(ctx context.Context) (LonghornHealthResponse, error)
	VaultSealed(ctx context.Context, namespace string) (VaultSealedResponse, error)
}

// Real talks to a live node-agent daemon over HTTPS, bearer-token
// authenticated, with the daemon's TLS cert pinned by SHA-256 fingerprint
// (captured once during provisioning — `barekube install node-agent`) —
// same reasoning as the Cockpit design it replaced: the channel carries a
// secret on every call, so the server's identity must be verified, not
// skipped.
type Real struct {
	Host             string
	Port             int
	Token            string
	PinnedCertSHA256 string

	once   sync.Once
	client *http.Client
}

// httpClient deliberately has no Client.Timeout: that's a blanket ceiling
// applied regardless of context, and it caused a real bug — drain calls can
// legitimately take up to the node-agent's own internal kubectl drain
// timeout (2 minutes, PodDisruptionBudget-respecting eviction), which is
// longer than a fixed 30s client timeout used to allow, so drain always
// failed with a confusing "context deadline exceeded" on anything slower
// than 30s. Every caller already passes a context with its own appropriate
// deadline (the orchestrator's top-level 10-minute operation bound is well
// above any individual call's own ceiling) — respect that instead.
//
// The client is built once per node and reused. It used to be built on every
// call, and every call then opened a new TLS connection that nothing reused or
// closed: about two goroutines and the TLS state per probe, which grew the
// console past 350 MiB on the router. The idle limits below keep at most two
// connections per node and close them after 30s unused.
func (r *Real) httpClient() *http.Client {
	r.once.Do(func() { r.client = r.newClient() })
	return r.client
}

func (r *Real) newClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:        2,
			MaxIdleConnsPerHost: 2,
			IdleConnTimeout:     30 * time.Second,
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true, // substituted by VerifyPeerCertificate below
				VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
					if len(rawCerts) == 0 {
						return fmt.Errorf("node-agent TLS: no certificate presented")
					}
					sum := sha256.Sum256(rawCerts[0])
					got := hex.EncodeToString(sum[:])
					if !strings.EqualFold(got, r.PinnedCertSHA256) {
						return fmt.Errorf("node-agent TLS: certificate fingerprint mismatch (got %s, want %s)", got, r.PinnedCertSHA256)
					}
					return nil
				},
			},
		},
	}
}

func (r *Real) do(ctx context.Context, path string, reqBody, respBody any) error {
	var bodyReader io.Reader
	if reqBody != nil {
		b, err := json.Marshal(reqBody)
		if err != nil {
			return err
		}
		bodyReader = bytes.NewReader(b)
	}
	url := fmt.Sprintf("https://%s:%d%s", r.Host, r.Port, path)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bodyReader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+r.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.httpClient().Do(req)
	if err != nil {
		// Never even got a response — this master is genuinely
		// unreachable (dial/TLS/timeout), which is the ONE case
		// MasterPool.each should fail over on. Marked distinctly from
		// the non-200 branch below, where the master WAS reached and
		// gave an authoritative answer.
		return &UnreachableError{err: fmt.Errorf("node-agent %s: %w", path, err)}
	}
	// Read the body to the end before closing it. A body closed unread cannot be
	// reused, so every call would open a new TLS connection. The cap keeps a
	// misbehaving node-agent from streaming forever.
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		var e ErrorResponse
		_ = json.NewDecoder(resp.Body).Decode(&e)
		if e.Error != "" {
			return fmt.Errorf("node-agent %s: %d: %s", path, resp.StatusCode, e.Error)
		}
		return fmt.Errorf("node-agent %s: unexpected status %d", path, resp.StatusCode)
	}
	if respBody != nil {
		return json.NewDecoder(resp.Body).Decode(respBody)
	}
	return nil
}

func (r *Real) Reboot(ctx context.Context) error {
	return r.do(ctx, "/reboot", nil, nil)
}

func (r *Real) Reachable(ctx context.Context) bool {
	return r.do(ctx, "/ping", nil, nil) == nil
}

func (r *Real) StopK3s(ctx context.Context) error {
	return r.do(ctx, "/k3s/stop", nil, nil)
}

func (r *Real) KillK3s(ctx context.Context) error {
	return r.do(ctx, "/k3s/kill", nil, nil)
}

func (r *Real) StartK3s(ctx context.Context) error {
	return r.do(ctx, "/k3s/start", nil, nil)
}

func (r *Real) K3sActive(ctx context.Context) (bool, error) {
	var resp K3sStatusResponse
	if err := r.do(ctx, "/k3s/status", nil, &resp); err != nil {
		return false, err
	}
	return resp.Active, nil
}

func (r *Real) PowerOff(ctx context.Context, force bool) error {
	return r.do(ctx, "/poweroff", PowerOffRequest{Force: force}, nil)
}

func (r *Real) Cordon(ctx context.Context, node string) error {
	return r.do(ctx, "/kubectl/cordon", CordonRequest{Node: node}, nil)
}

func (r *Real) Cordoned(ctx context.Context, node string) (bool, error) {
	var resp CordonedResponse
	if err := r.do(ctx, "/kubectl/cordoned", CordonedRequest{Node: node}, &resp); err != nil {
		return false, err
	}
	return resp.Cordoned, nil
}

func (r *Real) Uncordon(ctx context.Context, node string) error {
	return r.do(ctx, "/kubectl/uncordon", CordonRequest{Node: node}, nil)
}

func (r *Real) Drain(ctx context.Context, node string, ignoreDaemonSets, deleteEmptyDirData bool, timeout time.Duration) error {
	return r.do(ctx, "/kubectl/drain", DrainRequest{
		Node: node, IgnoreDaemonSets: ignoreDaemonSets, DeleteEmptyDirData: deleteEmptyDirData,
		TimeoutSeconds: int(timeout.Seconds()),
	}, nil)
}

func (r *Real) NodeReady(ctx context.Context, node string) (bool, error) {
	var resp NodeReadyResponse
	if err := r.do(ctx, "/kubectl/node-ready", NodeReadyRequest{Node: node}, &resp); err != nil {
		return false, err
	}
	return resp.Ready, nil
}

func (r *Real) SetLonghornScheduling(ctx context.Context, node string, allow bool) error {
	return r.do(ctx, "/kubectl/longhorn-scheduling", LonghornSchedulingRequest{Node: node, Allow: allow}, nil)
}

func (r *Real) ZeroReplicaRisk(ctx context.Context, node string) ([]string, error) {
	var resp ZeroReplicaRiskResponse
	if err := r.do(ctx, "/kubectl/zero-replica-risk", ZeroReplicaRiskRequest{Node: node}, &resp); err != nil {
		return nil, err
	}
	return resp.Volumes, nil
}

func (r *Real) LonghornAvailable(ctx context.Context) (bool, error) {
	var resp LonghornAvailableResponse
	if err := r.do(ctx, "/kubectl/longhorn-available", nil, &resp); err != nil {
		return false, err
	}
	return resp.Available, nil
}

func (r *Real) EtcdReadyz(ctx context.Context) bool {
	var resp ReadyzResponse
	if err := r.do(ctx, "/etcd-readyz", nil, &resp); err != nil {
		return false
	}
	return resp.OK
}

func (r *Real) EtcdSnapshot(ctx context.Context) error {
	return r.do(ctx, "/etcd/snapshot", nil, nil)
}

func (r *Real) EtcdIsLeader(ctx context.Context) (bool, error) {
	var resp ReadyzResponse
	if err := r.do(ctx, "/etcd/is-leader", nil, &resp); err != nil {
		return false, err
	}
	return resp.OK, nil
}

// UnreachableError marks an error as "never even got a response from this
// master" (dial failure, TLS failure, timeout before any HTTP response) —
// as opposed to an error returned once a master DID answer but the
// operation itself failed (e.g. kubectl drain hit a PodDisruptionBudget,
// or a pod wouldn't terminate in time). MasterPool.each (pool.go) only
// ever fails over to the next master for the former. Confirmed live,
// 2026-10-11: before this distinction existed, a legitimately-failing
// 2-minute Drain call (real PDB violation on agent-2) got retried
// identically across all 3 masters — each one hitting the exact same
// real-world failure for its own full 2 minutes — turning an honest
// 2-minute failure into ~6, and the final "all masters unreachable"
// message was actively misleading: every master WAS reached and answered,
// correctly, with the same true failure.
type UnreachableError struct {
	err error
}

func (e *UnreachableError) Error() string { return e.err.Error() }
func (e *UnreachableError) Unwrap() error { return e.err }

func IsUnreachable(err error) bool {
	var ue *UnreachableError
	return errors.As(err, &ue)
}

// constantTimeTokenEqual is used server-side (cmd/node-agent) but kept here
// so both sides share one implementation of the comparison.
func ConstantTimeTokenEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func (r *Real) VipBootstrap(ctx context.Context, on bool) error {
	return r.do(ctx, "/vip/bootstrap", VipBootstrapRequest{On: on}, nil)
}

func (r *Real) ClusterProbe(ctx context.Context, vip string) (ClusterProbeResponse, error) {
	var resp ClusterProbeResponse
	err := r.do(ctx, "/cluster/probe", ClusterProbeRequest{VIP: vip}, &resp)
	return resp, err
}

func (r *Real) LonghornHealth(ctx context.Context) (LonghornHealthResponse, error) {
	var resp LonghornHealthResponse
	err := r.do(ctx, "/kubectl/longhorn-health", nil, &resp)
	return resp, err
}

func (r *Real) VaultSealed(ctx context.Context, namespace string) (VaultSealedResponse, error) {
	var resp VaultSealedResponse
	err := r.do(ctx, "/kubectl/vault-sealed", VaultSealedRequest{Namespace: namespace}, &resp)
	return resp, err
}
