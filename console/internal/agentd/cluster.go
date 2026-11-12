package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/kostiantyn-matsebora/barekube/console/internal/nodeagent"
)

// vipUnit is the one systemd unit this daemon may start for the API VIP. It is
// installed by `barekube install node-agent` with the address and the
// interface baked in, and polkit allows start/stop of this unit only. Nothing
// about the address crosses the network, so a caller cannot make a node
// announce any other address.
const vipUnit = "barekube-vip-bootstrap.service"

var errInvalidVIP = errors.New("vip must be a plain IPv4 address")
var errInvalidNamespace = errors.New("invalid namespace")

// dialTCP reports whether a TCP connection to addr opens within two seconds.
func dialTCP(ctx context.Context, addr string) bool {
	d := net.Dialer{Timeout: 2 * time.Second}
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

func (s *Server) handleVipBootstrap(w http.ResponseWriter, r *http.Request) {
	var req nodeagent.VipBootstrapRequest
	if !decode(w, r, &req) {
		return
	}
	verb := "stop"
	if req.On {
		verb = "start"
	}
	if out, err := s.run(r.Context(), "systemctl", verb, vipUnit); err != nil {
		writeError(w, http.StatusInternalServerError, &kubectlError{out: string(out), err: err})
		return
	}
	w.WriteHeader(http.StatusOK)
}

type envVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type podContainer struct {
	Env []envVar `json:"env"`
}

type daemonSet struct {
	Spec struct {
		Template struct {
			Spec struct {
				Containers     []podContainer `json:"containers"`
				InitContainers []podContainer `json:"initContainers"`
			} `json:"spec"`
		} `json:"template"`
	} `json:"spec"`
	Status struct {
		DesiredNumberScheduled int `json:"desiredNumberScheduled"`
		NumberReady            int `json:"numberReady"`
	} `json:"status"`
}

// getDaemonSet reads one kube-system DaemonSet. A DaemonSet that does not exist
// is an answer (found false), any other failure is an error: the caller must
// not take "could not ask" for "does not exist".
func (s *Server) getDaemonSet(ctx context.Context, name string) (ds daemonSet, found bool, err error) {
	out, err := s.kubectl(ctx, "get", "daemonset", name, "-n", "kube-system", "-o", "json")
	if err != nil {
		if strings.Contains(err.Error(), "NotFound") {
			return ds, false, nil
		}
		return ds, false, err
	}
	if err := json.Unmarshal([]byte(out), &ds); err != nil {
		return ds, false, err
	}
	return ds, true, nil
}

// packagedDeployments are the k3s add-ons that run as a Deployment in
// kube-system. servicelb is separate: it is one DaemonSet per LoadBalancer
// Service, named svclb-*.
var packagedDeployments = []struct{ name, label string }{
	{"coredns", "CoreDNS"},
	{"traefik", "Traefik"},
	{"metrics-server", "metrics-server"},
	{"local-path-provisioner", "local-path-provisioner"},
}

// getPackaged reads the optional k3s add-ons with two list calls. An add-on
// that is not there is an answer (Present false); a list that cannot be read is
// an error, never "not installed".
func (s *Server) getPackaged(ctx context.Context) ([]nodeagent.PackagedComponent, error) {
	type item struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
		Spec struct {
			Replicas *int `json:"replicas"`
		} `json:"spec"`
		Status struct {
			ReadyReplicas          int `json:"readyReplicas"`
			NumberReady            int `json:"numberReady"`
			DesiredNumberScheduled int `json:"desiredNumberScheduled"`
		} `json:"status"`
	}
	var deps, dss struct {
		Items []item `json:"items"`
	}
	for _, c := range []struct {
		kind string
		into any
	}{{"deployments.apps", &deps}, {"daemonsets.apps", &dss}} {
		out, err := s.kubectl(ctx, "get", c.kind, "-n", "kube-system", "-o", "json")
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(out), c.into); err != nil {
			return nil, err
		}
	}
	var res []nodeagent.PackagedComponent
	for _, want := range packagedDeployments {
		c := nodeagent.PackagedComponent{Name: want.label}
		for _, d := range deps.Items {
			if d.Metadata.Name == want.name {
				c.Present, c.Ready, c.Desired = true, d.Status.ReadyReplicas, 1 // spec.replicas unset = 1
				if d.Spec.Replicas != nil {
					c.Desired = *d.Spec.Replicas
				}
			}
		}
		res = append(res, c)
	}
	lb := nodeagent.PackagedComponent{Name: "servicelb"}
	for _, d := range dss.Items {
		if strings.HasPrefix(d.Metadata.Name, "svclb-") {
			lb.Present = true
			lb.Ready += d.Status.NumberReady
			lb.Desired += d.Status.DesiredNumberScheduled
		}
	}
	return append(res, lb), nil
}

