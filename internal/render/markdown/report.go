// Package markdown renders the Markdown report (contracts/cli.md
// "exitctl report"): the same sections as the HTML report, without the
// interactive graph, with a plain text tree of the top anchors' dependents.
package markdown

import (
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/cloudowski/exitctl/internal/catalog"
	"github.com/cloudowski/exitctl/internal/graph"
	"github.com/cloudowski/exitctl/internal/model"
	"github.com/cloudowski/exitctl/internal/render/terminal"
	"github.com/cloudowski/exitctl/internal/result"
	"github.com/cloudowski/exitctl/internal/version"
)

// TreeAnchors is the number of top anchors whose dependents tree is shown.
const TreeAnchors = 5

// MaxTreeLines caps each dependents tree; the rest is summarized.
const MaxTreeLines = 10

// ExitReadinessLine closes every report.
const ExitReadinessLine = "Exit cost, contracts, and an exit plan: Exit Readiness"

// recommendedMarker marks recommended alternatives in tables.
const recommendedMarker = "[R]"

// Status and confidence values of result.Analysis (scoring package).
const (
	statusNothingScored   = "nothing_scored"
	confidenceProvisional = "provisional"
	confidenceWithheld    = "withheld"
	exceptionActive       = "active"
)

// dimensionOrder is the display order of dimensions.
var dimensionOrder = []string{"technical", "data", "operational", "skills", "economic"}

// Options controls the Markdown report.
type Options struct {
	// Names maps service, alternative, and trait IDs to display names.
	// Optional; IDs are shown when a name is missing.
	Names map[string]string
	// SupportEmail is shown near the end of the report; the line is omitted
	// when empty.
	SupportEmail string
}

func (o Options) name(id string) string {
	if n, ok := o.Names[id]; ok && n != "" {
		return n
	}
	return id
}

// Render writes the Markdown report of doc to w. The output is
// deterministic for a given document and options.
func Render(w io.Writer, doc *result.Document, opts Options) error {
	r := &report{doc: doc, o: opts}
	r.title()
	r.executiveSummary()
	r.dimensions()
	r.lever()
	r.anchors()
	r.trees()
	r.alternatives()
	r.coverage()
	r.exceptions()
	r.closing()
	_, err := io.WriteString(w, r.b.String())
	return err
}

type report struct {
	doc *result.Document
	o   Options
	b   strings.Builder
}

func (r *report) line(s string) {
	r.b.WriteString(strings.TrimRight(s, " "))
	r.b.WriteByte('\n')
}

func (r *report) heading(s string) {
	r.line("")
	r.line("## " + s)
	r.line("")
}

func (r *report) title() {
	r.line("# exitctl lock-in report")
	var repos []string
	for _, in := range r.doc.Inputs {
		if in.Repo != "" {
			repos = append(repos, in.Repo)
		}
	}
	if len(repos) > 0 {
		r.line("")
		r.line(plural(len(repos), "Repository", "Repositories") + ": " + strings.Join(repos, ", "))
	}
}

// Executive Summary.

func (r *report) executiveSummary() {
	a := &r.doc.Analysis
	r.heading("Executive Summary")
	if a.Status == statusNothingScored {
		r.line("**Nothing to score**")
		r.line("")
		r.line("No recognized cloud resources or SDK usage were found, so no lock-in index was computed. " +
			coverageSentence(a.Coverage))
		return
	}
	idx := 0
	if a.Index != nil {
		idx = *a.Index
	}
	if a.Confidence == confidenceWithheld {
		lo, hi := rangeOf(a, idx)
		r.line(fmt.Sprintf("**No grade** - lock-in index range **%d-%d**", lo, hi))
		r.line("")
		r.line(fmt.Sprintf("Coverage is %s (weighted), which is too low for a grade.", pct(a.Coverage.Weighted)))
	} else {
		head := fmt.Sprintf("**Grade %s** - lock-in index **%d/100**", a.Grade, idx)
		if a.Grade == "" {
			head = fmt.Sprintf("Lock-in index **%d/100**", idx)
		}
		if a.Confidence == confidenceProvisional {
			head += " (provisional, coverage " + pct(a.Coverage.Weighted) + ")"
		}
		if a.Range != nil {
			head += fmt.Sprintf(", range %d-%d", a.Range.Lower, a.Range.Upper)
		}
		r.line(head)
	}
	r.line("")
	r.line(r.summaryParagraph(idx))
}

