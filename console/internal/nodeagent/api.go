// Package nodeagent is the client (and shared wire types) for a small,
// purpose-built daemon that runs on each node (cmd/node-agent), replacing
// Cockpit entirely. It exposes a fixed, narrow set of endpoints — never a
// generic "run this command" — each running one specific, parameterized
// action. The daemon runs under the unprivileged barekube-agent system
// account (systemd User=), so its OS-level privilege is fixed at deploy
// time and is never part of the network protocol: authentication is a
// single bearer token checked by the daemon itself, with no PAM/session/
// account-mapping involved at all. See console/docs/workflows.md.
package nodeagent

import "time"

// Every request/response pair below corresponds 1:1 with an endpoint path
// on the daemon (see cmd/node-agent/main.go). Node names are validated
// server-side against a strict pattern before ever reaching a subprocess
// argv — defense in depth on top of exec.Command's argv-array form already
// not being shell-interpretable.

type CordonRequest struct {
	Node string `json:"node"`
}

type DrainRequest struct {
	Node               string `json:"node"`
	IgnoreDaemonSets   bool   `json:"ignoreDaemonSets"`
	DeleteEmptyDirData bool   `json:"deleteEmptyDirData"`
	TimeoutSeconds     int    `json:"timeoutSeconds"`
}

type NodeReadyRequest struct {
	Node string `json:"node"`
}

type NodeReadyResponse struct {
	Ready bool `json:"ready"`
}

type LonghornSchedulingRequest struct {
	Node  string `json:"node"`
	Allow bool   `json:"allow"`
}

type ZeroReplicaRiskRequest struct {
	Node string `json:"node"`
}

type ZeroReplicaRiskResponse struct {
	Volumes []string `json:"volumes"`
}

type LonghornAvailableResponse struct {
	Available bool `json:"available"`
}

type ReadyzResponse struct {
	OK bool `json:"ok"`
}

type K3sStatusResponse struct {
	Active bool `json:"active"`
}

type CordonedRequest struct {
	Node string `json:"node"`
}

type CordonedResponse struct {
	Cordoned bool `json:"cordoned"`
}

type PowerOffRequest struct {
	// Force selects `systemctl poweroff --force` over the plain form —
	// skips systemd's normal unit-stop ordering/timeouts and force-
	// unmounts immediately, rather than waiting on a service (or, as
	// found testing against the real master-2, a slow/wearing disk
	// during unmount) that may never finish on its own. See
	// console/docs/workflows.md, Phase 2 decisions.
	Force bool `json:"force"`
}

// VipBootstrapRequest starts or stops the node's fixed vip-bootstrap systemd
// unit. It carries no address and no interface: both are baked into the unit at
// install time, so a caller cannot make a node announce anything else.
type VipBootstrapRequest struct {
	On bool `json:"on"`
}

// ClusterProbeRequest asks a master what it sees of the API VIP deadlock. VIP
// is the address under test; the daemon validates it as a plain IPv4 address.
type ClusterProbeRequest struct {
	VIP string `json:"vip"`
}

// ClusterProbeResponse is what a master can read about Cilium and kube-vip.
// Every field is an observation. Nothing here is inferred by the daemon.
// PackagedComponent is one optional k3s add-on, with its replica counts.
type PackagedComponent struct {
	Name    string `json:"name"`
	Present bool   `json:"present"`
	Ready   int    `json:"ready"`
	Desired int    `json:"desired"`
}

type ClusterProbeResponse struct {
	// VIPAnswers is a TCP connect to VIP:6443 made from this master.
	VIPAnswers bool `json:"vipAnswers"`
	// CiliumPresent is true when the cilium DaemonSet exists in kube-system.
	CiliumPresent bool `json:"ciliumPresent"`
	// CiliumReady is true when every scheduled cilium pod is ready. The ready
	// and desired counts are kept apart because the DaemonSet also covers the
	// agents, which are not ready until they rejoin: "none ready" is the
	// deadlock, "all ready" is not a thing to wait for.
	CiliumReady      bool `json:"ciliumReady"`
	CiliumReadyCount int  `json:"ciliumReadyCount"`
	CiliumDesired    int  `json:"ciliumDesired"`
	// Packaged lists the components k3s can install itself (CoreDNS, Traefik,
	// metrics-server, local-path-provisioner, servicelb). Each is optional: one
	// that is disabled is reported with Present false, and is not a fault.
	Packaged []PackagedComponent `json:"packaged,omitempty"`
	// CiliumAPIHost is the API server host Cilium is configured to use
	// (KUBERNETES_SERVICE_HOST on the DaemonSet, else k8s-service-host in
	// cilium-config). Empty when neither is set.
	CiliumAPIHost string `json:"ciliumApiHost,omitempty"`
	// KubeVipPresent is true when a kube-vip DaemonSet exists in kube-system.
	KubeVipPresent bool `json:"kubeVipPresent"`
	// KubeVipLeader is the holder of the kube-vip control-plane lease, and
	// KubeVipRenewed that lease's last renewal, verbatim. A lease left over
	// from before the shutdown still names a holder, so the renewal time is
	// what tells a live holder from a stale one: it changes when kube-vip runs.
	KubeVipLeader  string `json:"kubeVipLeader,omitempty"`
	KubeVipRenewed string `json:"kubeVipRenewed,omitempty"`
}

// LonghornHealthResponse counts Longhorn volumes by robustness.
type LonghornHealthResponse struct {
	Available bool `json:"available"`
	Total     int  `json:"total"`
	Degraded  int  `json:"degraded"`
	Faulty    int  `json:"faulty"`
}

// VaultSealedRequest names the namespace Vault runs in.
type VaultSealedRequest struct {
	Namespace string `json:"namespace"`
}

// VaultSealedResponse lists the Vault pods that report themselves sealed,
// from the vault-sealed label Vault sets on its own pods.
type VaultSealedResponse struct {
	Found  int      `json:"found"`
	Sealed []string `json:"sealed,omitempty"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}

// HeartbeatRequest is the one request this package's Client interface does
// NOT send — it's pushed the other way, node-agent to console, on its own
// timer (-heartbeat-interval, cmd/node-agent, default 5s), instead of
// console having to poll for it. See console/docs/workflows.md
// Phase 2.1: before this, a node that died or came back outside of any
// orchestrator-driven operation left Status exactly as it was last set —
// an assumption, not an observation. Authenticated with the SAME shared
// node token used for the opposite direction (console -> node-agent); no
// new secret, just a new use of the existing one.
type HeartbeatRequest struct {
	Node string `json:"node"`
	// Timestamp is when the node-agent sampled K3sActive, not when console
	// received it — console still uses its own receive-time for staleness
	// checks (clock skew between 5 machines isn't worth reasoning about),
	// but the sample time is kept for display/debugging.
	Timestamp time.Time `json:"timestamp"`
	K3sActive bool      `json:"k3sActive"`
}

// HeartbeatResponse echoes back what console's state machine currently
// believes about the node that just reported in — the node pushes what it
// locally knows, console replies with its own, possibly very different,
// view (e.g. mid-Stand-By, cordoned, faulted for an unrelated reason). The
// node-agent only logs this today; it's the hook a later phase needs
// instead of adding a second request type.
type HeartbeatResponse struct {
	State   string `json:"state"`
	Message string `json:"message,omitempty"`
}
