package flows

// St is a workflow status. Zero is reserved: a handler returning St(0) with a
// nil error means "stay where you are".
type St int

const skip St = 0

const (
	Started St = iota + 1
	Refused
	Cordoning
	Draining
	RebootRequested
	AwaitingNodeDown
	AwaitingNodeUp
	Uncordoning
	AwaitingUncordoned
	RollingBack
	Completed
	Failed
	StoppingK3s
	AwaitingK3sInactive
	StoodBy
	AwaitingClearance
	PowerOffRequested
	PoweredOff
	ForcingOff
	AwaitingForcedDown
	StartingK3s
	AwaitingK3sActive
	AwaitingNodeReady
	CordoningAll
	StandingByAgents
	AwaitingAgentsStoodBy
	PoweringOffMasters
	AwaitingMastersDrained
	ReleasingNextMaster
	AwaitingMasterDown
	SnapshottingEtcd
	// Cluster start. Appended after the rest: a status is stored by number.
	AwaitingNodes
	AwaitingQuorum
	CheckingVip
	BreakingDeadlock
	AwaitingVip
	ReleasingVip
	AwaitingVipStable
	RejoiningMasters
	AwaitingMasterRejoined
	RejoiningAgents
	AwaitingAgentsRejoined
	CheckingHealth
)

var stNames = map[St]string{
	Started: "Started", Refused: "Refused", Cordoning: "Cordoning", Draining: "Draining",
	RebootRequested: "RebootRequested", AwaitingNodeDown: "AwaitingNodeDown", AwaitingNodeUp: "AwaitingNodeUp",
	Uncordoning: "Uncordoning", AwaitingUncordoned: "AwaitingUncordoned", RollingBack: "RollingBack",
	Completed: "Completed", Failed: "Failed", StoppingK3s: "StoppingK3s", AwaitingK3sInactive: "AwaitingK3sInactive",
	StoodBy: "StoodBy", AwaitingClearance: "AwaitingClearance", PowerOffRequested: "PowerOffRequested",
	PoweredOff: "PoweredOff", ForcingOff: "ForcingOff", AwaitingForcedDown: "AwaitingForcedDown",
	StartingK3s: "StartingK3s", AwaitingK3sActive: "AwaitingK3sActive", AwaitingNodeReady: "AwaitingNodeReady",
	CordoningAll: "CordoningAll", StandingByAgents: "StandingByAgents", AwaitingAgentsStoodBy: "AwaitingAgentsStoodBy",
	PoweringOffMasters: "PoweringOffMasters", AwaitingMastersDrained: "AwaitingMastersDrained",
	ReleasingNextMaster: "ReleasingNextMaster", AwaitingMasterDown: "AwaitingMasterDown", SnapshottingEtcd: "SnapshottingEtcd",
	AwaitingNodes: "AwaitingNodes", AwaitingQuorum: "AwaitingQuorum", CheckingVip: "CheckingVip",
	BreakingDeadlock: "BreakingDeadlock", AwaitingVip: "AwaitingVip", ReleasingVip: "ReleasingVip",
	AwaitingVipStable: "AwaitingVipStable", RejoiningMasters: "RejoiningMasters",
	AwaitingMasterRejoined: "AwaitingMasterRejoined", RejoiningAgents: "RejoiningAgents",
	AwaitingAgentsRejoined: "AwaitingAgentsRejoined", CheckingHealth: "CheckingHealth",
}

func (s St) String() string {
	if n, ok := stNames[s]; ok {
		return n
	}
	return "Unknown"
}

// finalKind classifies a terminal status for events and styling.
func finalKind(s St) string {
	switch s {
	case Completed, StoodBy, PoweredOff:
		return "completed"
	case Refused:
		return "refused"
	}
	return "failed"
}
