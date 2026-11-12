package agentd

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/kostiantyn-matsebora/barekube/console/internal/nodeagent"
)

const ciliumDS = `{"spec":{"template":{"spec":{"containers":[{"env":[{"name":"KUBERNETES_SERVICE_HOST","value":"192.168.1.100"}]}]}}},
 "status":{"desiredNumberScheduled":5,"numberReady":0}}`

const leaseJSON = `{"spec":{"holderIdentity":"master-2","renewTime":"2026-11-08T19:10:00.123456Z"}}`

func TestVipBootstrapStartsAndStopsOnlyTheFixedUnit(t *testing.T) {
	_, sc, h := setup(t)
	if w := call(h, "/vip/bootstrap", `{"on":true}`, true); w.Code != 200 {
		t.Fatalf("%d", w.Code)
	}
	if w := call(h, "/vip/bootstrap", `{"on":false}`, true); w.Code != 200 {
		t.Fatalf("%d", w.Code)
	}
	if !sc.did("systemctl start barekube-vip-bootstrap.service") || !sc.did("systemctl stop barekube-vip-bootstrap.service") {
		t.Fatalf("%v", sc.ran)
	}
	sc.fail["systemctl start"] = true // polkit denied, as on an agent
	if w := call(h, "/vip/bootstrap", `{"on":true}`, true); w.Code != 500 {
		t.Fatal("a denied start must surface")
	}
	if w := call(h, "/vip/bootstrap", `not json`, true); w.Code != 400 {
		t.Fatal("bad body")
	}
}

