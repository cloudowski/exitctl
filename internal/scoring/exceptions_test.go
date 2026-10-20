package scoring

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/cloudowski/exitctl/internal/catalog"
	"github.com/cloudowski/exitctl/internal/config"
	"github.com/cloudowski/exitctl/internal/graph"
)

func idFnd(node, trait string, lvl catalog.Level, dims ...catalog.Dimension) graph.Finding {
	f := fnd(trait, lvl, dims...)
	f.ID = node + "::" + trait
	return f
}

// excGraph: two service nodes with critical data findings and one neutral node.
//
//	r//infra#aws_cognito_user_pool.main  service aws.cognito  data-gravity (critical, data)
//	r//infra#aws_sqs_queue.events        service aws.sqs      proprietary-api (critical, data)
//	r//infra#aws_vpc.main                neutral
func excGraph() *graph.Graph {
	cog := svc("r//infra#aws_cognito_user_pool.main", "aws_cognito_user_pool",
		idFnd("r//infra#aws_cognito_user_pool.main", "data-gravity", catalog.LevelCritical, catalog.DimData))
	cog.Service, cog.NativeAddress = "aws.cognito", "aws_cognito_user_pool.main"
	sqs := svc("r//infra#aws_sqs_queue.events", "aws_sqs_queue",
		idFnd("r//infra#aws_sqs_queue.events", "proprietary-api", catalog.LevelCritical, catalog.DimData),
		idFnd("r//infra#aws_sqs_queue.events", "proprietary-sdk", catalog.LevelMedium, catalog.DimTechnical))
	sqs.Service, sqs.NativeAddress = "aws.sqs", "aws_sqs_queue.events"
	sqs.Aliases = []string{"aws:sqs:events"}
	vpc := &graph.Node{ID: "r//infra#aws_vpc.main", Kind: graph.NodeResource, Class: graph.ClassNeutral, Type: "aws_vpc", NativeAddress: "aws_vpc.main"}
	return buildGraph([]*graph.Node{cog, sqs, vpc}, nil)
}

func exc(id string, sel config.Selector, expires string) config.Exception {
	return config.Exception{ID: id, Select: sel, Reason: "deliberate", Owner: "team", Expires: expires}
}

