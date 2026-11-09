package nodeagent

import (
	"context"
	"testing"
	"time"
)

func TestPoolRoutesEveryCallAndFailsOver(t *testing.T) {
	dead := &Real{Host: "127.0.0.1", Port: 1, Token: "t", PinnedCertSHA256: "x"}
	good := &Mock{NodeName: "m2"}
	p := &MasterPool{Masters: []Client{dead, good}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := p.Cordon(ctx, "n"); err != nil {
		t.Fatal(err)
	}
	if v, err := p.Cordoned(ctx, "n"); !v || err != nil {
		t.Fatal(v, err)
	}
	if err := p.Uncordon(ctx, "n"); err != nil {
		t.Fatal(err)
	}
	if err := p.Drain(ctx, "n", true, true, time.Second); err != nil {
		t.Fatal(err)
	}
	if v, err := p.NodeReady(ctx, "n"); !v || err != nil {
		t.Fatal(v, err)
	}
	if err := p.SetLonghornScheduling(ctx, "n", true); err != nil {
		t.Fatal(err)
	}
	if v, err := p.ZeroReplicaRisk(ctx, "n"); v != nil || err != nil {
		t.Fatal(v, err)
	}
	if err := p.EtcdSnapshot(ctx); err != nil {
		t.Fatal(err)
	}
	if v, err := p.LonghornAvailable(ctx); v || err != nil {
		t.Fatal(v, err)
	}
}

func TestPoolWithNoMastersOrAllDead(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := (&MasterPool{}).Cordon(ctx, "n"); err == nil {
		t.Fatal("empty pool accepted")
	}
	dead := &Real{Host: "127.0.0.1", Port: 1, Token: "t", PinnedCertSHA256: "x"}
	if err := (&MasterPool{Masters: []Client{dead, dead}}).Uncordon(ctx, "n"); err == nil {
		t.Fatal("all-dead pool accepted")
	}
}
