package app

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/kostiantyn-matsebora/barekube/console/internal/events"
	"github.com/kostiantyn-matsebora/barekube/console/internal/observe"
	"github.com/kostiantyn-matsebora/barekube/console/internal/store"
)

// A restart must keep the event log and the time each node was last seen, and
// must not carry any old fact over as a current one.
func TestServiceKeepsHistoryAcrossARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "console.db")
	cfg, agents, _ := fixture()

	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := NewServiceWithStore(cfg, agents, nil, db)
	if err != nil {
		t.Fatal(err)
	}
	svc.Observer.Tick(context.Background())
	before := len(svc.Events(events.Filter{}))
	seen, _ := svc.Observer.Latest("m1")
	if before == 0 {
		t.Fatal("setup: the first probe must raise events")
	}
	_ = db.Close()

	db2, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	svc2, err := NewServiceWithStore(cfg, agents, nil, db2)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(svc2.Events(events.Filter{})); got != before {
		t.Fatalf("event log lost across a restart: %d != %d", got, before)
	}
	after, ok := svc2.Observer.Latest("m1")
	if !ok || !after.At.Equal(seen.At) {
		t.Fatalf("last-seen time lost: %+v", after)
	}
	if after.Reachable != observe.Unknown || observe.Derive(after) != observe.StateUnknown {
		t.Fatalf("a reading from before the restart must not look current: %+v", after)
	}
}

func TestServiceWithStoreFailsOnABrokenStore(t *testing.T) {
	cfg, agents, _ := fixture()
	db, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if _, err := NewServiceWithStore(cfg, agents, nil, db); err == nil {
		t.Fatal("want an error")
	}
}
