package html

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/cloudowski/exitctl/internal/graph"
	"github.com/cloudowski/exitctl/internal/model"
	"github.com/cloudowski/exitctl/internal/result"
	"github.com/cloudowski/exitctl/internal/version"
)

// ExitReadinessLine is the single closing line about the paid service.
const ExitReadinessLine = "Exit cost, contracts, and an exit plan: Exit Readiness"

// Options control the HTML report.
type Options struct {
	// Names maps service, alternative, and trait IDs to display names. May be nil.
	Names map[string]string
	// SupportEmail is shown at the bottom (callers pass version.SupportEmail);
	// the line is omitted when empty.
	SupportEmail string
}

// Analysis values (scoring package).
const (
	statusNothingScored   = "nothing_scored"
	confidenceProvisional = "provisional"
	confidenceWithheld    = "withheld"
	exceptionActive       = "active"
	findingAccepted       = graph.FindingAccepted
)

// maxAnchorCards is the number of anchors rendered as full cards; the rest
// are listed in a compact table.
const maxAnchorCards = 10

// maxEvidence is the number of evidence lines shown per anchor.
const maxEvidence = 5

// dimensionOrder is the display order of dimensions.
var dimensionOrder = []string{"technical", "data", "operational", "skills", "economic"}

var tmpl = template.Must(template.New("report").Parse(reportTemplate))

// Render writes the self-contained HTML report of doc to w. All CSS, JS, and
// data are inlined; the page makes no network requests. The output is
// deterministic for a given document and options.
func Render(w io.Writer, doc *result.Document, opts Options) error {
	v, err := buildView(doc, opts)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, v); err != nil {
		return err
	}
	_, err = w.Write(buf.Bytes())
	return err
}

// view is the template data.
type view struct {
	Repos         string
	Band          string // a b c d f none
	GradeLetter   string
	GradeCaption  string
	Index         string
	HasIndex      bool
	Range         string
	Pills         []string
	Paragraph     string
	TopAnchor     string
	TopAnchorSub  string
	LeverFact     string
	LeverFactSub  string
	CoverageFact  string
	CoverageSub   string
	NothingScored bool

	Dimensions []dimView
	Lever      *leverView
	Anchors    []anchorView
	MoreAnch   []anchorRow
	Coverage   coverageView
	Exceptions []exceptionView
	ExcSummary string

	HasGraph     bool
	GraphSummary string
	ExitLine     string
	SupportEmail string
	Footer       []string

	CSS     template.CSS
	Cyto    template.JS
	Script  template.JS
	Data    template.JS
	License template.JS
}

type dimView struct {
	Name     string
	Assessed bool
	Value    int
	Band     string
	Letter   string
}

type leverView struct {
	Service     string
	Alternative string
	Delta       int
	Unit        string
	Text        string
}

type anchorView struct {
	Rank         int
	Band         string
	Address      string
	Sub          string
	Reason       string
	Dependents   int
	Contribution string
	SDK          string
	Evidence     []evidenceView
	MoreEvidence int
	Findings     []findingView
	Alternatives []altView
}

type anchorRow struct {
	Rank         int
	Address      string
	Dependents   int
	Contribution string
	Alternatives string
}

type evidenceView struct{ Loc, Excerpt string }

type findingView struct {
	Name, Level, Band, Dimensions string
	Accepted                      bool
	Exception                     string
}

type altView struct {
	Name, Kind, Difficulty, Compatibility, Notes string
	Recommended                                  bool
}

type coverageView struct {
	Types, Resources, Weighted string
	UnknownTypes               int
	Unverified                 int
	Sources                    []sourceView
	Gaps                       []gapView
	Notes                      []string
	Clean                      bool
}

type sourceView struct{ Path, Kind, Status string }

type gapView struct{ Text, Subject, Path, Count string }

type exceptionView struct {
	ID, State, Owner, Expires, Reason string
	Findings                          int
}

func (o Options) name(id string) string {
	if n, ok := o.Names[id]; ok && n != "" {
		return n
	}
	return id
}

