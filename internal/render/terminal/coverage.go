package terminal

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/cloudowski/exitctl/internal/model"
	"github.com/cloudowski/exitctl/internal/result"
)

// Node classes of result.Node (graph package) used by the coverage view.
const (
	classUnknown    = "unknown"
	classUnverified = "unverified"
	classContext    = "context"
	nodeKindRes     = "resource"
)

// noteUnverifiedMatch prefixes the node note naming the draft catalog entry
// that matched an unverified node (graph.NoteUnverifiedMatch).
const noteUnverifiedMatch = "unverified_match:"

// maxCoverageTypeWidth caps the type column of the coverage tables.
const maxCoverageTypeWidth = 48

// CoverageLocation is an example location of a type: file and line.
type CoverageLocation struct {
	Repo string `json:"repo,omitempty"`
	Path string `json:"path"`
	Line int    `json:"line"`
}

// CoverageType is one row of the unknown types or unverified knowledge
// table: a resource type with the number of resources of that type and the
// number of distinct resources of other types that depend on them.
type CoverageType struct {
	SourceKind string            `json:"source_kind"`
	Type       string            `json:"type"`
	Resources  int               `json:"resources"`
	Dependents int               `json:"dependents"`
	Example    *CoverageLocation `json:"example,omitempty"`
	Entries    []string          `json:"draft_entries,omitempty"`
	Nodes      []string          `json:"nodes"`
}

// CoverageNode is an unknown or unverified node with its dependents count.
type CoverageNode struct {
	ID              string            `json:"id"`
	Type            string            `json:"type"`
	NativeAddress   string            `json:"native_address,omitempty"`
	Class           string            `json:"class"`
	DependentsCount int               `json:"dependents_count"`
	Location        *CoverageLocation `json:"location,omitempty"`
}

// CoverageView is everything `exitctl coverage` shows; it is also the
// --format json shape. Every slice is sorted deterministically.
type CoverageView struct {
	Status       string          `json:"status"`
	Confidence   string          `json:"confidence,omitempty"`
	Index        *int            `json:"index,omitempty"`
	Grade        string          `json:"grade,omitempty"`
	Range        *result.Range   `json:"range,omitempty"`
	Coverage     result.Coverage `json:"coverage"`
	UnknownTypes []CoverageType  `json:"unknown_types"`
	UnknownNodes []CoverageNode  `json:"unknown_nodes"`
	UnknownSDKs  []model.Gap     `json:"unknown_sdks,omitempty"`
	Unverified   []CoverageType  `json:"unverified_knowledge"`
	NotScored    []CoverageType  `json:"not_scored"`
	Unresolved   []model.Gap     `json:"unresolved_inputs"`
	NotRead      []model.Source  `json:"sources_not_read"`
	ParseGaps    []model.Gap     `json:"parse_failures"`
	Gaps         []model.Gap     `json:"gaps"`
	multiRepo    bool
}

