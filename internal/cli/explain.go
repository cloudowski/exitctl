package cli

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cloudowski/exitctl/internal/catalog"
	"github.com/cloudowski/exitctl/internal/graph"
	"github.com/cloudowski/exitctl/internal/render/terminal"
	"github.com/cloudowski/exitctl/internal/result"
)

func init() { register(newExplainCmd) }

func newExplainCmd(app *App) *cobra.Command {
	var all bool
	var format string
	cmd := &cobra.Command{
		Use:   "explain <rank|resource>",
		Short: "Explain one anchor: evidence, dependents, chains, alternatives",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runExplain(args[0], all, format)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "show the full dependents tree")
	cmd.Flags().StringVar(&format, "format", "text", "output format: text or json")
	return cmd
}

// explainJSON is the --format json shape of explain.
type explainJSON struct {
	Node         *result.Node             `json:"node"`
	Anchor       *result.Anchor           `json:"anchor,omitempty"`
	Dependents   []string                 `json:"dependents"`
	Tree         *graph.TreeNode          `json:"tree,omitempty"`
	Chains       [][]string               `json:"chains"`
	Wave         int                      `json:"wave"`
	WaveMembers  []string                 `json:"must_move_together,omitempty"`
	Alternatives []result.AlternativeView `json:"alternatives"`
}