func buildView(doc *result.Document, o Options) (*view, error) {
	a := &doc.Analysis
	v := &view{
		ExitLine:     ExitReadinessLine,
		SupportEmail: o.SupportEmail,
		CSS:          template.CSS(reportCSS),
		Cyto:         template.JS(cytoscapeJS),
		Script:       template.JS(reportJS),
		License:      template.JS("/*\n" + cytoscapeLicense + "*/"),
	}
	v.Repos = repos(doc)
	summary(v, doc, o)
	v.Dimensions = dimensions(a)
	v.Lever = lever(doc, o)
	anchors(v, doc, o)
	v.Coverage = coverage(doc)
	exceptions(v, a)
	v.Footer = footer(doc)

	data, n, err := graphData(doc, o)
	if err != nil {
		return nil, err
	}
	v.Data = template.JS(data)
	v.HasGraph = n > 0
	v.GraphSummary = fmt.Sprintf("%d %s, %d %s", n, plural(n, "resource", "resources"),
		len(doc.Graph.Edges), plural(len(doc.Graph.Edges), "dependency", "dependencies"))
	if g := len(doc.Graph.Groups); g > 0 {
		v.GraphSummary += fmt.Sprintf(", %d %s", g, plural(g, "group", "groups"))
	}
	return v, nil
}

func repos(doc *result.Document) string {
	var rs []string
	for _, in := range doc.Inputs {
		if in.Repo != "" {
			rs = append(rs, in.Repo)
		}
	}
	return strings.Join(rs, ", ")
}

// bandOf maps a 0..100 index or dimension value to the default grade bands
// (A 0-9 ... E 55-100).
func bandOf(p int) string {
	switch {
	case p < 10:
		return "a"
	case p < 25:
		return "b"
	case p < 40:
		return "c"
	case p < 55:
		return "d"
	}
	return "e"
}

// nodeRiskBandOf maps a node risk (0..100) to a risk band. Node risk is the
// worst finding of one node, not an index, so it keeps its own fixed
// thresholds instead of following the grade bands.
func nodeRiskBandOf(p int) string {
	switch {
	case p < 20:
		return "a"
	case p < 40:
		return "b"
	case p < 60:
		return "c"
	case p < 80:
		return "d"
	}
	return "e"
}

func riskBand(r float64) string {
	if r <= 0 {
		return "none"
	}
	return nodeRiskBandOf(int(math.Round(r * 100)))
}

