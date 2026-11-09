package nodeagent

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeDrainer is a minimal Client stand-in for exercising MasterPool.each
// through Drain — only Drain is wired up, every other method panics if
// called, so a test calling the wrong one fails loudly instead of silently
// passing.
type fakeDrainer struct {
	err   error
	calls *int
}

func (f *fakeDrainer) Drain(ctx context.Context, node string, ignoreDaemonSets, deleteEmptyDirData bool, timeout time.Duration) error {
	*f.calls++
	return f.err
}

func (f *fakeDrainer) Reboot(ctx context.Context) error            { panic("not used") }
func (f *fakeDrainer) Reachable(ctx context.Context) bool          { panic("not used") }
func (f *fakeDrainer) StopK3s(ctx context.Context) error           { panic("not used") }
func (f *fakeDrainer) KillK3s(ctx context.Context) error           { panic("not used") }
func (f *fakeDrainer) StartK3s(ctx context.Context) error          { panic("not used") }
func (f *fakeDrainer) K3sActive(ctx context.Context) (bool, error) { panic("not used") }
func (f *fakeDrainer) PowerOff(ctx context.Context, force bool) error {
	panic("not used")
}
func (f *fakeDrainer) Cordon(ctx context.Context, node string) error   { panic("not used") }
func (f *fakeDrainer) Uncordon(ctx context.Context, node string) error { panic("not used") }
func (f *fakeDrainer) Cordoned(ctx context.Context, node string) (bool, error) {
	panic("not used")
}
func (f *fakeDrainer) NodeReady(ctx context.Context, node string) (bool, error) {
	panic("not used")
}
func (f *fakeDrainer) SetLonghornScheduling(ctx context.Context, node string, allow bool) error {
	panic("not used")
}
func (f *fakeDrainer) ZeroReplicaRisk(ctx context.Context, node string) ([]string, error) {
	panic("not used")
}
func (f *fakeDrainer) LonghornAvailable(ctx context.Context) (bool, error) {
	panic("not used")
}
func (f *fakeDrainer) EtcdReadyz(ctx context.Context) bool             { panic("not used") }
func (f *fakeDrainer) EtcdSnapshot(ctx context.Context) error          { panic("not used") }
func (f *fakeDrainer) EtcdIsLeader(ctx context.Context) (bool, error)  { panic("not used") }
func (f *fakeDrainer) VipBootstrap(ctx context.Context, on bool) error { panic("not used") }
func (f *fakeDrainer) ClusterProbe(ctx context.Context, vip string) (ClusterProbeResponse, error) {
	panic("not used")
}
func (f *fakeDrainer) LonghornHealth(ctx context.Context) (LonghornHealthResponse, error) {
	panic("not used")
}
func (f *fakeDrainer) VaultSealed(ctx context.Context, ns string) (VaultSealedResponse, error) {
	panic("not used")
}

func TestMasterPoolDoesNotRetryAnAuthoritativeFailure(t *testing.T) {
	// Regression test for the live incident, 2026-10-11: a real drain
	// failure (PodDisruptionBudget violation) on master-1 must NOT be
	// retried on master-2/master-3 — that would just repeat the same
	// real-world failure 3x, turning one honest 2-minute timeout into ~6.
	calls1, calls2, calls3 := 0, 0, 0
	realErr := errors.New(`exit status 1: Cannot evict pod as it would violate the pod's disruption budget`)
	pool := &MasterPool{Masters: []Client{
		&fakeDrainer{err: realErr, calls: &calls1},
		&fakeDrainer{err: nil, calls: &calls2},
		&fakeDrainer{err: nil, calls: &calls3},
	}}

	err := pool.Drain(context.Background(), "agent-2", true, true, time.Minute)
	if err == nil {
		t.Fatal("expected the real drain error to propagate")
	}
	if !errors.Is(err, realErr) {
		t.Fatalf("expected the original error to propagate unwrapped, got %v", err)
	}
	if calls1 != 1 {
		t.Fatalf("expected exactly 1 call to the failing master, got %d", calls1)
	}
	if calls2 != 0 || calls3 != 0 {
		t.Fatalf("must NOT fail over to other masters for a non-unreachable error, got calls2=%d calls3=%d", calls2, calls3)
	}
}

func TestMasterPoolFailsOverOnGenuineUnreachability(t *testing.T) {
	calls1, calls2 := 0, 0
	pool := &MasterPool{Masters: []Client{
		&fakeDrainer{err: &UnreachableError{err: errors.New("dial tcp: connection refused")}, calls: &calls1},
		&fakeDrainer{err: nil, calls: &calls2},
	}}

	err := pool.Drain(context.Background(), "agent-2", true, true, time.Minute)
	if err != nil {
		t.Fatalf("expected the second, reachable master to succeed, got %v", err)
	}
	if calls1 != 1 || calls2 != 1 {
		t.Fatalf("expected both masters to be tried once each, got calls1=%d calls2=%d", calls1, calls2)
	}
}

func TestMasterPoolAllUnreachable(t *testing.T) {
	pool := &MasterPool{Masters: []Client{
		&fakeDrainer{err: &UnreachableError{err: errors.New("timeout")}, calls: new(int)},
		&fakeDrainer{err: &UnreachableError{err: errors.New("timeout")}, calls: new(int)},
	}}

	err := pool.Drain(context.Background(), "agent-2", true, true, time.Minute)
	if err == nil {
		t.Fatal("expected an error when every master is unreachable")
	}
}

func TestIsUnreachable(t *testing.T) {
	if IsUnreachable(errors.New("plain error")) {
		t.Fatal("a plain error must not be classified as unreachable")
	}
	if !IsUnreachable(&UnreachableError{err: errors.New("dial failed")}) {
		t.Fatal("an UnreachableError must be classified as unreachable")
	}
	wrapped := &UnreachableError{err: errors.New("dial failed")}
	if !IsUnreachable(wrapped) {
		t.Fatal("IsUnreachable must see through to the UnreachableError")
	}
}