func (app *App) runExplain(arg string, all bool, format string) error {
	doc, err := app.loadInput()
	if err != nil {
		return err
	}
	node, anchor, err := resolveTarget(doc, arg)
	if err != nil {
		return err
	}
	g := terminal.GraphFromDocument(doc)
	var chains [][]string
	for _, c := range doc.Analysis.Chains {
		chains = append(chains, c.Nodes)
	}
	data := terminal.ExplainData{
		Node:       node,
		Anchor:     anchor,
		Tree:       g.BlastRadiusTree(node.ID),
		Chains:     g.ChainsThroughNode(chains, node.ID, 3),
		Addresses:  terminal.Addresses(doc),
		Exceptions: terminal.ExceptionsByID(doc),
	}
	if node.SCC > 0 {
		for _, s := range doc.Graph.SCCs {
			if s.ID == node.SCC {
				for _, m := range s.Members {
					if m != node.ID {
						data.WaveMembers = append(data.WaveMembers, m)
					}
				}
			}
		}
	}
	// Alternatives: anchors carry them; otherwise ask the catalog when possible.
	var ix *catalog.Index
	if anchor != nil {
		data.Alternatives = anchor.Alternatives
	}
	if ix, err = app.loadIndex(); err == nil {
		if data.Alternatives == nil && node.Service != "" {
			data.Alternatives = result.AlternativeViews(ix, ix.Alternatives(node.Service))
		}
		data.Traits = map[string]*catalog.Trait{}
		for _, f := range node.Findings {
			if t := ix.Trait(f.Trait); t != nil {
				data.Traits[f.Trait] = t
			}
		}
	} else {
		ix = nil // explain still works without a usable catalog
	}

	if format == "json" {
		out := explainJSON{Node: node, Anchor: anchor, Dependents: g.Dependents(node.ID), Tree: data.Tree,
			Chains: data.Chains, Wave: node.Wave, WaveMembers: data.WaveMembers, Alternatives: data.Alternatives}
		if out.Dependents == nil {
			out.Dependents = []string{}
		}
		if out.Chains == nil {
			out.Chains = [][]string{}
		}
		if out.Alternatives == nil {
			out.Alternatives = []result.AlternativeView{}
		}
		enc := json.NewEncoder(app.Out)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	if format != "text" {
		return Errorf(ExitInvalidInput, "unknown --format %q (use text or json)", format)
	}
	return terminal.Explain(app.Out, data, app.termOptions(ix, all))
}

// resolveTarget finds the node for a rank or a resource reference: native
// address, canonical ID, alias, or a unique suffix of the address.
func resolveTarget(doc *result.Document, arg string) (*result.Node, *result.Anchor, error) {
	anchorOf := func(id string) *result.Anchor {
		for i := range doc.Analysis.Anchors {
			if doc.Analysis.Anchors[i].Node == id {
				return &doc.Analysis.Anchors[i]
			}
		}
		return nil
	}
	if rank, err := strconv.Atoi(arg); err == nil {
		for i := range doc.Analysis.Anchors {
			a := &doc.Analysis.Anchors[i]
			if a.Rank == rank {
				return doc.NodeByID(a.Node), a, nil
			}
		}
		return nil, nil, Errorf(ExitInvalidInput, "no anchor with rank %d (there are %d anchors)", rank, len(doc.Analysis.Anchors))
	}
	if n := doc.NodeByID(arg); n != nil {
		return n, anchorOf(n.ID), nil
	}
	var exact, suffix []*result.Node
	for i := range doc.Graph.Nodes {
		n := &doc.Graph.Nodes[i]
		switch {
		case n.NativeAddress == arg:
			exact = append(exact, n)
		case strings.HasSuffix(n.NativeAddress, arg) || strings.HasSuffix(n.ID, arg):
			suffix = append(suffix, n)
		}
	}
	cands := exact
	if len(cands) == 0 {
		cands = suffix
	}
	if len(cands) == 0 {
		cands = byLocation(doc, arg)
	}
	if len(cands) == 1 {
		return cands[0], anchorOf(cands[0].ID), nil
	}
	if len(cands) > 1 {
		names := make([]string, 0, len(cands))
		for _, n := range cands {
			names = append(names, n.ID)
		}
		sort.Strings(names)
		if len(names) > 5 {
			names = names[:5]
		}
		return nil, nil, Errorf(ExitInvalidInput, "%q matches several resources; use one of: %s", arg, strings.Join(names, ", "))
	}
	return nil, nil, Errorf(ExitInvalidInput, "%s", notFound(doc, arg))
}

// byLocation resolves the "<location>: <address>" form printed by
// "scan --wide" for addresses shared by several anchors: nodes with that
// display address whose source location contains the location as whole
// path segments.
func byLocation(doc *result.Document, arg string) []*result.Node {
	loc, addr, ok := strings.Cut(arg, ":")
	loc, addr = strings.Trim(strings.TrimSpace(loc), "/"), strings.TrimSpace(addr)
	if !ok || loc == "" || addr == "" {
		return nil
	}
	var out []*result.Node
	for i := range doc.Graph.Nodes {
		n := &doc.Graph.Nodes[i]
		if terminal.Address(n) != addr {
			continue
		}
		path, _, _ := strings.Cut(n.ID, "#")
		if strings.Contains("/"+strings.ReplaceAll(path, "//", "/")+"/", "/"+loc+"/") {
			out = append(out, n)
		}
	}
	return out
}

func notFound(doc *result.Document, arg string) string {
	type cand struct {
		addr string
		d    int
	}
	var cs []cand
	for _, n := range doc.Graph.Nodes {
		if n.NativeAddress == "" {
			continue
		}
		cs = append(cs, cand{n.NativeAddress, levenshtein(arg, n.NativeAddress)})
	}
	sort.SliceStable(cs, func(i, j int) bool {
		if cs[i].d != cs[j].d {
			return cs[i].d < cs[j].d
		}
		return cs[i].addr < cs[j].addr
	})
	msg := "no resource " + strconv.Quote(arg) + " in the result"
	var sugg []string
	for _, c := range cs {
		if len(sugg) == 3 {
			break
		}
		if len(sugg) > 0 && sugg[len(sugg)-1] == c.addr {
			continue
		}
		sugg = append(sugg, c.addr)
	}
	if len(sugg) > 0 {
		msg += "; did you mean: " + strings.Join(sugg, ", ")
	}
	return msg
}

func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}