func (ds daemonSet) env(name string) string {
	all := append(append([]podContainer{}, ds.Spec.Template.Spec.Containers...), ds.Spec.Template.Spec.InitContainers...)
	for _, c := range all {
		for _, e := range c.Env {
			if e.Name == name && e.Value != "" {
				return e.Value
			}
		}
	}
	return ""
}

// handleClusterProbe reads the facts the cold start needs to tell the API VIP
// deadlock from a slow normal boot. It only reads.
func (s *Server) handleClusterProbe(w http.ResponseWriter, r *http.Request) {
	var req nodeagent.ClusterProbeRequest
	if !decode(w, r, &req) {
		return
	}
	ctx := r.Context()
	resp := nodeagent.ClusterProbeResponse{}
	// An empty VIP means the caller knows of none: Cilium and kube-vip are still
	// read, there is just no address to try. Anything else must be plain IPv4.
	if req.VIP != "" {
		ip := net.ParseIP(req.VIP)
		if ip == nil || ip.To4() == nil {
			writeError(w, http.StatusBadRequest, errInvalidVIP)
			return
		}
		resp.VIPAnswers = s.dial(ctx, net.JoinHostPort(ip.String(), "6443"))
	}

	cilium, found, err := s.getDaemonSet(ctx, "cilium")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if found {
		resp.CiliumPresent = true
		resp.CiliumReadyCount, resp.CiliumDesired = cilium.Status.NumberReady, cilium.Status.DesiredNumberScheduled
		resp.CiliumReady = resp.CiliumDesired > 0 && resp.CiliumReadyCount == resp.CiliumDesired
		resp.CiliumAPIHost = cilium.env("KUBERNETES_SERVICE_HOST")
		if resp.CiliumAPIHost == "" {
			if out, err := s.kubectl(ctx, "get", "configmap", "cilium-config", "-n", "kube-system", "-o", "jsonpath={.data.k8s-service-host}"); err == nil {
				resp.CiliumAPIHost = strings.TrimSpace(out)
			}
		}
	}

	packaged, err := s.getPackaged(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	resp.Packaged = packaged

	_, vipFound, err := s.getDaemonSet(ctx, "kube-vip")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	resp.KubeVipPresent = vipFound
	// A missing lease is normal before kube-vip ever ran, so its errors are not
	// failures of the probe.
	if out, err := s.kubectl(ctx, "get", "leases.coordination.k8s.io", "plndr-cp-lock", "-n", "kube-system", "-o", "json"); err == nil {
		var lease struct {
			Spec struct {
				HolderIdentity string `json:"holderIdentity"`
				RenewTime      string `json:"renewTime"`
			} `json:"spec"`
		}
		if json.Unmarshal([]byte(out), &lease) == nil {
			resp.KubeVipLeader, resp.KubeVipRenewed = lease.Spec.HolderIdentity, lease.Spec.RenewTime
		}
	}
	writeJSON(w, resp)
}

type kubeList struct {
	Items []struct {
		Metadata struct {
			Name   string            `json:"name"`
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
		Status struct {
			Robustness string `json:"robustness"`
		} `json:"status"`
	} `json:"items"`
}

func (s *Server) handleLonghornHealth(w http.ResponseWriter, r *http.Request) {
	if !s.longhornAvailable(r.Context()) {
		writeJSON(w, nodeagent.LonghornHealthResponse{})
		return
	}
	out, err := s.kubectl(r.Context(), "get", "volumes.longhorn.io", "-n", "longhorn-system", "-o", "json")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	var list kubeList
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	resp := nodeagent.LonghornHealthResponse{Available: true, Total: len(list.Items)}
	for _, v := range list.Items {
		switch v.Status.Robustness {
		case "degraded":
			resp.Degraded++
		case "faulty":
			resp.Faulty++
		}
	}
	writeJSON(w, resp)
}

// handleVaultSealed reads the vault-sealed label Vault puts on its own pods.
func (s *Server) handleVaultSealed(w http.ResponseWriter, r *http.Request) {
	var req nodeagent.VaultSealedRequest
	if !decode(w, r, &req) {
		return
	}
	if !nodeNamePattern.MatchString(req.Namespace) {
		writeError(w, http.StatusBadRequest, errInvalidNamespace)
		return
	}
	out, err := s.kubectl(r.Context(), "get", "pods", "-n", req.Namespace, "-l", "vault-sealed", "-o", "json")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	var list kubeList
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	resp := nodeagent.VaultSealedResponse{Found: len(list.Items)}
	for _, p := range list.Items {
		if p.Metadata.Labels["vault-sealed"] == "true" {
			resp.Sealed = append(resp.Sealed, p.Metadata.Name)
		}
	}
	writeJSON(w, resp)
}
