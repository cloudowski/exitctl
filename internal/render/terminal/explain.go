package terminal

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/cloudowski/exitctl/internal/catalog"
	"github.com/cloudowski/exitctl/internal/graph"
	"github.com/cloudowski/exitctl/internal/result"
)

// MaxTreeLines caps the dependents tree in explain unless Options.All is set.
const MaxTreeLines = 10

// ExplainData is everything explain shows for one node.
type ExplainData struct {
	Node         *result.Node
	Anchor       *result.Anchor // nil when the node is not an anchor
	Tree         *graph.TreeNode
	Chains       [][]string
	WaveMembers  []string // other members of the node's cycle group
	Alternatives []result.AlternativeView
	Traits       map[string]*catalog.Trait // trait ID -> trait (names, explanations)
	Addresses    map[string]string         // node ID -> display address
	// Exceptions maps exception ID -> state, for the reason and owner of
	// accepted findings (nil = show only the exception ID).
	Exceptions map[string]result.ExceptionState
}

// ExceptionsByID indexes the exceptions of a result by ID.
func ExceptionsByID(doc *result.Document) map[string]result.ExceptionState {
	m := make(map[string]result.ExceptionState, len(doc.Analysis.Exceptions))
	for _, e := range doc.Analysis.Exceptions {
		m[e.ID] = e
	}
	return m
}

// GraphFromDocument rebuilds the in-memory graph of a saved result.
func GraphFromDocument(doc *result.Document) *graph.Graph {
	nodes := make([]*graph.Node, 0, len(doc.Graph.Nodes))
	for _, n := range doc.Graph.Nodes {
		nodes = append(nodes, &graph.Node{
			ID: n.ID, Aliases: n.Aliases, Kind: graph.NodeKind(n.Kind), Type: n.Type,
			NativeAddress: n.NativeAddress, Service: n.Service, Class: n.Class,
			Sources: n.Sources, Evidence: n.Evidence, Findings: n.Findings,
			SCC: n.SCC, Wave: n.Wave, Group: n.Group, Notes: n.Notes,
		})
	}
	edges := make([]*graph.Edge, 0, len(doc.Graph.Edges))
	for _, e := range doc.Graph.Edges {
		edges = append(edges, &graph.Edge{From: e.From, To: e.To, Kind: graph.EdgeKind(e.Kind),
			Confidence: e.Confidence, Evidence: e.Evidence})
	}
	return graph.New(nodes, edges)
}

// Addresses maps node IDs to the address shown to users.
func Addresses(doc *result.Document) map[string]string {
	m := make(map[string]string, len(doc.Graph.Nodes))
	for _, n := range doc.Graph.Nodes {
		m[n.ID] = Address(&n)
	}
	return m
}

// Address returns the display address of a node.
func Address(n *result.Node) string { return result.DisplayAddress(n) }

