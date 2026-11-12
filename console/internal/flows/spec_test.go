package flows

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kostiantyn-matsebora/barekube/console/internal/config"
)

func okWait() Wait {
	return Wait{Timer: func(config.Timing, *Job) time.Duration { return time.Second },
		OnEvent:   func(_ context.Context, _ *Run, _ payload) St { return skip },
		OnTimeout: func(_ context.Context, _ *Run) St { return skip }}
}

func TestEveryWorkflowSpecIsValidAndDrawn(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	infos := h.eng.Workflows()
	if len(infos) != 7 {
		t.Fatalf("%d workflows", len(infos))
	}
	for _, w := range infos {
		if !strings.HasPrefix(w.Mermaid, "flowchart TD") || !strings.Contains(w.Mermaid, "classDef") {
			t.Errorf("%s: bad diagram", w.Name)
		}
		sp := h.eng.wfs[w.Name].spec
		if sp.Start() != Started {
			t.Errorf("%s starts at %s", w.Name, sp.Start())
		}
		for _, st := range sp.Statuses() {
			if st.String() == "Unknown" {
				t.Errorf("%s: status %d has no name", w.Name, st)
			}
		}
	}
	if !strings.Contains(infos[2].Mermaid, "AwaitingClearance") || !strings.Contains(infos[2].Mermaid, "-.->") {
		t.Error("power off diagram lacks clearance or failure edges")
	}
	if St(999).String() != "Unknown" {
		t.Error("unknown status name")
	}
}

func TestSpecValidateRejectsBrokenGraphs(t *testing.T) {
	t.Parallel()
	step := func(_ context.Context, _ *Run) St { return skip }
	cases := map[string]Spec{
		"no edges":          {Name: "x"},
		"terminal handler":  {Name: "x", Edges: []Edge{{Started, Completed, "", Event}}, Steps: map[St]stepFn{Started: step, Completed: step}},
		"neither":           {Name: "x", Edges: []Edge{{Started, Completed, "", Event}}},
		"both":              {Name: "x", Edges: []Edge{{Started, Completed, "", Event}}, Steps: map[St]stepFn{Started: step}, Waits: map[St]Wait{Started: okWait()}},
		"wait no timeout":   {Name: "x", Edges: []Edge{{Started, Completed, "", Event}}, Waits: map[St]Wait{Started: okWait()}},
		"wait no handler":   {Name: "x", Edges: []Edge{{Started, Completed, "", Event}, {Started, Failed, "", Timeout}}, Waits: map[St]Wait{Started: {}}},
		"step with timeout": {Name: "x", Edges: []Edge{{Started, Completed, "", Event}, {Started, Failed, "", Timeout}}, Steps: map[St]stepFn{Started: step}},
		"two starts":        {Name: "x", Edges: []Edge{{Started, Completed, "", Event}, {Refused, Completed, "", Event}}, Steps: map[St]stepFn{Started: step, Refused: step}},
	}
	for name, sp := range cases {
		if err := sp.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	ok := Spec{Name: "x", Edges: []Edge{{Started, Completed, "", Event}}, Steps: map[St]stepFn{Started: step}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestMermaidShapes(t *testing.T) {
	t.Parallel()
	sp := Spec{Name: "x", Edges: []Edge{
		{Started, AwaitingNodeDown, "go", Event}, {AwaitingNodeDown, StoodBy, "ok", Event},
		{AwaitingNodeDown, Failed, "slow", Timeout}, {Started, Refused, "no", Fail}},
		Waits: map[St]Wait{AwaitingNodeDown: okWait()}}
	m := sp.Mermaid()
	for _, want := range []string{"{{AwaitingNodeDown}}:::wait", "StoodBy([StoodBy]):::rest", "Failed([Failed]):::fail", "Refused([Refused]):::fail", "-.->", "Started([Started])"} {
		if !strings.Contains(m, want) {
			t.Errorf("missing %q in\n%s", want, m)
		}
	}
	done := Spec{Edges: []Edge{{Started, Completed, "", Event}}}.Mermaid()
	if !strings.Contains(done, "Completed([Completed]):::done") {
		t.Error(done)
	}
}