func stateOf(t *testing.T, states []ExceptionState, id string) ExceptionState {
	t.Helper()
	for _, s := range states {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("exception %s not reported in %+v", id, states)
	return ExceptionState{}
}

func findingOf(g *graph.Graph, id string) graph.Finding {
	for _, n := range g.Nodes {
		for _, f := range n.Findings {
			if f.ID == id {
				return f
			}
		}
	}
	return graph.Finding{}
}

func TestApplyExceptionsStates(t *testing.T) {
	g := excGraph()
	noOwner := exc("no-owner", config.Selector{Service: "aws.sqs"}, "")
	noOwner.Owner = " "
	noReason := exc("no-reason", config.Selector{Service: "aws.sqs"}, "")
	noReason.Reason = ""
	states := ApplyExceptions(g, []config.Exception{
		exc("old-sqs", config.Selector{Resource: "aws_sqs_queue.events"}, "2026-01-01"),
		exc("keep-cognito", config.Selector{Service: "aws.cognito"}, "2027-01-01"),
		noReason,
		noOwner,
		exc("nothing", config.Selector{Service: "aws.s3"}, ""),
		exc("Bad_ID", config.Selector{Service: "aws.sqs"}, ""),
		exc("two", config.Selector{Service: "aws.sqs", Trait: "x"}, ""),
		exc("bad-date", config.Selector{Service: "aws.sqs"}, "2026-13-01"),
		exc("last-day", config.Selector{Finding: "r//infra#aws_sqs_queue.events::proprietary-sdk"}, "2026-10-06"),
	}, "2026-10-06")

	var ids []string
	for _, s := range states {
		ids = append(ids, s.ID)
	}
	if fmt.Sprint(ids) != "[Bad_ID bad-date keep-cognito last-day no-owner no-reason nothing old-sqs two]" {
		t.Fatalf("not sorted by ID: %v", ids)
	}
	want := map[string][2]string{
		"keep-cognito": {ExceptionActive, ""},
		"old-sqs":      {ExceptionExpired, ""},
		"no-reason":    {ExceptionInvalid, ProblemMissingReason},
		"no-owner":     {ExceptionInvalid, ProblemMissingOwner},
		"nothing":      {ExceptionInvalid, ProblemNoMatch},
		"Bad_ID":       {ExceptionInvalid, ProblemBadID},
		"two":          {ExceptionInvalid, ProblemBadSelector},
		"bad-date":     {ExceptionInvalid, ProblemBadExpires},
		"last-day":     {ExceptionActive, ""}, // expires day itself is still active
	}
	for _, id := range ids {
		s := stateOf(t, states, id)
		if w := want[id]; s.State != w[0] || s.Problem != w[1] {
			t.Errorf("%s: state %q problem %q, want %q %q", id, s.State, s.Problem, w[0], w[1])
		}
		if s.State == ExceptionInvalid && len(s.Matched) != 0 {
			t.Errorf("%s: invalid exception lists matches %v", id, s.Matched)
		}
	}
	if m := stateOf(t, states, "old-sqs").Matched; !reflect.DeepEqual(m, []string{
		"r//infra#aws_sqs_queue.events::proprietary-api", "r//infra#aws_sqs_queue.events::proprietary-sdk"}) {
		t.Errorf("expired matches %v", m)
	}

	// Active: accepted with the exception ID. Expired: still active.
	if f := findingOf(g, "r//infra#aws_cognito_user_pool.main::data-gravity"); f.Status != graph.FindingAccepted || f.Exception != "keep-cognito" {
		t.Errorf("cognito finding %+v", f)
	}
	if f := findingOf(g, "r//infra#aws_sqs_queue.events::proprietary-api"); f.Status != graph.FindingActive || f.Exception != "" {
		t.Errorf("expired exception applied: %+v", f)
	}
	if f := findingOf(g, "r//infra#aws_sqs_queue.events::proprietary-sdk"); f.Status != graph.FindingAccepted || f.Exception != "last-day" {
		t.Errorf("finding selector: %+v", f)
	}
}

func TestApplyExceptionsSelectors(t *testing.T) {
	cases := []struct {
		name string
		sel  config.Selector
		want []string
	}{
		{"canonical id", config.Selector{Resource: "r//infra#aws_sqs_queue.events"}, []string{"r//infra#aws_sqs_queue.events::proprietary-api", "r//infra#aws_sqs_queue.events::proprietary-sdk"}},
		{"alias", config.Selector{Resource: "aws:sqs:events"}, []string{"r//infra#aws_sqs_queue.events::proprietary-api", "r//infra#aws_sqs_queue.events::proprietary-sdk"}},
		{"trait", config.Selector{Trait: "data-gravity"}, []string{"r//infra#aws_cognito_user_pool.main::data-gravity"}},
		{"resource and trait", config.Selector{Resource: "aws_sqs_queue.events", Trait: "proprietary-sdk"}, []string{"r//infra#aws_sqs_queue.events::proprietary-sdk"}},
		{"resource without findings", config.Selector{Resource: "aws_vpc.main"}, []string{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := excGraph()
			st := ApplyExceptions(g, []config.Exception{exc("e", c.sel, "")}, "2026-10-06")
			if !reflect.DeepEqual(st[0].Matched, c.want) {
				t.Fatalf("matched %v, want %v", st[0].Matched, c.want)
			}
		})
	}
}

func TestApplyExceptionsIndexDelta(t *testing.T) {
	g := excGraph()
	cfg := config.DefaultScoring()
	before := Score(g, deps(nil), cfg)
	// S = {cognito, sqs, vpc}, w = 1 each: r = 1, 1, 0 -> 200/3 = 66.67 -> 67.
	if before.Index != 67 {
		t.Fatalf("index before %d", before.Index)
	}
	ApplyExceptions(g, []config.Exception{exc("keep-cognito", config.Selector{Service: "aws.cognito"}, "")}, "")
	after := Score(g, deps(nil), cfg)
	// Cognito stays in the denominator with r = 0: 100/3 = 33.33 -> 33.
	if after.Index != 33 || fmt.Sprint(len(after.Scored)) != "3" {
		t.Fatalf("index after %d scored %v", after.Index, after.Scored)
	}
	if after.Nodes["r//infra#aws_cognito_user_pool.main"].Risk != 0 {
		t.Fatal("accepted node still has risk")
	}
	if iwe, ok := IndexWithoutExceptions(g, deps(nil), cfg); !ok || iwe != before.Index {
		t.Fatalf("index without exceptions %d (ok=%v), want %d", iwe, ok, before.Index)
	}
	// IndexWithoutExceptions must not touch the graph.
	if f := findingOf(g, "r//infra#aws_cognito_user_pool.main::data-gravity"); f.Status != graph.FindingAccepted {
		t.Fatal("graph findings were modified")
	}

	// Re-applying without exceptions restores every finding.
	if st := ApplyExceptions(g, nil, ""); st != nil {
		t.Fatalf("states %v", st)
	}
	if Score(g, deps(nil), cfg).Index != 67 {
		t.Fatal("reset failed")
	}
}

func TestApplyExceptionsOverlapAndNoReferenceDate(t *testing.T) {
	g := excGraph()
	states := ApplyExceptions(g, []config.Exception{
		exc("b-second", config.Selector{Service: "aws.cognito"}, "2000-01-01"),
		exc("a-first", config.Selector{Trait: "data-gravity"}, ""),
	}, "")
	// Without a reference date expiry is not evaluated.
	if s := stateOf(t, states, "b-second"); s.State != ExceptionActive {
		t.Fatalf("b-second %+v", s)
	}
	if f := findingOf(g, "r//infra#aws_cognito_user_pool.main::data-gravity"); f.Exception != "a-first" {
		t.Fatalf("first active exception by ID must win, got %q", f.Exception)
	}
}

func TestLatestDate(t *testing.T) {
	if got := LatestDate([]string{"2026-01-02", "", "2026-10-06", "2025-12-31"}); got != "2026-10-06" {
		t.Fatalf("got %q", got)
	}
	if LatestDate(nil) != "" {
		t.Fatal("empty")
	}
}