func pct(f float64) string {
	p := int(math.Floor(f*100 + 1e-9))
	p = max(0, min(100, p))
	return strconv.Itoa(p) + "%"
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// address returns the display address of a node.
func address(n *result.Node, o Options) string {
	if n.Kind == "implied_service" {
		return o.name(n.Service) + " (used by code, not declared)"
	}
	return result.DisplayAddress(n)
}

func nodeAddress(doc *result.Document, id string, o Options) string {
	if n := doc.NodeByID(id); n != nil {
		return address(n, o)
	}
	return id
}

func leverAlternative(doc *result.Document, lv *result.Lever, o Options) string {
	alt := o.name(lv.Alternative)
	if alt != lv.Alternative {
		return alt
	}
	for _, an := range doc.Analysis.Anchors {
		for _, av := range an.Alternatives {
			if av.ID == lv.Alternative && av.Name != "" {
				return av.Name
			}
		}
	}
	return alt
}

func summary(v *view, doc *result.Document, o Options) {
	a := &doc.Analysis
	c := a.Coverage
	v.CoverageFact = pct(c.Types) + " of types"
	v.CoverageSub = pct(c.Resources) + " of resources recognized"
	if c.UnknownTypes > 0 {
		v.CoverageSub += fmt.Sprintf(", %d unknown %s", c.UnknownTypes, plural(c.UnknownTypes, "type", "types"))
	}
	if a.Status == statusNothingScored {
		v.NothingScored = true
		v.Band, v.GradeLetter, v.GradeCaption = "none", "-", "no grade"
		v.Paragraph = "No recognized cloud resources or SDK usage were found, so there is nothing to score. " +
			"The coverage section lists what was found but not recognized."
		v.TopAnchor, v.LeverFact = "none", "none"
		return
	}
	idx := 0
	if a.Index != nil {
		idx = *a.Index
		v.HasIndex = true
	}
	v.Index = strconv.Itoa(idx)
	if a.Range != nil {
		v.Range = fmt.Sprintf("range %d-%d", a.Range.Lower, a.Range.Upper)
	}
	var sentences []string
	switch {
	case a.Confidence == confidenceWithheld || a.Grade == "":
		v.Band, v.GradeLetter, v.GradeCaption = "none", "-", "grade withheld"
		lo, hi := idx, idx
		if a.Range != nil {
			lo, hi = a.Range.Lower, a.Range.Upper
		}
		v.Pills = append(v.Pills, "coverage "+pct(c.Weighted))
		sentences = append(sentences, fmt.Sprintf(
			"Coverage is too low for a reliable grade (%s weighted). The index lies between %d and %d; "+
				"catalog coverage for the unknown types narrows this range.", pct(c.Weighted), lo, hi))
	default:
		v.Band = strings.ToLower(a.Grade)
		v.GradeLetter = a.Grade
		v.GradeCaption = "grade"
		if a.Confidence == confidenceProvisional {
			v.GradeCaption = "provisional grade"
			v.Pills = append(v.Pills, "provisional, coverage "+pct(c.Weighted))
		}
		if s := result.GradeMeaning(a.Grade); s != "" {
			sentences = append(sentences, s)
		}
	}

	if len(a.Anchors) > 0 {
		an := a.Anchors[0]
		addr := nodeAddress(doc, an.Node, o)
		v.TopAnchor = addr
		v.TopAnchorSub = fmt.Sprintf("%d dependent %s", an.DependentsCount, plural(an.DependentsCount, "resource", "resources"))
		if n := doc.NodeByID(an.Node); n != nil && n.Service != "" {
			v.TopAnchorSub = o.name(n.Service) + ", " + v.TopAnchorSub
		}
		sentences = append(sentences, fmt.Sprintf("The largest contributor is %s with %d dependent %s.",
			addr, an.DependentsCount, plural(an.DependentsCount, "resource", "resources")))
	} else {
		v.TopAnchor = "none"
		v.TopAnchorSub = "no scored resource has lock-in findings"
	}
	if lv := a.Lever; lv != nil {
		alt := leverAlternative(doc, lv, o)
		v.LeverFact = o.name(lv.Service) + " -> " + alt
		v.LeverFactSub = fmt.Sprintf("lowers the index by %d %s", lv.Delta, plural(lv.Delta, "point", "points"))
		sentences = append(sentences, fmt.Sprintf("Replacing %s with %s is the biggest single lever: it lowers the index by %d %s.",
			o.name(lv.Service), alt, lv.Delta, plural(lv.Delta, "point", "points")))
	} else {
		v.LeverFact = "none"
		v.LeverFactSub = "no single replacement changes the index"
	}
	sentences = append(sentences, fmt.Sprintf("%s of resource types and %s of resources were recognized.",
		pct(c.Types), pct(c.Resources)))
	if active := activeExceptions(a); active > 0 {
		v.Pills = append(v.Pills, fmt.Sprintf("%d accepted %s", active, plural(active, "exception", "exceptions")))
	}
	v.Paragraph = strings.Join(sentences, " ")
}

func activeExceptions(a *result.Analysis) int {
	n := 0
	for _, e := range a.Exceptions {
		if e.State == exceptionActive {
			n++
		}
	}
	return n
}

func dimensions(a *result.Analysis) []dimView {
	out := make([]dimView, 0, len(dimensionOrder))
	for _, d := range dimensionOrder {
		dv := dimView{Name: d, Band: "none"}
		if p := a.Dimensions[d]; p != nil && a.Status != statusNothingScored {
			dv.Assessed = true
			dv.Value = max(0, min(100, *p))
			dv.Band = bandOf(dv.Value)
			dv.Letter = strings.ToUpper(dv.Band)
		}
		out = append(out, dv)
	}
	return out
}

func lever(doc *result.Document, o Options) *leverView {
	lv := doc.Analysis.Lever
	if lv == nil {
		return nil
	}
	alt := leverAlternative(doc, lv, o)
	t := fmt.Sprintf("Replacing %s with %s removes the most lock-in of any single change.", o.name(lv.Service), alt)
	var anchored []string
	for _, an := range doc.Analysis.Anchors {
		if n := doc.NodeByID(an.Node); n != nil && n.Service == lv.Service {
			anchored = append(anchored, address(n, o))
		}
	}
	if len(anchored) > 0 {
		t += fmt.Sprintf(" It affects %d %s: %s.", len(anchored), plural(len(anchored), "anchor", "anchors"), strings.Join(anchored, ", "))
	}
	return &leverView{Service: o.name(lv.Service), Alternative: alt, Delta: lv.Delta,
		Unit: plural(lv.Delta, "point", "points"), Text: t}
}

// humanize turns a trait ID such as "managed-change-stream" into
// "Managed change stream".
func humanize(id string) string {
	words := strings.Split(id, "-")
	for i, w := range words {
		switch {
		case acronyms[w]:
			words[i] = strings.ToUpper(w)
		case i == 0 && w != "":
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

// acronyms are upper-cased by humanize.
var acronyms = map[string]bool{"api": true, "sdk": true, "iam": true, "ci": true, "dns": true, "sql": true,
	"vpc": true, "oidc": true, "cdn": true, "kms": true, "k8s": true, "crd": true, "ml": true, "ai": true}

var levelBand = map[string]string{"low": "b", "medium": "c", "high": "d", "critical": "e"}

func trait(id string, o Options) string {
	if n := o.name(id); n != id {
		return n
	}
	return humanize(id)
}

func anchors(v *view, doc *result.Document, o Options) {
	for i, an := range doc.Analysis.Anchors {
		n := doc.NodeByID(an.Node)
		if i >= maxAnchorCards {
			var alts []string
			for _, av := range an.Alternatives {
				alts = append(alts, altName(av, o))
			}
			row := anchorRow{Rank: an.Rank, Address: nodeAddress(doc, an.Node, o), Dependents: an.DependentsCount,
				Contribution: strconv.FormatFloat(an.Contribution, 'f', 2, 64), Alternatives: strings.Join(alts, ", ")}
			if row.Alternatives == "" {
				row.Alternatives = "none in catalog"
			}
			v.MoreAnch = append(v.MoreAnch, row)
			continue
		}
		av := anchorView{Rank: an.Rank, Address: an.Node, Reason: an.Reason, Dependents: an.DependentsCount,
			Contribution: strconv.FormatFloat(an.Contribution, 'f', 2, 64), Band: "none"}
		if an.SDKFiles > 0 {
			av.SDK = fmt.Sprintf("%d SDK %s", an.SDKFiles, plural(an.SDKFiles, "file", "files"))
			if an.ComponentsUsing > 0 {
				av.SDK += fmt.Sprintf(" in %d %s", an.ComponentsUsing, plural(an.ComponentsUsing, "component", "components"))
			}
		}
		if n != nil {
			av.Address = address(n, o)
			av.Band = riskBand(n.Risk)
			var sub []string
			if n.Service != "" {
				sub = append(sub, o.name(n.Service))
			}
			if n.Type != "" && n.Kind != "implied_service" {
				sub = append(sub, n.Type)
			}
			av.Sub = strings.Join(sub, " | ")
			ev := n.Evidence
			if len(ev) > maxEvidence {
				av.MoreEvidence = len(ev) - maxEvidence
				ev = ev[:maxEvidence]
			}
			for _, e := range ev {
				av.Evidence = append(av.Evidence, evidenceView{Loc: evidenceLoc(e, len(doc.Inputs) > 1), Excerpt: e.Excerpt})
			}
			for _, f := range n.Findings {
				dims := make([]string, 0, len(f.Dimensions))
				for _, d := range f.Dimensions {
					dims = append(dims, string(d))
				}
				fb := levelBand[string(f.Level)]
				if fb == "" {
					fb = "none"
				}
				av.Findings = append(av.Findings, findingView{Name: trait(f.Trait, o), Level: string(f.Level), Band: fb,
					Dimensions: strings.Join(dims, ", "), Accepted: f.Status == findingAccepted, Exception: f.Exception})
			}
		}
		for _, alt := range an.Alternatives {
			av.Alternatives = append(av.Alternatives, altView{Name: altName(alt, o), Kind: strings.ReplaceAll(alt.Kind, "-", " "),
				Difficulty: alt.Difficulty, Compatibility: strings.ReplaceAll(alt.Compatibility, "-", " "),
				Notes: alt.Notes, Recommended: alt.Recommended})
		}
		v.Anchors = append(v.Anchors, av)
	}
}

func altName(a result.AlternativeView, o Options) string {
	if a.Name != "" && a.Name != a.ID {
		return a.Name
	}
	return o.name(a.ID)
}

func evidenceLoc(e model.Evidence, multi bool) string {
	p := e.Path
	if multi && e.Repo != "" {
		p = e.Repo + "/" + p
	}
	return p + ":" + strconv.Itoa(e.Line)
}

// statusText is the neutral description of a source that was not fully read.
// Source notes are deliberately not shown: reports describe gaps only in
// these fixed words, never with parser output.
func statusText(s model.SourceStatus) string {
	switch s {
	case model.StatusPartial:
		return "partly read; the readable part is included"
	case model.StatusSkipped:
		return "skipped"
	case model.StatusFailed:
		return "could not be read; counted as a coverage gap"
	case model.StatusUnresolved:
		return "not available offline"
	}
	return "not fully read"
}

func gapText(k model.GapKind) string {
	switch k {
	case model.GapUnknownType:
		return "Type not in the catalog"
	case model.GapUnknownSDK:
		return "Cloud SDK not in the catalog"
	case model.GapUnresolvedModule:
		return "Module source not available offline"
	case model.GapUnresolvedChart:
		return "Chart not available offline"
	case model.GapUnresolvedReference:
		return "Reference to an object outside the scanned inputs"
	case model.GapParseFailure:
		return "File could not be read"
	case model.GapRemoteKustomize:
		return "Remote Kustomize base not fetched"
	}
	return "Coverage gap"
}

func coverage(doc *result.Document) coverageView {
	c := doc.Analysis.Coverage
	cv := coverageView{Types: pct(c.Types), Resources: pct(c.Resources), Weighted: pct(c.Weighted),
		UnknownTypes: c.UnknownTypes, Unverified: len(c.UnverifiedMatches), Notes: doc.Analysis.Notes}
	for _, s := range doc.Facts.Sources {
		if s.Status == model.StatusParsed {
			continue
		}
		p := s.Path
		if s.Repo != "" && len(doc.Inputs) > 1 {
			p = s.Repo + "/" + p
		}
		cv.Sources = append(cv.Sources, sourceView{Path: p, Kind: string(s.Kind), Status: statusText(s.Status)})
	}
	for _, g := range doc.Facts.Gaps {
		gv := gapView{Text: gapText(g.Kind), Path: g.Path}
		if g.Kind != model.GapParseFailure {
			gv.Subject = g.Subject
			if k := strings.LastIndex(gv.Subject, "#"); k >= 0 && k < len(gv.Subject)-1 {
				gv.Subject = gv.Subject[k+1:]
			}
		}
		if g.Count > 1 {
			gv.Count = strconv.Itoa(g.Count)
		}
		cv.Gaps = append(cv.Gaps, gv)
	}
	cv.Clean = len(cv.Sources) == 0 && len(cv.Gaps) == 0
	return cv
}

func exceptions(v *view, a *result.Analysis) {
	for _, e := range a.Exceptions {
		exp := e.Expires
		if exp == "" {
			exp = "-"
		}
		v.Exceptions = append(v.Exceptions, exceptionView{ID: e.ID, State: e.State, Owner: e.Owner,
			Expires: exp, Reason: e.Reason, Findings: len(e.MatchedFindings)})
	}
	if n := activeExceptions(a); n > 0 {
		v.ExcSummary = fmt.Sprintf("%d accepted %s.", n, plural(n, "exception", "exceptions"))
		if a.IndexWithoutExceptions != nil {
			v.ExcSummary += fmt.Sprintf(" The index without exceptions is %d.", *a.IndexWithoutExceptions)
		}
	}
}

func footer(doc *result.Document) []string {
	vs := doc.Versions
	tool := vs.Tool
	if tool == "" {
		tool = version.Tool
	}
	rules := vs.Rules
	if rules == "" {
		rules = version.RulesVersion
	}
	fp := vs.ConfigFingerprint
	if len(fp) > 8 {
		fp = fp[:8]
	}
	cfg := "config " + fp
	if vs.CustomScoring {
		cfg += " (custom scoring)"
	}
	out := []string{"exitctl " + tool, "catalog " + vs.Catalog, "rules " + rules, cfg}
	if vs.CatalogDraftsIncluded {
		out = append(out, "draft catalog")
	}
	return out
}

// Graph payload: compact, index-based references.

type gNode struct {
	Label      string  `json:"label"`
	Type       string  `json:"type,omitempty"`
	Service    string  `json:"service,omitempty"`
	Risk       float64 `json:"risk"`
	Class      string  `json:"class,omitempty"`
	Wave       int     `json:"wave,omitempty"`
	Group      *int    `json:"group,omitempty"`
	Dependents []int   `json:"dependents,omitempty"`
	Anchor     int     `json:"anchor,omitempty"`
}

type gGroup struct {
	Kind    string  `json:"kind"`
	Label   string  `json:"label"`
	Members []int   `json:"members"`
	Parent  *int    `json:"parent,omitempty"`
	MaxRisk float64 `json:"max_risk,omitempty"`
}

type gData struct {
	Nodes  []gNode  `json:"nodes"`
	Edges  [][3]any `json:"edges"`
	Groups []gGroup `json:"groups"`
}

// graphData encodes the graph for report.js. Nodes, edges, and groups keep
// the document's (sorted) order; references are array indices. encoding/json
// escapes <, >, and & so the payload is safe inside a script element.
func graphData(doc *result.Document, o Options) (string, int, error) {
	g := &doc.Graph
	idx := make(map[string]int, len(g.Nodes))
	for i, n := range g.Nodes {
		idx[n.ID] = i
	}
	gidx := make(map[string]int, len(g.Groups))
	for i, gr := range g.Groups {
		gidx[gr.ID] = i
	}
	rank := map[string]int{}
	for _, an := range doc.Analysis.Anchors {
		rank[an.Node] = an.Rank
	}
	d := gData{Nodes: make([]gNode, 0, len(g.Nodes)), Edges: make([][3]any, 0, len(g.Edges)), Groups: make([]gGroup, 0, len(g.Groups))}
	for i := range g.Nodes {
		n := &g.Nodes[i]
		label := address(n, o)
		if n.Kind == "implied_service" {
			label = o.name(n.Service) + " (implied)"
		}
		gn := gNode{Label: label, Type: n.Type, Risk: result.Round4(n.Risk), Class: n.Class, Wave: n.Wave, Anchor: rank[n.ID]}
		if n.Service != "" {
			gn.Service = o.name(n.Service)
		}
		if gi, ok := gidx[n.Group]; ok && n.Group != "" {
			gn.Group = &gi
		}
		for _, dep := range n.Dependents {
			if j, ok := idx[dep]; ok {
				gn.Dependents = append(gn.Dependents, j)
			}
		}
		d.Nodes = append(d.Nodes, gn)
	}
	for _, e := range g.Edges {
		f, ok1 := idx[e.From]
		t, ok2 := idx[e.To]
		if !ok1 || !ok2 {
			continue
		}
		d.Edges = append(d.Edges, [3]any{f, t, e.Kind})
	}
	for _, gr := range g.Groups {
		gg := gGroup{Kind: gr.Kind, Label: gr.Label, Members: []int{}, MaxRisk: result.Round4(gr.MaxRisk)}
		for _, m := range gr.Members {
			if j, ok := idx[m]; ok {
				gg.Members = append(gg.Members, j)
			}
		}
		if p, ok := gidx[gr.Parent]; ok && gr.Parent != "" {
			gg.Parent = &p
		}
		d.Groups = append(d.Groups, gg)
	}
	b, err := json.Marshal(d)
	if err != nil {
		return "", 0, err
	}
	return string(b), len(d.Nodes), nil
}