func rangeOf(a *result.Analysis, idx int) (int, int) {
	if a.Range != nil {
		return a.Range.Lower, a.Range.Upper
	}
	return idx, idx
}

// summaryParagraph is one factual paragraph from the index, the top anchor,
// the lever, and coverage.
func (r *report) summaryParagraph(idx int) string {
	a := &r.doc.Analysis
	var s []string
	switch {
	case a.Confidence == confidenceWithheld:
		lo, hi := rangeOf(a, idx)
		s = append(s, fmt.Sprintf("The lock-in index lies between %d and %d out of 100, depending on the resources the catalog does not recognize yet.", lo, hi))
	case a.Grade != "":
		g := fmt.Sprintf("The lock-in index is %d out of 100, grade %s.", idx, a.Grade)
		if a.Confidence == confidenceProvisional {
			g = fmt.Sprintf("The lock-in index is %d out of 100, grade %s (provisional, because coverage is %s).", idx, a.Grade, pct(a.Coverage.Weighted))
		}
		s = append(s, g)
		if m := result.GradeMeaning(a.Grade); m != "" {
			s = append(s, m)
		}
	default:
		s = append(s, fmt.Sprintf("The lock-in index is %d out of 100.", idx))
	}
	if a.Range != nil && a.Confidence != confidenceWithheld {
		s = append(s, fmt.Sprintf("Given the resources the catalog does not recognize yet, it lies between %d and %d.", a.Range.Lower, a.Range.Upper))
	}
	if len(a.Anchors) > 0 {
		an := a.Anchors[0]
		t := fmt.Sprintf("The top anchor is %s with %d dependent %s", r.address(an.Node), an.DependentsCount,
			plural(an.DependentsCount, "resource", "resources"))
		if an.SDKFiles > 0 {
			t += fmt.Sprintf(" and SDK usage in %d %s", an.SDKFiles, plural(an.SDKFiles, "file", "files"))
		}
		s = append(s, t+".")
	} else {
		s = append(s, "No scored resource has lock-in findings.")
	}
	if lv := a.Lever; lv != nil {
		s = append(s, fmt.Sprintf("Replacing %s with %s is the biggest single lever and lowers the index by %d %s.",
			r.o.name(lv.Service), r.altName(lv.Alternative), lv.Delta, plural(lv.Delta, "point", "points")))
	}
	s = append(s, coverageSentence(a.Coverage))
	return strings.Join(s, " ")
}

func coverageSentence(c result.Coverage) string {
	return fmt.Sprintf("The catalog recognizes %s of resource types and %s of resources in the scanned inputs.",
		pct(c.Types), pct(c.Resources))
}

// Dimensions.

func (r *report) dimensions() {
	a := &r.doc.Analysis
	r.heading("Dimensions")
	if a.Status == statusNothingScored {
		r.line("Not assessed: nothing was scored.")
		return
	}
	r.line("| Dimension | Score |")
	r.line("| --- | ---: |")
	for _, d := range dimensionOrder {
		v := "not assessed"
		if p := a.Dimensions[d]; p != nil {
			v = strconv.Itoa(*p) + "/100"
		}
		r.line("| " + cell(d) + " | " + cell(v) + " |")
	}
}

// Biggest lever.

func (r *report) lever() {
	r.heading("Biggest lever")
	lv := r.doc.Analysis.Lever
	if lv == nil {
		r.line("No single replacement lowers the index.")
		return
	}
	r.line(fmt.Sprintf("%s -> %s lowers the index by %d %s.",
		r.o.name(lv.Service), r.altName(lv.Alternative), lv.Delta, plural(lv.Delta, "pt", "pts")))
}

// altName resolves an alternative display name from Options.Names, falling
// back to the names carried by the anchors.
func (r *report) altName(id string) string {
	if n := r.o.name(id); n != id {
		return n
	}
	for _, an := range r.doc.Analysis.Anchors {
		for _, av := range an.Alternatives {
			if av.ID == id && av.Name != "" {
				return av.Name
			}
		}
	}
	return id
}

// Anchors.

