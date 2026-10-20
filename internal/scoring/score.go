package scoring

import (
	"math"
	"sort"

	"github.com/cloudowski/exitctl/internal/catalog"
	"github.com/cloudowski/exitctl/internal/config"
	"github.com/cloudowski/exitctl/internal/graph"
)

// Analysis status values.
const (
	StatusOK            = "ok"
	StatusNothingScored = "nothing_scored"
)

// Confidence values (data-model.md 4.4).
const (
	ConfidenceNormal      = "normal"
	ConfidenceProvisional = "provisional"
	ConfidenceWithheld    = "withheld"
)

// NodeScore holds the per-node values of data-model.md 4.3. All floats are
// rounded to 4 decimals (Round4).
type NodeScore struct {
	ID string
	// Risk is r(n) in [0,1].
	Risk float64
	// RiskByDim is r_d(n) for every dimension with weight > 0.
	RiskByDim map[catalog.Dimension]float64
	// Weight is w(n) = 1 + log2(1 + D(n)).
	Weight float64
	// Dependents is D(n).
	Dependents int
	// Contribution is w(n) * r(n).
	Contribution float64
}

// Coverage reports how much of the estate the catalog recognized.
type Coverage struct {
	Types        float64 // distinct known types / distinct types (S resources and U)
	Resources    float64 // |S resources| / (|S resources| + |U|)
	Weighted     float64 // coverage_w = Σ_S w / (Σ_S w + Σ_U w)
	UnknownTypes int     // distinct types in U
}

// Analysis is the scoring outcome (data-model.md 4.4).
type Analysis struct {
	Status     string // ok | nothing_scored
	Index      int
	Lower      int
	Upper      int
	ShowRange  bool   // Σ_U w > 0
	Grade      string // "" when confidence is withheld or nothing was scored
	Confidence string // normal | provisional | withheld
	// Dimensions holds a score for every dimension in catalog.AllDimensions;
	// nil for dimensions with weight 0 (economic by default) and when nothing
	// was scored.
	Dimensions map[catalog.Dimension]*int
	Coverage   Coverage
	// Nodes holds the values of every node in S and U (U nodes have risk 0).
	Nodes map[string]*NodeScore
	// Scored lists the IDs of S, sorted.
	Scored []string
}

// Round4 rounds half-up to 4 decimal places.
func Round4(x float64) float64 {
	return math.Floor(float64(x*10000)+0.5) / 10000
}

// roundHalfUp implements round_half_up(x) = floor(x + 0.5).
func roundHalfUp(x float64) int {
	return int(math.Floor(x + 0.5))
}

// InS reports whether a node belongs to the scored set S.
func InS(n *graph.Node) bool {
	switch n.Kind {
	case graph.NodeImpliedService:
		return true
	case graph.NodeResource:
		return n.Class == graph.ClassService || n.Class == graph.ClassNeutral
	}
	return false
}

// InU reports whether a node belongs to the unknown set U.
func InU(n *graph.Node) bool {
	return n.Kind == graph.NodeResource && (n.Class == graph.ClassUnknown || n.Class == graph.ClassUnverified)
}

func maxLevelWeight(cfg config.Scoring) float64 {
	m := 0.0
	for _, v := range cfg.LevelWeights {
		if v > m {
			m = v
		}
	}
	return m
}

func maxDimWeight(cfg config.Scoring) float64 {
	m := 0.0
	for _, v := range cfg.DimensionWeights {
		if v > m {
			m = v
		}
	}
	return m
}

// Risk returns r(n) for a list of findings (unrounded): the maximum of
// lw(f)*dw(f) over active findings divided by norm. 0 when there are no active
// findings or norm is 0.
func Risk(findings []graph.Finding, cfg config.Scoring) float64 {
	norm := float64(maxLevelWeight(cfg) * maxDimWeight(cfg))
	if norm == 0 {
		return 0
	}
	best := 0.0
	for _, f := range findings {
		if f.Status != graph.FindingActive {
			continue
		}
		dw := 0.0
		for _, d := range f.Dimensions {
			if w := cfg.DimensionWeights[d]; w > dw {
				dw = w
			}
		}
		p := float64(cfg.LevelWeights[f.Level] * dw)
		if p > best {
			best = p
		}
	}
	return best / norm
}

// RiskByDimension returns r_d(n) (unrounded) for every dimension in
// catalog.AllDimensions whose weight is > 0.
func RiskByDimension(findings []graph.Finding, cfg config.Scoring) map[catalog.Dimension]float64 {
	out := map[catalog.Dimension]float64{}
	for _, d := range catalog.AllDimensions {
		if cfg.DimensionWeights[d] <= 0 {
			continue
		}
		best := 0.0
		for _, f := range findings {
			if f.Status != graph.FindingActive {
				continue
			}
			for _, fd := range f.Dimensions {
				if fd == d {
					if lw := cfg.LevelWeights[f.Level]; lw > best {
						best = lw
					}
					break
				}
			}
		}
		out[d] = best
	}
	return out
}

// Weight returns w(n) = 1 + log2(1 + D(n)).
func Weight(dependents int) float64 {
	return 1 + math.Log2(float64(1+dependents))
}

// sums holds the raw (unrounded) aggregates of data-model.md 4.4.
type sums struct {
	sw, swr, uw float64
	swrd        map[catalog.Dimension]float64
	scored      int
}

// FindingsFunc returns the findings used to score a node.
type FindingsFunc func(n *graph.Node) []graph.Finding

func nodeFindings(n *graph.Node) []graph.Finding { return n.Findings }