// BuildCoverage derives the coverage view from a result document. It does
// not modify doc.
func BuildCoverage(doc *result.Document) *CoverageView {
	a := &doc.Analysis
	v := &CoverageView{
		Status: a.Status, Confidence: a.Confidence, Index: a.Index, Grade: a.Grade, Range: a.Range,
		Coverage:     a.Coverage,
		UnknownTypes: []CoverageType{}, UnknownNodes: []CoverageNode{}, Unverified: []CoverageType{}, NotScored: []CoverageType{},
		Unresolved: []model.Gap{}, NotRead: []model.Source{}, ParseGaps: []model.Gap{}, Gaps: []model.Gap{},
		multiRepo: len(doc.Inputs) > 1,
	}
	if v.Coverage.UnverifiedMatches != nil {
		v.Coverage.UnverifiedMatches = append([]string(nil), v.Coverage.UnverifiedMatches...)
		sort.Strings(v.Coverage.UnverifiedMatches)
	}

	raw := map[string]*model.RawNode{}
	for i := range doc.Facts.RawNodes {
		rn := &doc.Facts.RawNodes[i]
		if _, ok := raw[rn.ID]; !ok {
			raw[rn.ID] = rn
		}
	}
	rawOf := func(n *result.Node) *model.RawNode {
		if rn := raw[n.ID]; rn != nil {
			return rn
		}
		for _, al := range n.Aliases {
			if rn := raw[al]; rn != nil {
				return rn
			}
		}
		return nil
	}

	g := GraphFromDocument(doc)
	type key struct{ kind, typ string }
	type acc struct {
		row  CoverageType
		deps map[string]bool
		ent  map[string]bool
	}
	unknown, unverified := map[key]*acc{}, map[key]*acc{}
	var nodes []*result.Node
	for i := range doc.Graph.Nodes {
		n := &doc.Graph.Nodes[i]
		if n.Kind != nodeKindRes || (n.Class != classUnknown && n.Class != classUnverified) {
			continue
		}
		nodes = append(nodes, n)
	}
	sort.SliceStable(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	for _, n := range nodes {
		kind, typ := coverageKindType(n, rawOf(n))
		loc := firstLocation(n)
		v.UnknownNodes = append(v.UnknownNodes, CoverageNode{ID: n.ID, Type: typ, NativeAddress: n.NativeAddress,
			Class: n.Class, DependentsCount: n.DependentsCount, Location: loc})
		m := unknown
		if n.Class == classUnverified {
			m = unverified
		}
		k := key{kind, typ}
		e := m[k]
		if e == nil {
			e = &acc{row: CoverageType{SourceKind: kind, Type: typ}, deps: map[string]bool{}, ent: map[string]bool{}}
			m[k] = e
		}
		e.row.Resources++
		e.row.Nodes = append(e.row.Nodes, n.ID)
		if e.row.Example == nil || locLess(loc, e.row.Example) {
			e.row.Example = loc
		}
		for _, d := range g.Dependents(n.ID) {
			e.deps[d] = true
		}
		for _, note := range n.Notes {
			if strings.HasPrefix(note, noteUnverifiedMatch) {
				e.ent[strings.TrimPrefix(note, noteUnverifiedMatch)] = true
			}
		}
	}
	finish := func(m map[key]*acc) []CoverageType {
		keys := make([]key, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			if keys[i].kind != keys[j].kind {
				return keys[i].kind < keys[j].kind
			}
			return keys[i].typ < keys[j].typ
		})
		out := make([]CoverageType, 0, len(keys))
		for _, k := range keys {
			e := m[k]
			members := map[string]bool{}
			for _, id := range e.row.Nodes {
				members[id] = true
			}
			for _, id := range sortedSet(e.deps) {
				if !members[id] {
					e.row.Dependents++
				}
			}
			e.row.Entries = sortedSet(e.ent)
			sort.Strings(e.row.Nodes)
			out = append(out, e.row)
		}
		sortCoverageTypes(out)
		return out
	}
	v.UnknownTypes = finish(unknown)
	v.Unverified = finish(unverified)

	for _, gp := range doc.Facts.Gaps {
		if gp.Kind == model.GapParseFailure {
			gp.Subject = "" // never shown: it may carry parser detail
		}
		v.Gaps = append(v.Gaps, gp)
		switch gp.Kind {
		case model.GapUnknownSDK:
			v.UnknownSDKs = append(v.UnknownSDKs, gp)
		case model.GapUnresolvedModule, model.GapUnresolvedChart, model.GapRemoteKustomize, model.GapUnresolvedReference:
			v.Unresolved = append(v.Unresolved, gp)
		case model.GapParseFailure:
			v.ParseGaps = append(v.ParseGaps, gp)
		}
	}
	sortGaps(v.Gaps)
	sortGaps(v.ParseGaps)
	sort.SliceStable(v.UnknownSDKs, func(i, j int) bool {
		a, b := v.UnknownSDKs[i], v.UnknownSDKs[j]
		if a.Provider != b.Provider {
			return a.Provider < b.Provider
		}
		if a.Service != b.Service {
			return a.Service < b.Service
		}
		if a.Subject != b.Subject {
			return a.Subject < b.Subject
		}
		if a.Repo != b.Repo {
			return a.Repo < b.Repo
		}
		return a.Path < b.Path
	})
	sort.SliceStable(v.Unresolved, func(i, j int) bool {
		a, b := v.Unresolved[i], v.Unresolved[j]
		if unresolvedOrder(a.Kind) != unresolvedOrder(b.Kind) {
			return unresolvedOrder(a.Kind) < unresolvedOrder(b.Kind)
		}
		if a.Subject != b.Subject {
			return a.Subject < b.Subject
		}
		if a.Repo != b.Repo {
			return a.Repo < b.Repo
		}
		return a.Path < b.Path
	})
	for _, s := range doc.Facts.Sources {
		switch s.Status {
		case model.StatusFailed, model.StatusPartial, model.StatusSkipped:
			v.NotRead = append(v.NotRead, s)
		}
	}
	sort.SliceStable(v.NotRead, func(i, j int) bool {
		a, b := v.NotRead[i], v.NotRead[j]
		if a.Repo != b.Repo {
			return a.Repo < b.Repo
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Kind < b.Kind
	})
	v.NotScored = notScored(doc, rawOf)
	return v
}

// coverageKindType returns the source kind and the type label of a node as
// the graph builder reports unknown types: container images and CI actions
// are qualified by registry or action.
func coverageKindType(n *result.Node, rn *model.RawNode) (string, string) {
	kind := ""
	if rn != nil {
		kind = string(rn.SourceKind)
	} else if len(n.Evidence) > 0 {
		kind = string(n.Evidence[0].SourceKind)
	}
	if kind == "" {
		kind = "other"
	}
	typ := n.Type
	if rn != nil {
		attr := ""
		switch rn.Type {
		case model.TypeContainerImage:
			attr = "registry"
		case model.TypeCIAction:
			attr = "action"
		}
		if attr != "" {
			s, _ := rn.Attributes[attr].(string)
			typ = rn.Type + ":" + s
		}
	}
	return kind, typ
}

func firstLocation(n *result.Node) *CoverageLocation {
	if len(n.Evidence) == 0 {
		return nil
	}
	ev := append([]model.Evidence(nil), n.Evidence...)
	ev = model.SortEvidence(ev)
	return &CoverageLocation{Repo: ev[0].Repo, Path: ev[0].Path, Line: ev[0].Line}
}

func locLess(a, b *CoverageLocation) bool {
	if a == nil {
		return false
	}
	if b == nil {
		return true
	}
	if a.Repo != b.Repo {
		return a.Repo < b.Repo
	}
	if a.Path != b.Path {
		return a.Path < b.Path
	}
	return a.Line < b.Line
}

func sortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// sortCoverageTypes orders rows by dependents desc, then type, then source
// kind.
func sortCoverageTypes(rows []CoverageType) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Dependents != b.Dependents {
			return a.Dependents > b.Dependents
		}
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		return a.SourceKind < b.SourceKind
	})
}