func (r *report) anchors() {
	a := &r.doc.Analysis
	r.heading("Anchors")
	if len(a.Anchors) == 0 {
		r.line("None: no scored resource has lock-in findings.")
		return
	}
	r.line("| # | Address | Level | Dependents | Components using | Alternatives |")
	r.line("| ---: | --- | --- | ---: | ---: | --- |")
	for _, an := range a.Anchors {
		level := ""
		if n := r.doc.NodeByID(an.Node); n != nil {
			level = maxLevel(n.Findings)
		}
		r.line("| " + strings.Join([]string{
			strconv.Itoa(an.Rank),
			cell(r.address(an.Node)),
			cell(level),
			strconv.Itoa(an.DependentsCount),
			strconv.Itoa(an.ComponentsUsing),
			cell(r.altList(an.Alternatives)),
		}, " | ") + " |")
	}
	if hasRecommended(a.Anchors) {
		r.line("")
		r.line(recommendedMarker + " = recommended alternative")
	}
	for _, an := range a.Anchors {
		r.anchorDetail(an)
	}
}

func (r *report) anchorDetail(an result.Anchor) {
	r.line("")
	r.line(fmt.Sprintf("### %d. %s", an.Rank, r.address(an.Node)))
	n := r.doc.NodeByID(an.Node)
	r.line("")
	facts := fmt.Sprintf("Dependents: %d", an.DependentsCount)
	if an.SDKFiles > 0 {
		facts += fmt.Sprintf(". SDK usage: %d %s", an.SDKFiles, plural(an.SDKFiles, "file", "files"))
		if an.ComponentsUsing > 0 {
			facts += fmt.Sprintf(" in %d %s", an.ComponentsUsing, plural(an.ComponentsUsing, "component", "components"))
		}
	}
	if n != nil && n.Service != "" {
		facts += ". Service: " + r.o.name(n.Service)
	}
	r.line(facts + ".")
	if an.Reason != "" {
		r.line("")
		r.line("Why this is an anchor: " + an.Reason)
	}
	if n == nil {
		return
	}
	if ev := evidenceOf(n); len(ev) > 0 {
		r.line("")
		r.line("Evidence:")
		r.line("")
		for _, e := range ev {
			l := "- " + code(e.Path+":"+strconv.Itoa(e.Line))
			if e.Excerpt != "" {
				l += " " + code(e.Excerpt)
			}
			r.line(l)
		}
	}
	if len(n.Findings) > 0 {
		r.line("")
		r.line("Findings:")
		r.line("")
		for _, f := range n.Findings {
			l := "- " + string(f.Level) + ": " + r.o.name(f.Trait)
			if len(f.Dimensions) > 0 {
				ds := make([]string, len(f.Dimensions))
				for i, d := range f.Dimensions {
					ds[i] = string(d)
				}
				l += " (" + strings.Join(ds, ", ") + ")"
			}
			if f.Status == graph.FindingAccepted {
				l += " [accepted"
				if f.Exception != "" {
					l += ": " + f.Exception
				}
				l += "]"
			}
			r.line(l)
		}
	}
}

// Dependents trees.

func (r *report) trees() {
	a := &r.doc.Analysis
	r.heading("Dependents of top anchors")
	if len(a.Anchors) == 0 {
		r.line("None.")
		return
	}
	g := terminal.GraphFromDocument(r.doc)
	k := min(len(a.Anchors), TreeAnchors)
	for i, an := range a.Anchors[:k] {
		if i > 0 {
			r.line("")
		}
		r.line(fmt.Sprintf("### %d. %s (%d %s)", an.Rank, r.address(an.Node), an.DependentsCount,
			plural(an.DependentsCount, "dependent", "dependents")))
		r.line("")
		tree := g.BlastRadiusTree(an.Node)
		if tree == nil || len(tree.Children) == 0 {
			r.line("No dependents.")
			continue
		}
		lines := r.treeLines(tree)
		limit := min(len(lines), MaxTreeLines)
		r.line("```text")
		r.line(r.address(an.Node))
		for _, l := range lines[:limit] {
			r.line(l)
		}
		if limit < len(lines) {
			r.line(fmt.Sprintf("... %d more", len(lines)-limit))
		}
		r.line("```")
	}
}

func (r *report) treeLines(root *graph.TreeNode) []string {
	const branch, last, pipe, space = "|- ", "`- ", "|  ", "   "
	var out []string
	var walk func(n *graph.TreeNode, prefix string)
	walk = func(n *graph.TreeNode, prefix string) {
		for i, c := range n.Children {
			conn, next := branch, pipe
			if i == len(n.Children)-1 {
				conn, next = last, space
			}
			out = append(out, prefix+conn+r.address(c.ID))
			walk(c, prefix+next)
		}
	}
	walk(root, "")
	return out
}

