// Package engine is the single analysis entry point used by scan and merge:
// facts -> graph (catalog) -> scoring (config) -> result document.
package engine

import (
	"slices"
	"sort"

	"github.com/cloudowski/exitctl/internal/catalog"
	"github.com/cloudowski/exitctl/internal/config"
	"github.com/cloudowski/exitctl/internal/graph"
	"github.com/cloudowski/exitctl/internal/model"
	"github.com/cloudowski/exitctl/internal/result"
	"github.com/cloudowski/exitctl/internal/scoring"
	"github.com/cloudowski/exitctl/internal/version"
)

// maxDependentsListed bounds the per-node dependents lists stored in the
// result (used by the HTML blast-radius highlight). Above this node count,
// only anchors carry their dependents list.
const maxDependentsListed = 2000

// derivedGaps are produced by the graph builder, not by detectors. They are
// stripped from input facts so that re-analysis (merge) does not duplicate them.
var derivedGaps = map[model.GapKind]bool{
	model.GapUnknownType:         true,
	model.GapUnknownSDK:          true,
	model.GapUnresolvedReference: true,
}

// Input describes one analyzed repository.
type Input struct {
	Repo     string
	AsOf     string
	Versions *result.Versions // nil = produced by this run
}

// Analysis bundles the document with the in-memory graph and scores, so
// renderers that need graph algorithms (explain, graph) can use them.
type Analysis struct {
	Doc    *result.Document
	Graph  *graph.Graph
	Scores *scoring.Analysis
	Index  *catalog.Index
	Config config.Config
}

// Analyze runs the whole pipeline over facts.
func Analyze(facts model.Facts, ix *catalog.Index, cfg config.Config, inputs []Input) *Analysis {
	facts = stripDerivedGaps(facts)
	facts.Sort()

	g := graph.Build(facts, ix)
	g.Condense()
	waves := g.Waves()
	// Exceptions are applied before scoring, so risk, anchors, the lever,
	// dimensions, and chains all use the scores with exceptions.
	excs := scoring.ApplyExceptions(g, cfg.Exceptions, ReferenceDate(inputs))
	deps := dependentsWithoutContext(g)
	depCount := func(id string) int { return len(deps(id)) }
	sc := scoring.Score(g, depCount, cfg.Scoring)
	threshold := scoring.MediumThreshold(cfg.Scoring)
	chains := g.Chains(func(id string) bool {
		ns := sc.Nodes[id]
		return ns != nil && ns.Risk > 0 && ns.Risk >= threshold
	})
	anchors := scoring.Anchors(g, sc, ix)
	groups := g.Groups(cfg.Scoring.GroupThreshold) // sets Node.Group
	graph.ScoreGroups(groups, func(id string) (float64, float64) {
		if ns := sc.Nodes[id]; ns != nil {
			return ns.Risk, ns.Contribution
		}
		return 0, 0
	})

	vers := Versions(ix, cfg)
	doc := &result.Document{
		Format:   version.ResultFormat,
		Kind:     result.KindResult,
		Versions: vers,
		Facts:    facts,
	}
	for _, in := range inputs {
		v := vers
		if in.Versions != nil {
			v = *in.Versions
		}
		doc.Inputs = append(doc.Inputs, result.Input{Repo: in.Repo, AsOf: in.AsOf, Versions: v})
	}
	doc.Facts.Gaps = append(doc.Facts.Gaps, g.Gaps...)

	anchorSet := map[string]bool{}
	for _, a := range anchors {
		anchorSet[a.Node] = true
	}
	listAll := len(g.Nodes) <= maxDependentsListed
	for _, n := range g.Nodes {
		ns := sc.Nodes[n.ID]
		rn := result.Node{
			ID: n.ID, Aliases: n.Aliases, Kind: string(n.Kind), Type: n.Type,
			NativeAddress: n.NativeAddress, Service: n.Service, Class: n.Class,
			Sources: n.Sources, Evidence: n.Evidence, Findings: n.Findings,
			Weight: scoring.Weight(depCount(n.ID)), DependentsCount: depCount(n.ID),
			Wave: n.Wave, SCC: n.SCC, Group: n.Group, Notes: n.Notes,
		}
		if ns != nil {
			rn.Risk, rn.Weight, rn.Contribution = ns.Risk, ns.Weight, ns.Contribution
			if len(ns.RiskByDim) > 0 {
				rn.RiskByDimension = map[string]float64{}
				for d, v := range ns.RiskByDim {
					if v > 0 {
						rn.RiskByDimension[string(d)] = result.Round4(v)
					}
				}
				if len(rn.RiskByDimension) == 0 {
					rn.RiskByDimension = nil
				}
			}
		}
		rn.Weight = result.Round4(rn.Weight)
		if rn.Wave < 1 {
			rn.Wave = 1
		}
		if rn.DependentsCount > 0 && (listAll || anchorSet[n.ID]) {
			rn.Dependents = deps(n.ID)
		}
		doc.Graph.Nodes = append(doc.Graph.Nodes, rn)
	}
	for _, e := range g.Edges {
		doc.Graph.Edges = append(doc.Graph.Edges, result.Edge{
			From: e.From, To: e.To, Kind: string(e.Kind), Confidence: e.Confidence, Evidence: e.Evidence,
		})
	}
	for _, gr := range groups {
		doc.Graph.Groups = append(doc.Graph.Groups, result.Group{
			ID: gr.ID, Kind: string(gr.Kind), Label: gr.Label, Members: gr.Members, Parent: gr.Parent,
			MaxRisk: result.Round4(gr.MaxRisk), Contribution: result.Round4(gr.Contribution),
		})
	}
	doc.Graph.SCCs = sccs(g)
	for _, w := range waves {
		doc.Analysis.Waves = append(doc.Analysis.Waves, result.Wave{Wave: w.Wave, Units: w.Units})
	}
	for _, c := range chains {
		doc.Analysis.Chains = append(doc.Analysis.Chains, result.Chain{Nodes: c})
	}

	a := &doc.Analysis
	a.Status = sc.Status
	a.Coverage = result.Coverage{
		Types: sc.Coverage.Types, Resources: sc.Coverage.Resources,
		Weighted: sc.Coverage.Weighted, UnknownTypes: sc.Coverage.UnknownTypes,
	}
	for _, n := range g.Nodes {
		if n.Class == graph.ClassUnverified {
			a.Coverage.UnverifiedMatches = append(a.Coverage.UnverifiedMatches, n.ID)
		}
	}
	if sc.Status == scoring.StatusOK {
		a.Index = result.IntPtr(sc.Index)
		a.IndexWithoutExceptions = result.IntPtr(sc.Index)
		if iwe, ok := scoring.IndexWithoutExceptions(g, depCount, cfg.Scoring); ok {
			a.IndexWithoutExceptions = result.IntPtr(iwe)
		}
		a.Grade = sc.Grade
		a.Confidence = sc.Confidence
		if sc.ShowRange {
			a.Range = &result.Range{Lower: sc.Lower, Upper: sc.Upper}
		}
		a.Dimensions = map[string]*int{}
		for d, v := range sc.Dimensions {
			a.Dimensions[string(d)] = v
		}
	}
	for _, an := range anchors {
		a.Anchors = append(a.Anchors, result.Anchor{
			Rank: an.Rank, Node: an.Node, Contribution: result.Round4(an.Contribution),
			DependentsCount: an.DependentsCount, ComponentsUsing: an.ComponentsUsing,
			SDKFiles: an.SDKFiles, Alternatives: result.AlternativeViews(ix, an.Alternatives),
			Reason: an.Reason,
		})
	}
	if lv := scoring.FindLever(g, sc, depCount, cfg.Scoring, ix); lv != nil {
		a.Lever = &result.Lever{Service: lv.Service, Alternative: lv.Alternative, Delta: lv.Delta}
	}
	for _, e := range excs {
		a.Exceptions = append(a.Exceptions, result.ExceptionState{
			ID: e.ID, State: e.State, Reason: e.Reason, Owner: e.Owner, Expires: e.Expires,
			MatchedFindings: e.Matched,
		})
	}
	if vers.CatalogDraftsIncluded {
		a.Notes = append(a.Notes, "draft catalog entries were scored (development mode); results are unverified")
	}
	doc.Normalize()
	return &Analysis{Doc: doc, Graph: g, Scores: sc, Index: ix, Config: cfg}
}

