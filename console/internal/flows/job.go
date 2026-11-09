package flows

import "time"

// Mode is how hard an operation pushes.
type Mode string

const (
	ModeGraceful Mode = "graceful"
	ModeForce    Mode = "force"
)

// Job is the object every workflow run carries. Node workflows use the node
// fields, the cluster workflow the cluster fields.
type Job struct {
	Node       string        `json:"node,omitempty"`
	Role       string        `json:"role,omitempty"`
	Mode       Mode          `json:"mode,omitempty"`
	Cluster    bool          `json:"cluster,omitempty"`
	Parent     string        `json:"parent,omitempty"`
	ForceAfter time.Duration `json:"forceAfter,omitempty"`
	By         string        `json:"by,omitempty"`
	Reason     string        `json:"reason,omitempty"`

	Leader   string   `json:"leader,omitempty"`
	Agents   []string `json:"agents,omitempty"`
	Masters  []string `json:"masters,omitempty"`
	Released []string `json:"released,omitempty"`
	Current  string   `json:"current,omitempty"`

	// Cluster start. Bootstrap is the master that holds the API VIP by hand,
	// LeaseSeen the kube-vip lease renewal seen before it did (a renewal that
	// differs proves kube-vip ran since), VipLeader who holds the lease once it
	// does. Failures collects the nodes that did not rejoin.
	Bootstrap string   `json:"bootstrap,omitempty"`
	LeaseSeen string   `json:"leaseSeen,omitempty"`
	VipLeader string   `json:"vipLeader,omitempty"`
	Failures  []string `json:"failures,omitempty"`
}