// Alternatives.

func (r *report) alternatives() {
	a := &r.doc.Analysis
	r.heading("Alternatives")
	hasAlts := false
	for _, an := range a.Anchors {
		if len(an.Alternatives) > 0 {
			hasAlts = true
			break
		}
	}
	if !hasAlts {
		r.line("None in catalog for the anchors above.")
		return
	}
	r.line("| # | Anchor | Alternative | Kind | Difficulty | Compatibility | Notes |")
	r.line("| ---: | --- | --- | --- | --- | --- | --- |")
	for _, an := range a.Anchors {
		for _, av := range an.Alternatives {
			r.line("| " + strings.Join([]string{
				strconv.Itoa(an.Rank),
				cell(r.address(an.Node)),
				cell(r.altLabel(av)),
				cell(av.Kind),
				cell(av.Difficulty),
				cell(av.Compatibility),
				cell(av.Notes),
			}, " | ") + " |")
		}
	}
	if hasRecommended(a.Anchors) {
		r.line("")
		r.line(recommendedMarker + " = recommended alternative")
	}
}

func (r *report) altLabel(av result.AlternativeView) string {
	name := av.Name
	if name == "" {
		name = r.o.name(av.ID)
	}
	if av.Recommended {
		name = recommendedMarker + " " + name
	}
	return name
}

func (r *report) altList(alts []result.AlternativeView) string {
	if len(alts) == 0 {
		return "none in catalog"
	}
	parts := make([]string, len(alts))
	for i, av := range alts {
		parts[i] = r.altLabel(av)
	}
	return strings.Join(parts, ", ")
}

func hasRecommended(anchors []result.Anchor) bool {
	for _, an := range anchors {
		for _, av := range an.Alternatives {
			if av.Recommended {
				return true
			}
		}
	}
	return false
}

// Coverage. Wording is neutral: it uses Source.Note (user-safe by contract)
// and gap kinds, never gap subjects of parse failures or raw error text.

func (r *report) coverage() {
	c := r.doc.Analysis.Coverage
	r.heading("Coverage")
	r.line(fmt.Sprintf("- Resource types recognized: %s", pct(c.Types)))
	r.line(fmt.Sprintf("- Resources recognized: %s", pct(c.Resources)))
	r.line(fmt.Sprintf("- Weighted coverage: %s", pct(c.Weighted)))
	if c.UnknownTypes > 0 {
		r.line(fmt.Sprintf("- Types not in the catalog: %d (exitctl coverage lists them)", c.UnknownTypes))
	}
	if len(c.UnverifiedMatches) > 0 {
		r.line(fmt.Sprintf("- Matched by draft catalog entries: %d", len(c.UnverifiedMatches)))
	}

	var notRead []model.Source
	for _, s := range r.doc.Facts.Sources {
		if s.Status != model.StatusParsed {
			notRead = append(notRead, s)
		}
	}
	if len(notRead) > 0 {
		r.line("")
		r.line("Inputs not fully read:")
		r.line("")
		for _, s := range notRead {
			l := "- " + code(s.Path) + " (" + string(s.Kind) + "): " + statusWording(s.Status)
			if s.Note != "" && s.Note != statusWording(s.Status) {
				l += "; " + s.Note
			}
			r.line(l)
		}
	}

	gaps := r.doc.Facts.Gaps
	if len(gaps) > 0 {
		r.line("")
		r.line("Gaps:")
		r.line("")
		for _, g := range gaps {
			l := "- " + gapWording(g.Kind)
			if g.Kind != model.GapParseFailure && g.Subject != "" {
				l += ": " + code(g.Subject)
			}
			if g.Path != "" {
				l += " in " + code(g.Path)
			}
			if g.Count > 1 {
				l += fmt.Sprintf(" (%d times)", g.Count)
			}
			r.line(l)
		}
	}
	if len(notRead) == 0 && len(gaps) == 0 {
		r.line("")
		r.line("All inputs were read and no gaps were recorded.")
	}
}

func statusWording(s model.SourceStatus) string {
	switch s {
	case model.StatusPartial:
		return "partly read"
	case model.StatusSkipped:
		return "skipped"
	case model.StatusFailed:
		return "could not be parsed"
	case model.StatusUnresolved:
		return "not resolved offline"
	}
	return string(s)
}