// sortedNodes returns the graph nodes sorted by ID (R15) without relying on
// the graph's own ordering.
func sortedNodes(g *graph.Graph) []*graph.Node {
	ns := make([]*graph.Node, 0, len(g.Nodes))
	ns = append(ns, g.Nodes...)
	sort.Slice(ns, func(i, j int) bool { return ns[i].ID < ns[j].ID })
	return ns
}

func compute(g *graph.Graph, dependents func(string) int, cfg config.Scoring, findings FindingsFunc, scores map[string]*NodeScore) sums {
	s := sums{swrd: map[catalog.Dimension]float64{}}
	for _, n := range sortedNodes(g) {
		inS, inU := InS(n), InU(n)
		if !inS && !inU {
			continue
		}
		d := dependents(n.ID)
		w := Weight(d)
		if inU {
			s.uw += w
			if scores != nil {
				scores[n.ID] = &NodeScore{ID: n.ID, Weight: Round4(w), Dependents: d, RiskByDim: map[catalog.Dimension]float64{}}
			}
			continue
		}
		fs := findings(n)
		r := Risk(fs, cfg)
		rd := RiskByDimension(fs, cfg)
		wr := float64(w * r)
		s.sw += w
		s.swr += wr
		s.scored++
		for _, dim := range catalog.AllDimensions {
			if v, ok := rd[dim]; ok {
				s.swrd[dim] += float64(w * v)
			}
		}
		if scores != nil {
			rounded := map[catalog.Dimension]float64{}
			for k, v := range rd {
				rounded[k] = Round4(v)
			}
			scores[n.ID] = &NodeScore{ID: n.ID, Risk: Round4(r), RiskByDim: rounded, Weight: Round4(w), Dependents: d, Contribution: Round4(wr)}
		}
	}
	return s
}

// IndexWith recomputes the index with the findings returned by findings
// instead of each node's own findings (graph and weights unchanged). It is
// used by the lever (data-model.md 4.5): pass a function that returns the
// replacement findings for nodes of the evaluated service and n.Findings for
// all others. ok is false when S is empty. A nil findings function uses the
// nodes' own findings, giving the same index as Score.
func IndexWith(g *graph.Graph, dependents func(id string) int, cfg config.Scoring, findings FindingsFunc) (index int, ok bool) {
	if findings == nil {
		findings = nodeFindings
	}
	s := compute(g, dependents, cfg, findings, nil)
	if s.scored == 0 || s.sw == 0 {
		return 0, false
	}
	return roundHalfUp(float64(100*s.swr) / s.sw), true
}

// Score computes the analysis of data-model.md 4.3 and 4.4. dependents
// returns D(n) for a node ID (callers pass g.DependentsCount).
func Score(g *graph.Graph, dependents func(id string) int, cfg config.Scoring) *Analysis {
	a := &Analysis{
		Nodes:      map[string]*NodeScore{},
		Dimensions: map[catalog.Dimension]*int{},
		Scored:     []string{},
	}
	s := compute(g, dependents, cfg, nodeFindings, a.Nodes)

	// Coverage by types and resources.
	knownTypes := map[string]bool{}
	unknownTypes := map[string]bool{}
	allTypes := map[string]bool{}
	sRes, uRes := 0, 0
	for _, n := range sortedNodes(g) {
		switch {
		case InS(n):
			a.Scored = append(a.Scored, n.ID)
			if n.Kind == graph.NodeResource {
				sRes++
				knownTypes[n.Type] = true
				allTypes[n.Type] = true
			}
		case InU(n):
			uRes++
			unknownTypes[n.Type] = true
			allTypes[n.Type] = true
		}
	}
	a.Coverage.Types = ratio(float64(len(knownTypes)), float64(len(allTypes)))
	a.Coverage.Resources = ratio(float64(sRes), float64(sRes+uRes))
	a.Coverage.UnknownTypes = len(unknownTypes)
	covW := ratio(s.sw, s.sw+s.uw)
	a.Coverage.Weighted = Round4(covW)
	a.ShowRange = s.uw > 0

	for _, d := range catalog.AllDimensions {
		a.Dimensions[d] = nil
	}

	if s.scored == 0 || s.sw == 0 {
		a.Status = StatusNothingScored
		a.Confidence = ConfidenceWithheld
		return a
	}
	a.Status = StatusOK
	a.Index = roundHalfUp(float64(100*s.swr) / s.sw)
	a.Lower = roundHalfUp(float64(100*s.swr) / (s.sw + s.uw))
	a.Upper = roundHalfUp(float64(100*(s.swr+s.uw)) / (s.sw + s.uw))

	switch {
	case covW >= cfg.CoverageThresholds.Normal:
		a.Confidence = ConfidenceNormal
	case covW >= cfg.CoverageThresholds.Provisional:
		a.Confidence = ConfidenceProvisional
	default:
		a.Confidence = ConfidenceWithheld
	}
	if a.Confidence != ConfidenceWithheld {
		a.Grade = cfg.Grade(a.Index)
	}

	for _, d := range catalog.AllDimensions {
		if cfg.DimensionWeights[d] <= 0 {
			continue
		}
		v := roundHalfUp(float64(100*s.swrd[d]) / s.sw)
		a.Dimensions[d] = &v
	}
	return a
}

func ratio(num, den float64) float64 {
	if den == 0 {
		return 1.0
	}
	return Round4(num / den)
}

// MediumThreshold is the normalized risk of a medium finding in the
// highest-weighted dimension; nodes at or above it qualify for lock-in
// chains (data-model.md 3.6).
func MediumThreshold(cfg config.Scoring) float64 {
	norm := maxLevelWeight(cfg) * maxDimWeight(cfg)
	if norm == 0 {
		return 1
	}
	return float64(cfg.LevelWeights["medium"]*maxDimWeight(cfg)) / norm
}