func sortGaps(gs []model.Gap) {
	sort.SliceStable(gs, func(i, j int) bool {
		a, b := gs[i], gs[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Subject != b.Subject {
			return a.Subject < b.Subject
		}
		if a.Repo != b.Repo {
			return a.Repo < b.Repo
		}
		return a.Path < b.Path
	})
}

func unresolvedOrder(k model.GapKind) int {
	switch k {
	case model.GapUnresolvedModule:
		return 0
	case model.GapUnresolvedChart:
		return 1
	case model.GapRemoteKustomize:
		return 2
	case model.GapUnresolvedReference:
		return 3
	}
	return 4
}

// unresolvedWording is the neutral label of an unresolved input.
func unresolvedWording(k model.GapKind) string {
	switch k {
	case model.GapUnresolvedModule:
		return "module not available offline"
	case model.GapUnresolvedChart:
		return "chart not available offline"
	case model.GapRemoteKustomize:
		return "remote kustomize base not fetched"
	case model.GapUnresolvedReference:
		return "reference outside the scanned inputs"
	}
	return string(k)
}

// notReadWording is the neutral label of a source that was not fully read.
func notReadWording(s model.SourceStatus) string {
	switch s {
	case model.StatusFailed:
		return "could not be parsed"
	case model.StatusPartial:
		return "partly read"
	case model.StatusSkipped:
		return "skipped"
	}
	return string(s)
}

// notReadText combines the status wording with the source's user-safe note,
// without repeating the wording when the note already says it.
func notReadText(s model.Source) string {
	w := notReadWording(s.Status)
	switch {
	case s.Note == "":
		return w
	case strings.HasPrefix(s.Note, w):
		return s.Note
	}
	return w + "; " + s.Note
}

// Coverage writes the coverage view (contracts/cli.md "exitctl coverage").
// Wording is neutral: raw parser errors never appear (sources carry
// user-safe notes only and parse failure subjects are not printed).
func Coverage(w io.Writer, v *CoverageView, o Options) error {
	var b strings.Builder
	for _, l := range coverageLines(v, o) {
		b.WriteString(strings.TrimRight(l, " "))
		b.WriteByte('\n')
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func coverageLines(v *CoverageView, o Options) []string {
	out := []string{coverageHeader(v, o), confidenceLine(v, o), ""}

	// Unknown types.
	out = append(out, o.bold(fmt.Sprintf("Unknown types (%d)", len(v.UnknownTypes))))
	if len(v.UnknownTypes) == 0 {
		out = append(out, "  none: every resource type matched the catalog")
	} else {
		out = append(out, typeTable(v.UnknownTypes, v.multiRepo, false, o)...)
	}
	out = append(out, "")

	// Cloud SDK packages and clients no catalog service claims.
	if len(v.UnknownSDKs) > 0 {
		out = append(out, o.bold(fmt.Sprintf("Unmapped cloud SDKs (%d)", len(v.UnknownSDKs))))
		out = append(out, "  found in dependencies or code, but no catalog service lists them; add them to the catalog")
		nameW, subjW := 0, 0
		name := make([]string, len(v.UnknownSDKs))
		subj := make([]string, len(v.UnknownSDKs))
		for i, gp := range v.UnknownSDKs {
			name[i] = gp.Provider + "/" + gp.Service
			subj[i] = truncate(gp.Subject, 64)
			nameW = max(nameW, width(name[i]))
			subjW = max(subjW, width(subj[i]))
		}
		for i, gp := range v.UnknownSDKs {
			l := "  " + padRight(name[i], nameW) + "  " + padRight(subj[i], subjW)
			if gp.Path != "" {
				l += "  " + o.dim(repoPath(gp.Repo, gp.Path, v.multiRepo))
			}
			if gp.Count > 1 {
				l += fmt.Sprintf(" (%d times)", gp.Count)
			}
			out = append(out, l)
		}
		out = append(out, "")
	}

	// Unresolved inputs.
	out = append(out, o.bold(fmt.Sprintf("Unresolved inputs (%d)", len(v.Unresolved))))
	if len(v.Unresolved) == 0 {
		out = append(out, "  none")
	} else {
		labelW, subjW := 0, 0
		subj := make([]string, len(v.Unresolved))
		for i, gp := range v.Unresolved {
			labelW = max(labelW, width(unresolvedWording(gp.Kind)))
			subj[i] = truncate(unresolvedSubject(gp), 64)
			subjW = max(subjW, width(subj[i]))
		}
		for i, gp := range v.Unresolved {
			l := "  " + padRight(unresolvedWording(gp.Kind), labelW) + "  " + padRight(subj[i], subjW)
			if gp.Path != "" {
				l += "  " + o.dim(repoPath(gp.Repo, gp.Path, v.multiRepo))
			}
			if gp.Count > 1 {
				l += fmt.Sprintf(" (%d times)", gp.Count)
			}
			out = append(out, l)
		}
	}
	out = append(out, "")

	// Unverified knowledge.
	nUnv := 0
	for _, r := range v.Unverified {
		nUnv += r.Resources
	}
	out = append(out, o.bold(fmt.Sprintf("Unverified knowledge (%d %s, %d %s)",
		len(v.Unverified), plural(len(v.Unverified), "type", "types"), nUnv, plural(nUnv, "resource", "resources"))))
	if len(v.Unverified) == 0 {
		out = append(out, "  none")
	} else {
		out = append(out, "  matched only by draft catalog entries; not scored until the entries are verified")
		out = append(out, typeTable(v.Unverified, v.multiRepo, true, o)...)
	}
	out = append(out, "")

	// Types that deploy nothing.
	if len(v.NotScored) > 0 {
		nRes := 0
		typeW := 0
		for _, r := range v.NotScored {
			nRes += r.Resources
			typeW = max(typeW, width(r.Type))
		}
		out = append(out, o.bold(fmt.Sprintf("Not scored (%d %s, %d %s)",
			len(v.NotScored), plural(len(v.NotScored), "type", "types"), nRes, plural(nRes, "resource", "resources"))))
		out = append(out, "  deploy nothing (lookups, local rendering, metadata); left out of the score and coverage")
		for _, r := range v.NotScored {
			out = append(out, "  "+padRight(r.Type, typeW)+"  "+padLeft(fmt.Sprint(r.Resources), 5))
		}
		out = append(out, "")
	}

	// Parse failures.
	n := len(v.NotRead) + len(v.ParseGaps)
	out = append(out, o.bold(fmt.Sprintf("Parse failures (%d)", n)))
	if n == 0 {
		out = append(out, "  none: every input was read")
	} else {
		for _, s := range v.NotRead {
			out = append(out, "  "+repoPath(s.Repo, s.Path, v.multiRepo)+" ("+string(s.Kind)+"): "+notReadText(s))
		}
		for _, gp := range v.ParseGaps {
			l := "  " + repoPath(gp.Repo, gp.Path, v.multiRepo) + ": could not be parsed"
			if gp.Count > 1 {
				l += fmt.Sprintf(" (%d times)", gp.Count)
			}
			out = append(out, l)
		}
	}
	return out
}

func coverageHeader(v *CoverageView, o Options) string {
	c := v.Coverage
	s := " " + o.sep() + " "
	return o.bold("Coverage:") + " " + pct(c.Types) + " of types" + s + pct(c.Resources) + " of resources" +
		s + pct(c.Weighted) + " weighted"
}

func confidenceLine(v *CoverageView, o Options) string {
	if v.Status == statusNothingScored {
		return "  Confidence: withheld - nothing scored"
	}
	l := "  Confidence: " + v.Confidence
	switch v.Confidence {
	case confidenceWithheld:
		l += " - insufficient coverage for a grade"
	case confidenceProvisional:
		if v.Grade != "" {
			l += " - grade " + o.grade(v.Grade) + " (provisional)"
		}
	default:
		if v.Grade != "" {
			l += " - grade " + o.grade(v.Grade)
		}
	}
	if v.Range != nil {
		l += fmt.Sprintf("  range %d-%d", v.Range.Lower, v.Range.Upper)
	}
	return l
}

// typeTable renders the unknown types (or unverified knowledge) grouped by
// source kind. Groups are ordered by their highest dependents count, then
// kind; rows inside a group keep the dependents desc, type order.
func typeTable(rows []CoverageType, multiRepo, entries bool, o Options) []string {
	type group struct {
		kind string
		rows []CoverageType
	}
	var groups []*group
	idx := map[string]*group{}
	for _, r := range rows { // rows are already sorted by dependents desc
		g := idx[r.SourceKind]
		if g == nil {
			g = &group{kind: r.SourceKind}
			idx[r.SourceKind] = g
			groups = append(groups, g)
		}
		g.rows = append(g.rows, r)
	}
	sort.SliceStable(groups, func(i, j int) bool {
		a, b := groups[i].rows[0].Dependents, groups[j].rows[0].Dependents
		if a != b {
			return a > b
		}
		return groups[i].kind < groups[j].kind
	})

	const resTitle, depTitle, exTitle = "resources", "dependents", "example"
	example := func(r CoverageType) string {
		if r.Example == nil {
			return "-"
		}
		return repoPath(r.Example.Repo, r.Example.Path, multiRepo) + ":" + strconv.Itoa(r.Example.Line)
	}
	// Rows are indented two more columns than their group heading.
	typeW, exW := 0, width(exTitle)
	for _, r := range rows {
		typeW = max(typeW, 2+width(truncate(r.Type, maxCoverageTypeWidth)))
		exW = max(exW, width(example(r)))
	}
	for _, g := range groups {
		typeW = max(typeW, width(g.kind))
	}
	var out []string
	for _, g := range groups {
		h := "  " + o.bold(padRight(g.kind, typeW)) + "  " + o.dim(resTitle) + "  " + o.dim(depTitle) + "  "
		if entries {
			h += o.dim(padRight(exTitle, exW)) + "  " + o.dim("draft entry")
		} else {
			h += o.dim(exTitle)
		}
		out = append(out, h)
		for _, r := range g.rows {
			l := "    " + padRight(truncate(r.Type, maxCoverageTypeWidth), typeW-2) + "  " +
				padLeft(strconv.Itoa(r.Resources), width(resTitle)) + "  " +
				padLeft(strconv.Itoa(r.Dependents), width(depTitle)) + "  "
			if entries {
				l += padRight(example(r), exW) + "  " + strings.Join(r.Entries, ", ")
			} else {
				l += example(r)
			}
			out = append(out, l)
		}
	}
	return out
}

// unresolvedSubject shortens canonical node IDs of unresolved references to
// the address after '#'.
func unresolvedSubject(gp model.Gap) string {
	s := gp.Subject
	if gp.Kind == model.GapUnresolvedReference {
		if i := strings.LastIndex(s, "#"); i >= 0 && i < len(s)-1 {
			s = s[i+1:]
		}
	}
	if s == "" {
		s = "-"
	}
	return s
}

func repoPath(repo, path string, multiRepo bool) string {
	if multiRepo && repo != "" {
		return repo + "/" + path
	}
	return path
}

// notScored groups context nodes (types that deploy nothing) by type, most
// resources first.
func notScored(doc *result.Document, rawOf func(*result.Node) *model.RawNode) []CoverageType {
	type key struct{ kind, typ string }
	by := map[key]*CoverageType{}
	for i := range doc.Graph.Nodes {
		n := &doc.Graph.Nodes[i]
		if n.Class != classContext {
			continue
		}
		kind, typ := coverageKindType(n, rawOf(n))
		k := key{kind, typ}
		if by[k] == nil {
			by[k] = &CoverageType{SourceKind: kind, Type: typ}
		}
		by[k].Resources++
		by[k].Nodes = append(by[k].Nodes, n.ID)
	}
	out := make([]CoverageType, 0, len(by))
	for _, r := range by {
		sort.Strings(r.Nodes)
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Resources != out[j].Resources {
			return out[i].Resources > out[j].Resources
		}
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		return out[i].SourceKind < out[j].SourceKind
	})
	return out
}