// Explain renders the explain view (contracts/cli.md "exitctl explain").
func Explain(w io.Writer, d ExplainData, opts Options) error {
	n := d.Node
	bw := &lineWriter{w: w}
	maxLevel, dims := levelAndDims(n.Findings)
	header := fmt.Sprintf("%s   level: %s", opts.bold(Address(n)), maxLevel)
	if len(dims) > 0 {
		header += "   dimensions: " + strings.Join(dims, ", ")
	}
	if n.Service != "" {
		header += "   service: " + explainName(opts, n.Service)
	}
	bw.line(header)
	if d.Anchor != nil {
		bw.line(fmt.Sprintf("anchor #%d   dependents: %d   weight: %.2f   risk: %.2f",
			d.Anchor.Rank, n.DependentsCount, n.Weight, n.Risk))
	}

	bw.line("")
	bw.line(opts.bold("Evidence"))
	for _, e := range evidenceOf(n) {
		bw.line(fmt.Sprintf("  %s:%d  %s", e.Path, e.Line, e.Excerpt))
	}

	if len(n.Findings) > 0 {
		bw.line("")
		bw.line(opts.bold("Findings"))
		for _, f := range n.Findings {
			name, expl := f.Trait, ""
			if t := d.Traits[f.Trait]; t != nil {
				name, expl = t.Name, t.Explanation
			}
			status := ""
			if f.Status == graph.FindingAccepted {
				status = "  [accepted"
				if f.Exception != "" {
					status += ": " + f.Exception
				}
				status += "]"
			}
			bw.line(fmt.Sprintf("  %-8s %s%s", f.Level, name, status))
			if expl != "" {
				bw.line("           " + expl)
			}
			if f.Status == graph.FindingAccepted {
				if e, ok := d.Exceptions[f.Exception]; ok {
					bw.line("           accepted by " + e.Owner + ": " + e.Reason)
					if e.Expires != "" {
						bw.line("           until " + e.Expires)
					}
				}
			}
		}
	}

	bw.line("")
	bw.line(opts.bold(fmt.Sprintf("Dependents (%d)", n.DependentsCount)))
	if d.Tree == nil || len(d.Tree.Children) == 0 {
		bw.line("  none")
	} else {
		lines := treeLines(d.Tree, d.Addresses, opts)
		limit := len(lines)
		if !opts.All && limit > MaxTreeLines {
			limit = MaxTreeLines
		}
		for _, l := range lines[:limit] {
			bw.line("  " + l)
		}
		if limit < len(lines) {
			bw.line(fmt.Sprintf("  ... %d more (--all)", len(lines)-limit))
		}
	}

	if len(d.Chains) > 0 {
		bw.line("")
		bw.line(opts.bold("Lock-in chains"))
		for _, c := range d.Chains {
			parts := make([]string, len(c))
			for i, id := range c {
				parts[i] = addr(d.Addresses, id)
			}
			bw.line("  " + strings.Join(parts, " -> "))
		}
	}

	bw.line("")
	wave := fmt.Sprintf("Migration wave: %d", n.Wave)
	if len(d.WaveMembers) > 0 {
		parts := make([]string, len(d.WaveMembers))
		for i, id := range d.WaveMembers {
			parts[i] = addr(d.Addresses, id)
		}
		wave += " (must move together with: " + strings.Join(parts, ", ") + ")"
	}
	bw.line(wave)

	bw.line("")
	bw.line(opts.bold("Alternatives"))
	if len(d.Alternatives) == 0 {
		bw.line("  none in catalog")
	}
	for _, a := range d.Alternatives {
		name := a.Name
		if a.Recommended {
			name = opts.recommended() + " " + name
		}
		line := fmt.Sprintf("  %-28s difficulty: %-9s %s", name, a.Difficulty, a.Compatibility)
		bw.line(strings.TrimRight(line, " "))
		if a.Notes != "" {
			bw.line("    " + a.Notes)
		}
	}

	if d.Anchor != nil && d.Anchor.Reason != "" {
		bw.line("")
		bw.line("Why this is an anchor: " + d.Anchor.Reason)
	}
	return bw.err
}

type lineWriter struct {
	w   io.Writer
	err error
}

func (l *lineWriter) line(s string) {
	if l.err != nil {
		return
	}
	_, l.err = fmt.Fprintln(l.w, s)
}

func explainName(opts Options, id string) string {
	if v, ok := opts.Names[id]; ok && v != "" {
		return v
	}
	return id
}

func addr(m map[string]string, id string) string {
	if a, ok := m[id]; ok {
		return a
	}
	return id
}

func evidenceOf(n *result.Node) []struct {
	Path, Excerpt string
	Line          int
} {
	type ev = struct {
		Path, Excerpt string
		Line          int
	}
	seen := map[string]bool{}
	var out []ev
	add := func(path string, line int, ex string) {
		k := fmt.Sprintf("%s:%d", path, line)
		if seen[k] {
			return
		}
		seen[k] = true
		out = append(out, ev{path, ex, line})
	}
	for _, e := range n.Evidence {
		add(e.Path, e.Line, e.Excerpt)
	}
	for _, f := range n.Findings {
		for _, e := range f.Evidence {
			add(e.Path, e.Line, e.Excerpt)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Line < out[j].Line
	})
	return out
}

func levelAndDims(fs []graph.Finding) (string, []string) {
	best := catalog.LevelNone
	dims := map[string]bool{}
	for _, f := range fs {
		if f.Status != graph.FindingActive {
			continue
		}
		if catalog.LevelRank(f.Level) > catalog.LevelRank(best) {
			best = f.Level
		}
		for _, d := range f.Dimensions {
			dims[string(d)] = true
		}
	}
	var ds []string
	for _, d := range catalog.AllDimensions {
		if dims[string(d)] {
			ds = append(ds, string(d))
		}
	}
	return string(best), ds
}

// treeLines renders the tree below the root with box-drawing characters
// (ASCII when !UTF8).
func treeLines(root *graph.TreeNode, addrs map[string]string, opts Options) []string {
	branch, last, pipe, space := "├─ ", "└─ ", "│  ", "   "
	if !opts.UTF8 {
		branch, last, pipe = "|- ", "`- ", "|  "
	}
	var out []string
	var walk func(n *graph.TreeNode, prefix string)
	walk = func(n *graph.TreeNode, prefix string) {
		for i, c := range n.Children {
			conn, next := branch, pipe
			if i == len(n.Children)-1 {
				conn, next = last, space
			}
			out = append(out, prefix+conn+addr(addrs, c.ID))
			walk(c, prefix+next)
		}
	}
	walk(root, "")
	return out
}
