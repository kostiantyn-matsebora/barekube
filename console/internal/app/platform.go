package app

import (
	"context"
	"fmt"
	"time"

	"github.com/kostiantyn-matsebora/barekube/console/internal/flows"
	"github.com/kostiantyn-matsebora/barekube/console/internal/nodeagent"
)

// The platform layer is what runs on the nodes and makes the cluster usable:
// the network (Cilium) and the API virtual IP (kube-vip). It is shown apart
// from the nodes, because a node can be Ready while the layer on it is down.

// platformTTL is how long a probe is reused. The page polls every second and
// a probe is several kubectl calls on a master.
const platformTTL = 5 * time.Second

// PlatformComponent is one thing the platform layer is made of.
type PlatformComponent struct {
	Name   string `json:"name"`
	State  string `json:"state"` // ok | partial | down | unknown
	Detail string `json:"detail"`
}

// PlatformView is the platform card and the platform part of the status bar.
type PlatformView struct {
	// Shown is false only when the console has read the cluster and found no
	// Cilium: there is then no platform layer to show. A cluster that cannot be
	// read yet is shown as unknown rather than hidden.
	Shown      bool                `json:"shown"`
	State      string              `json:"state"` // flows.Platform* or unknown
	Detail     string              `json:"detail"`
	VIP        string              `json:"vip,omitempty"`
	Components []PlatformComponent `json:"components"`
	CanRestore bool                `json:"canRestore"`
	Run        *flows.RunInfo      `json:"run,omitempty"`
	CheckedAt  *time.Time          `json:"checkedAt,omitempty"`
}

type platformCache struct {
	at   time.Time
	view PlatformView
	ok   bool
}

// buildPlatform turns one probe into the view. vip is the configured address.
func buildPlatform(vip string, p nodeagent.ClusterProbeResponse) PlatformView {
	state, detail := flows.PlatformState(vip, p)
	v := PlatformView{Shown: state != flows.PlatformAbsent, State: state, Detail: detail, VIP: vip, Components: []PlatformComponent{}}
	if !v.Shown {
		return v
	}
	cilium := PlatformComponent{Name: "Cilium", Detail: fmt.Sprintf("%d of %d agents ready", p.CiliumReadyCount, p.CiliumDesired)}
	switch {
	case p.CiliumReadyCount == 0:
		cilium.State = "down"
	case p.CiliumReadyCount < p.CiliumDesired:
		cilium.State = "partial"
	default:
		cilium.State = "ok"
	}
	if p.CiliumAPIHost != "" {
		cilium.Detail += ", reaches the API at " + p.CiliumAPIHost
	}
	v.Components = append(v.Components, cilium)

	if p.KubeVipPresent || vip != "" {
		kv := PlatformComponent{Name: "kube-vip"}
		switch {
		case vip == "":
			kv.State, kv.Detail = "unknown", "installed, no API VIP configured in the console"
		case p.VIPAnswers:
			kv.State, kv.Detail = "ok", "API VIP "+vip+" answers"
		default:
			kv.State, kv.Detail = "down", "API VIP "+vip+" does not answer"
		}
		if p.KubeVipLeader != "" {
			kv.Detail += "; lease held by " + p.KubeVipLeader
		}
		v.Components = append(v.Components, kv)
	}
	for _, c := range p.Packaged {
		v.Components = append(v.Components, packagedComponent(c))
	}
	return v
}

// Platform reads the platform layer, at most once per platformTTL. A cluster
// that cannot be read is unknown, never a guess in either direction.
func (s *Service) Platform(ctx context.Context) PlatformView {
	s.platMu.Lock()
	defer s.platMu.Unlock()
	now := s.clk.Now()
	if !s.plat.ok || now.Sub(s.plat.at) >= platformTTL {
		vip := s.cfg.Platform.VIP
		view := PlatformView{Shown: true, State: "unknown", VIP: vip, Components: []PlatformComponent{},
			Detail: "the cluster cannot be read right now"}
		if p, err := s.ops.ClusterProbe(ctx, vip); err == nil {
			view = buildPlatform(vip, p)
		}
		t := now
		view.CheckedAt = &t
		s.plat = platformCache{at: now, view: view, ok: true}
	}
	v := s.plat.view
	// The run is not cached: a restore in progress must show at once.
	v.Run = nil
	v.CanRestore = s.cfg.Platform.VIP != ""
	for _, r := range s.Engine.Runs() {
		if r.Workflow == flows.WfVipRestore {
			rr := r
			v.Run = &rr
			if !r.Terminal {
				v.CanRestore = false
			}
			break
		}
	}
	return v
}

// packagedComponent turns an optional k3s add-on into a row. One that is not
// installed is shown as such, in the "off" state, and never counts against the
// platform; one scaled to zero is off too, on purpose.
func packagedComponent(c nodeagent.PackagedComponent) PlatformComponent {
	row := PlatformComponent{Name: c.Name}
	switch {
	case !c.Present:
		row.State, row.Detail = "off", "not installed"
	case c.Desired == 0:
		row.State, row.Detail = "off", "installed, scaled to 0"
	case c.Ready == 0:
		row.State, row.Detail = "down", fmt.Sprintf("0 of %d ready", c.Desired)
	case c.Ready < c.Desired:
		row.State, row.Detail = "partial", fmt.Sprintf("%d of %d ready", c.Ready, c.Desired)
	default:
		row.State, row.Detail = "ok", fmt.Sprintf("%d of %d ready", c.Ready, c.Desired)
	}
	return row
}