func gapWording(k model.GapKind) string {
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
		return "File could not be parsed"
	case model.GapRemoteKustomize:
		return "Remote Kustomize base not fetched"
	}
	return "Gap (" + string(k) + ")"
}

// Exceptions.

func (r *report) exceptions() {
	a := &r.doc.Analysis
	r.heading("Exceptions")
	if len(a.Exceptions) == 0 {
		r.line("No exceptions configured.")
		return
	}
	active := 0
	for _, e := range a.Exceptions {
		if e.State == exceptionActive {
			active++
		}
	}
	if active > 0 && a.IndexWithoutExceptions != nil {
		r.line(fmt.Sprintf("%d accepted; the index without exceptions is %d.", active, *a.IndexWithoutExceptions))
		r.line("")
	}
	r.line("| Exception | State | Owner | Expires | Findings | Reason |")
	r.line("| --- | --- | --- | --- | ---: | --- |")
	for _, e := range a.Exceptions {
		exp := e.Expires
		if exp == "" {
			exp = "-"
		}
		r.line("| " + strings.Join([]string{
			cell(e.ID), cell(e.State), cell(e.Owner), cell(exp),
			strconv.Itoa(len(e.MatchedFindings)), cell(e.Reason),
		}, " | ") + " |")
	}
}

// Closing lines and version footer.

func (r *report) closing() {
	r.line("")
	r.line("---")
	r.line("")
	r.line(ExitReadinessLine)
	if r.o.SupportEmail != "" {
		r.line("")
		r.line("Support: " + r.o.SupportEmail)
	}
	r.line("")
	r.line(r.footer())
}

func (r *report) footer() string {
	v := r.doc.Versions
	tool := v.Tool
	if tool == "" {
		tool = version.Tool
	}
	rules := v.Rules
	if rules == "" {
		rules = version.RulesVersion
	}
	fp := v.ConfigFingerprint
	if len(fp) > 8 {
		fp = fp[:8]
	}
	const s = " · "
	l := "exitctl " + tool + s + "catalog " + v.Catalog + s + "rules " + rules + s + "config " + fp
	if v.CustomScoring {
		l += " (custom scoring)"
	}
	if v.CatalogDraftsIncluded {
		l += " (draft catalog)"
	}
	return l
}

// Helpers.

// address returns the display address of a node ID.
func (r *report) address(id string) string {
	n := r.doc.NodeByID(id)
	if n == nil {
		return id
	}
	if n.Kind == "implied_service" {
		return r.o.name(n.Service) + " (used by code, not declared)"
	}
	return terminal.Address(n)
}

type evidence struct {
	Path, Excerpt string
	Line          int
}

// evidenceOf returns the node's and its findings' evidence, de-duplicated by
// path and line, sorted by path then line.
func evidenceOf(n *result.Node) []evidence {
	var all []model.Evidence
	all = append(all, n.Evidence...)
	for _, f := range n.Findings {
		all = append(all, f.Evidence...)
	}
	all = model.SortEvidence(append([]model.Evidence(nil), all...))
	seen := map[string]bool{}
	var out []evidence
	for _, e := range all {
		k := e.Path + ":" + strconv.Itoa(e.Line)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, evidence{Path: e.Path, Line: e.Line, Excerpt: e.Excerpt})
	}
	return out
}

func maxLevel(fs []graph.Finding) string {
	best := catalog.LevelNone
	for _, f := range fs {
		if f.Status != graph.FindingActive {
			continue
		}
		if catalog.LevelRank(f.Level) > catalog.LevelRank(best) {
			best = f.Level
		}
	}
	return string(best)
}

// cell makes s safe for a table cell: pipes escaped, line breaks flattened.
func cell(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.ReplaceAll(s, "|", `\|`)
}

// code wraps s in an inline code span, using a longer fence when s contains
// backticks.
func code(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	fence := "`"
	for strings.Contains(s, fence) {
		fence += "`"
	}
	if strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") || fence != "`" {
		return fence + " " + s + " " + fence
	}
	return fence + s + fence
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// pct renders a 0..1 fraction as a whole percentage, rounded down so that
// 100% is shown only for complete coverage (same as the terminal summary).
func pct(f float64) string {
	p := int(math.Floor(f*100 + 1e-9))
	p = max(0, min(100, p))
	return strconv.Itoa(p) + "%"
}