// ReferenceDate returns the reference date for exception expiry: the latest
// as_of among the inputs. A scan has one as_of for all its paths (--as-of,
// else today); a merge re-evaluates exceptions as of the newest input, so a
// merged result never treats an exception as active that the newest scan
// already saw expired. "" when no input carries a date (expiry is then not
// evaluated).
func ReferenceDate(inputs []Input) string {
	dates := make([]string, 0, len(inputs))
	for _, in := range inputs {
		dates = append(dates, in.AsOf)
	}
	return scoring.LatestDate(dates)
}

// dependentsWithoutContext returns the dependents of a node, leaving out
// context nodes (types that deploy nothing): a lookup that reads a resource
// does not make it harder to move. Results are memoized; without context
// nodes it is graph.Dependents itself.
func dependentsWithoutContext(g *graph.Graph) func(id string) []string {
	if !slices.ContainsFunc(g.Nodes, func(n *graph.Node) bool { return n.Class == graph.ClassContext }) {
		return g.Dependents
	}
	memo := map[string][]string{}
	return func(id string) []string {
		if v, ok := memo[id]; ok {
			return v
		}
		var out []string
		for _, d := range g.Dependents(id) {
			if n := g.Node(d); n == nil || n.Class != graph.ClassContext {
				out = append(out, d)
			}
		}
		memo[id] = out
		return out
	}
}

// Versions returns the versions block for the current catalog and config.
func Versions(ix *catalog.Index, cfg config.Config) result.Versions {
	m := ix.Catalog().Manifest
	return result.Versions{
		Tool:                  version.Tool,
		Catalog:               m.CatalogVersion,
		CatalogSchema:         m.Schema,
		Rules:                 version.RulesVersion,
		ConfigFingerprint:     cfg.Scoring.Fingerprint(),
		CustomScoring:         cfg.Scoring.Custom(),
		CatalogDraftsIncluded: ix.Options().IncludeDrafts,
	}
}

func stripDerivedGaps(f model.Facts) model.Facts {
	out := f.Gaps[:0:0]
	for _, gp := range f.Gaps {
		if !derivedGaps[gp.Kind] {
			out = append(out, gp)
		}
	}
	f.Gaps = out
	return f
}

func sccs(g *graph.Graph) []result.SCC {
	by := map[int][]string{}
	for _, n := range g.Nodes {
		if n.SCC > 0 {
			by[n.SCC] = append(by[n.SCC], n.ID)
		}
	}
	ids := make([]int, 0, len(by))
	for id := range by {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	out := make([]result.SCC, 0, len(ids))
	for _, id := range ids {
		m := by[id]
		sort.Strings(m)
		out = append(out, result.SCC{ID: id, Members: m})
	}
	return out
}
