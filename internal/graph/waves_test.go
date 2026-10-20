package graph

import (
	"reflect"
	"testing"
)

func TestWavesChain(t *testing.T) {
	// c depends on b depends on a.
	g := handGraph([]string{"a", "b", "c"}, [][2]string{{"c", "b"}, {"b", "a"}})
	want := []WaveUnits{
		{Wave: 1, Units: [][]string{{"a"}}},
		{Wave: 2, Units: [][]string{{"b"}}},
		{Wave: 3, Units: [][]string{{"c"}}},
	}
	if got := g.Waves(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Waves = %v, want %v", got, want)
	}
	for id, w := range map[string]int{"a": 1, "b": 2, "c": 3} {
		if g.Node(id).Wave != w {
			t.Errorf("%s.Wave = %d, want %d", id, g.Node(id).Wave, w)
		}
	}
}

func TestWavesDiamond(t *testing.T) {
	// top -> left, right -> base; extra depends on base only; lone is isolated.
	g := handGraph([]string{"base", "extra", "left", "lone", "right", "top"}, [][2]string{
		{"top", "left"}, {"top", "right"}, {"left", "base"}, {"right", "base"}, {"extra", "base"},
	})
	want := []WaveUnits{
		{Wave: 1, Units: [][]string{{"base"}, {"lone"}}},
		{Wave: 2, Units: [][]string{{"extra"}, {"left"}, {"right"}}},
		{Wave: 3, Units: [][]string{{"top"}}},
	}
	if got := g.Waves(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Waves = %v, want %v", got, want)
	}
}

func TestWavesCycle(t *testing.T) {
	// a -> b -> c -> a is a cycle; d depends on a; c depends on e.
	g := handGraph([]string{"a", "b", "c", "d", "e"}, [][2]string{
		{"a", "b"}, {"b", "c"}, {"c", "a"}, {"d", "a"}, {"c", "e"},
	})
	want := []WaveUnits{
		{Wave: 1, Units: [][]string{{"e"}}},
		{Wave: 2, Units: [][]string{{"a", "b", "c"}}},
		{Wave: 3, Units: [][]string{{"d"}}},
	}
	if got := g.Waves(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Waves = %v, want %v", got, want)
	}
	for _, id := range []string{"a", "b", "c"} {
		if g.Node(id).Wave != 2 {
			t.Errorf("%s.Wave = %d, want 2", id, g.Node(id).Wave)
		}
	}
}

func TestWavesEmpty(t *testing.T) {
	g := handGraph(nil, nil)
	if got := g.Waves(); len(got) != 0 {
		t.Errorf("Waves(empty) = %v", got)
	}
}

// TestWavesCyclesFixture mirrors the graph expected from
// testdata/fixtures/cycles (see its EXPECTED.md) until the Terraform and
// Kubernetes detectors can build it end to end.
func TestWavesCyclesFixture(t *testing.T) {
	const (
		sgA  = "cycles//#aws_security_group.a"
		sgB  = "cycles//#aws_security_group.b"
		rAB  = "cycles//#aws_security_group_rule.a_from_b"
		rBA  = "cycles//#aws_security_group_rule.b_from_a"
		cm   = "cycles//k8s#/ConfigMap/cycles/web-config"
		svc  = "cycles//k8s#/Service/cycles/web"
		depl = "cycles//k8s#apps/Deployment/cycles/web"
	)
	g := handGraph([]string{sgA, sgB, rAB, rBA, cm, svc, depl}, [][2]string{
		{sgA, sgB}, {sgB, sgA},
		{rAB, sgA}, {rAB, sgB}, {rBA, sgA}, {rBA, sgB},
		{svc, depl}, {depl, cm},
	})
	var multi []Unit
	for _, u := range g.Condense() {
		if len(u.Members) > 1 {
			multi = append(multi, u)
		}
	}
	if len(multi) != 1 || len(multi[0].Members) != 2 {
		t.Fatalf("multi-member units = %v, want one 2-cycle", multi)
	}
	if g.Node(sgA).SCC == 0 || g.Node(sgA).SCC != g.Node(sgB).SCC {
		t.Errorf("SCC a=%d b=%d", g.Node(sgA).SCC, g.Node(sgB).SCC)
	}
	for _, id := range []string{rAB, rBA, cm, svc, depl} {
		if g.Node(id).SCC != 0 {
			t.Errorf("%s SCC = %d, want 0", id, g.Node(id).SCC)
		}
	}
	if n := g.DependentsCount(sgA); n != 3 {
		t.Errorf("DependentsCount(sg a) = %d, want 3", n)
	}
	want := []WaveUnits{
		{Wave: 1, Units: [][]string{{sgA, sgB}, {cm}}},
		{Wave: 2, Units: [][]string{{rAB}, {rBA}, {depl}}},
		{Wave: 3, Units: [][]string{{svc}}},
	}
	if got := g.Waves(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Waves = %v, want %v", got, want)
	}
}
