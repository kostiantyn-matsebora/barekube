package store

import (
	"testing"
	"time"

	"github.com/kostiantyn-matsebora/barekube/console/internal/observe"
)

func TestObservationsSaveReplaceLoad(t *testing.T) {
	t.Parallel()
	d, _ := open(t)
	s := d.Observations()
	a := observe.Observation{Node: "a", At: t0, Reachable: observe.Yes, K3sActive: observe.No, Ready: observe.Yes, Cordoned: observe.No}
	if err := s.SaveObservation(a); err != nil {
		t.Fatal(err)
	}
	a2 := a
	a2.At, a2.K3sActive = t0.Add(time.Minute), observe.Yes
	_ = s.SaveObservation(a2) // replaces, one row per node
	_ = s.SaveObservation(observe.Observation{Node: "b", At: t0})
	got, err := s.LoadObservations()
	if err != nil || len(got) != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	if got[0] != a2 && !(got[0].Node == "a" && got[0].K3sActive == observe.Yes && got[0].Cordoned == observe.No && got[0].At.Equal(a2.At)) {
		t.Fatalf("round trip: %+v", got[0])
	}
}

func TestObservationsErrorsOnClosedDB(t *testing.T) {
	t.Parallel()
	d, _ := open(t)
	s := d.Observations()
	_ = d.Close()
	if err := s.SaveObservation(observe.Observation{Node: "a"}); err == nil {
		t.Fatal("save")
	}
	if _, err := s.LoadObservations(); err == nil {
		t.Fatal("load")
	}
}