func TestClusterProbeReadsTheDeadlock(t *testing.T) {
	s, sc, h := setup(t)
	s.dial = func(context.Context, string) bool { return false }
	sc.out["k3s kubectl get daemonset cilium"] = ciliumDS
	sc.out["k3s kubectl get daemonset kube-vip"] = `{"status":{}}`
	sc.out["k3s kubectl get deployments.apps"] = `{"items":[
	  {"metadata":{"name":"coredns"},"spec":{"replicas":2},"status":{"readyReplicas":1}},
	  {"metadata":{"name":"cilium-operator"},"spec":{"replicas":2},"status":{"readyReplicas":2}},
	  {"metadata":{"name":"local-path-provisioner"},"status":{"readyReplicas":1}}]}`
	sc.out["k3s kubectl get daemonsets.apps"] = `{"items":[
	  {"metadata":{"name":"cilium"},"status":{"desiredNumberScheduled":5,"numberReady":0}},
	  {"metadata":{"name":"svclb-a-1"},"status":{"desiredNumberScheduled":3,"numberReady":3}},
	  {"metadata":{"name":"svclb-b-2"},"status":{"desiredNumberScheduled":3,"numberReady":2}}]}`
	sc.out["k3s kubectl get leases.coordination.k8s.io plndr-cp-lock"] = leaseJSON

	var got nodeagent.ClusterProbeResponse
	w := call(h, "/cluster/probe", `{"vip":"192.168.1.100"}`, true)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	want := nodeagent.ClusterProbeResponse{CiliumPresent: true, CiliumDesired: 5, CiliumAPIHost: "192.168.1.100", KubeVipPresent: true,
		KubeVipLeader: "master-2", KubeVipRenewed: "2026-11-08T19:10:00.123456Z"}
	wantPackaged := []nodeagent.PackagedComponent{
		{Name: "CoreDNS", Present: true, Ready: 1, Desired: 2},
		{Name: "Traefik"}, // disabled: reported absent, not an error
		{Name: "metrics-server"},
		{Name: "local-path-provisioner", Present: true, Ready: 1, Desired: 1},
		{Name: "servicelb", Present: true, Ready: 5, Desired: 6}, // every svclb-* DaemonSet, summed
	}
	if !reflect.DeepEqual(got.Packaged, wantPackaged) {
		t.Fatalf("packaged: got %+v want %+v", got.Packaged, wantPackaged)
	}
	got.Packaged = nil
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

func TestClusterProbeReadyAndVipAnswering(t *testing.T) {
	s, sc, h := setup(t)
	var dialled string
	s.dial = func(_ context.Context, addr string) bool { dialled = addr; return true }
	sc.out["k3s kubectl get daemonset cilium"] = `{"status":{"desiredNumberScheduled":5,"numberReady":5}}`
	sc.out["k3s kubectl get configmap cilium-config"] = "127.0.0.1\n" // no env on the DaemonSet: fall back to the config map
	sc.failMsg["k3s kubectl get daemonset kube-vip"] = `Error from server (NotFound): daemonsets.apps "kube-vip" not found`
	sc.out["k3s kubectl get deployments.apps"] = `{"items":[]}`
	sc.out["k3s kubectl get daemonsets.apps"] = `{"items":[]}`

	var got nodeagent.ClusterProbeResponse
	_ = json.Unmarshal(call(h, "/cluster/probe", `{"vip":"192.168.1.100"}`, true).Body.Bytes(), &got)
	if dialled != "192.168.1.100:6443" || !got.VIPAnswers || !got.CiliumReady || got.CiliumReadyCount != 5 || got.CiliumAPIHost != "127.0.0.1" || got.KubeVipPresent {
		t.Fatalf("dialled %q got %+v", dialled, got)
	}
}

func TestClusterProbeTellsAbsentFromUnreadable(t *testing.T) {
	s, sc, h := setup(t)
	s.dial = func(context.Context, string) bool { return false }
	sc.failMsg["k3s kubectl get daemonset cilium"] = `Error from server (NotFound): daemonsets.apps "cilium" not found`
	sc.failMsg["k3s kubectl get daemonset kube-vip"] = `Error from server (NotFound): not found`
	sc.out["k3s kubectl get deployments.apps"] = `{"items":[]}`
	sc.out["k3s kubectl get daemonsets.apps"] = `{"items":[]}`
	var got nodeagent.ClusterProbeResponse
	w := call(h, "/cluster/probe", `{"vip":"192.168.1.100"}`, true)
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != 200 || got.CiliumPresent || got.KubeVipPresent {
		t.Fatalf("absent is an answer: %d %+v", w.Code, got)
	}
	// An API that cannot be asked is an error, never "not present".
	sc.failMsg["k3s kubectl get daemonset cilium"] = "The connection to the server was refused"
	if w := call(h, "/cluster/probe", `{"vip":"192.168.1.100"}`, true); w.Code != 500 {
		t.Fatalf("unreadable must be 500, got %d", w.Code)
	}
}

func TestClusterProbeRejectsAnythingButAnIPv4(t *testing.T) {
	_, sc, h := setup(t)
	for _, bad := range []string{`{"vip":"example.com"}`, `{"vip":"::1"}`, `{"vip":"1.2.3.4; reboot"}`, `{"vip":"192.168.1.100:6443"}`} {
		if w := call(h, "/cluster/probe", bad, true); w.Code != 400 {
			t.Errorf("%s answered %d", bad, w.Code)
		}
	}
	if len(sc.ran) != 0 {
		t.Fatalf("a rejected VIP must not reach a command: %v", sc.ran)
	}
}

func TestLonghornHealthCountsRobustness(t *testing.T) {
	_, sc, h := setup(t)
	sc.out["k3s kubectl get volumes.longhorn.io"] = `{"items":[
	 {"status":{"robustness":"healthy"}},{"status":{"robustness":"degraded"}},
	 {"status":{"robustness":"degraded"}},{"status":{"robustness":"faulty"}},{"status":{"robustness":"unknown"}}]}`
	var got nodeagent.LonghornHealthResponse
	_ = json.Unmarshal(call(h, "/kubectl/longhorn-health", "", true).Body.Bytes(), &got)
	if got != (nodeagent.LonghornHealthResponse{Available: true, Total: 5, Degraded: 2, Faulty: 1}) {
		t.Fatalf("%+v", got)
	}
	sc.fail["k3s kubectl get crd"] = true
	_ = json.Unmarshal(call(h, "/kubectl/longhorn-health", "", true).Body.Bytes(), &got)
	if got.Available {
		t.Fatal("no longhorn is not an error")
	}
}

func TestVaultSealedReadsTheLabel(t *testing.T) {
	_, sc, h := setup(t)
	sc.out["k3s kubectl get pods -n vault -l vault-sealed"] = `{"items":[
	 {"metadata":{"name":"vault-0","labels":{"vault-sealed":"true"}}},
	 {"metadata":{"name":"vault-1","labels":{"vault-sealed":"false"}}}]}`
	var got nodeagent.VaultSealedResponse
	_ = json.Unmarshal(call(h, "/kubectl/vault-sealed", `{"namespace":"vault"}`, true).Body.Bytes(), &got)
	if got.Found != 2 || len(got.Sealed) != 1 || got.Sealed[0] != "vault-0" {
		t.Fatalf("%+v", got)
	}
	for _, bad := range []string{`{"namespace":""}`, `{"namespace":"a b"}`, `{"namespace":"-o yaml"}`, `{"namespace":"VAULT"}`} {
		if w := call(h, "/kubectl/vault-sealed", bad, true); w.Code != 400 {
			t.Errorf("%s answered %d", bad, w.Code)
		}
	}
	if strings.Contains(strings.Join(sc.ran, "\n"), "yaml") {
		t.Fatal("a rejected namespace reached kubectl")
	}
}

// A cluster with Cilium but no VIP the caller knows of: read the platform, try no address.
func TestClusterProbeWithoutAVipDialsNothing(t *testing.T) {
	s, sc, h := setup(t)
	dialled := false
	s.dial = func(context.Context, string) bool { dialled = true; return true }
	sc.out["k3s kubectl get daemonset cilium"] = `{"status":{"desiredNumberScheduled":2,"numberReady":2}}`
	sc.failMsg["k3s kubectl get daemonset kube-vip"] = "Error from server (NotFound): not found"
	sc.out["k3s kubectl get deployments.apps"] = `{"items":[]}`
	sc.out["k3s kubectl get daemonsets.apps"] = `{"items":[]}`
	var got nodeagent.ClusterProbeResponse
	w := call(h, "/cluster/probe", `{"vip":""}`, true)
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != 200 || dialled || got.VIPAnswers || !got.CiliumReady {
		t.Fatalf("%d dialled=%v %+v", w.Code, dialled, got)
	}
}
