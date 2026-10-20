// Package result defines the result document (contracts/result.schema.json),
// its deterministic encoding, storage, and loading.
package result

import (
	"bufio"
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"io"
	"math"
	"sort"
	"strings"

	"github.com/cloudowski/exitctl/internal/catalog"
	"github.com/cloudowski/exitctl/internal/graph"
	"github.com/cloudowski/exitctl/internal/model"
)

// Document kinds.
const (
	KindResult = "result"
	KindGraph  = "graph"
)

// Versions identifies everything that influenced a result.
type Versions struct {
	Tool                  string `json:"tool"`
	Catalog               string `json:"catalog"`
	CatalogSchema         string `json:"catalog_schema"`
	Rules                 string `json:"rules"`
	ConfigFingerprint     string `json:"config_fingerprint"`
	CustomScoring         bool   `json:"custom_scoring"`
	CatalogDraftsIncluded bool   `json:"catalog_drafts_included,omitempty"`
}

// Input is one scanned repository or merged input.
type Input struct {
	Repo     string   `json:"repo"`
	AsOf     string   `json:"as_of"`
	Versions Versions `json:"versions"`
}

// Node is the serialized graph node.
type Node struct {
	ID              string             `json:"id"`
	Aliases         []string           `json:"aliases,omitempty"`
	Kind            string             `json:"kind"`
	Type            string             `json:"type"`
	NativeAddress   string             `json:"native_address,omitempty"`
	Service         string             `json:"service,omitempty"`
	Class           string             `json:"class"`
	Sources         []string           `json:"sources"`
	Evidence        []model.Evidence   `json:"evidence,omitempty"`
	Findings        []graph.Finding    `json:"findings"`
	Risk            float64            `json:"risk"`
	RiskByDimension map[string]float64 `json:"risk_by_dimension,omitempty"`
	Weight          float64            `json:"weight"`
	Contribution    float64            `json:"contribution,omitempty"`
	DependentsCount int                `json:"dependents_count"`
	Dependents      []string           `json:"dependents,omitempty"`
	Wave            int                `json:"wave"`
	SCC             int                `json:"scc,omitempty"`
	Group           string             `json:"group,omitempty"`
	Notes           []string           `json:"notes,omitempty"`
}

// Edge is the serialized graph edge: From depends on To.
type Edge struct {
	From       string           `json:"from"`
	To         string           `json:"to"`
	Kind       string           `json:"kind"`
	Confidence string           `json:"confidence"`
	Evidence   []model.Evidence `json:"evidence"`
}

// Group is a collapsed or container group of nodes.
type Group struct {
	ID           string   `json:"id"`
	Kind         string   `json:"kind"`
	Label        string   `json:"label"`
	Members      []string `json:"members"`
	Parent       string   `json:"parent,omitempty"`
	MaxRisk      float64  `json:"max_risk,omitempty"`
	Contribution float64  `json:"contribution,omitempty"`
}

// SCC is a cycle group with more than one member.
type SCC struct {
	ID      int      `json:"id"`
	Members []string `json:"members"`
}

// Graph section.
type Graph struct {
	Nodes  []Node  `json:"nodes"`
	Edges  []Edge  `json:"edges"`
	Groups []Group `json:"groups"`
	SCCs   []SCC   `json:"sccs"`
}

// AlternativeView is an alternative as shown to users.
type AlternativeView struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	Difficulty    string `json:"difficulty"`
	Compatibility string `json:"compatibility"`
	Recommended   bool   `json:"recommended"`
	Notes         string `json:"notes,omitempty"`
}

// Anchor in the analysis.
type Anchor struct {
	Rank            int               `json:"rank"`
	Node            string            `json:"node"`
	Contribution    float64           `json:"contribution"`
	DependentsCount int               `json:"dependents_count"`
	ComponentsUsing int               `json:"components_using"`
	SDKFiles        int               `json:"sdk_files"`
	Alternatives    []AlternativeView `json:"alternatives"`
	Reason          string            `json:"reason,omitempty"`
}

// Lever is the biggest single replacement.
type Lever struct {
	Service     string `json:"service"`
	Alternative string `json:"alternative"`
	Delta       int    `json:"delta"`
}

// Range is the index range given unknown resources.
type Range struct {
	Lower int `json:"lower"`
	Upper int `json:"upper"`
}

// Coverage in the analysis.
type Coverage struct {
	Types             float64  `json:"types"`
	Resources         float64  `json:"resources"`
	Weighted          float64  `json:"weighted"`
	UnknownTypes      int      `json:"unknown_types"`
	UnverifiedMatches []string `json:"unverified_matches,omitempty"`
}

// Wave groups units (each unit is a list of node IDs that move together).
type Wave struct {
	Wave  int        `json:"wave"`
	Units [][]string `json:"units"`
}

// Chain is a lock-in chain.
type Chain struct {
	Nodes []string `json:"nodes"`
}

// ExceptionState reports an exception from the configuration.
type ExceptionState struct {
	ID              string   `json:"id"`
	State           string   `json:"state"`
	Reason          string   `json:"reason"`
	Owner           string   `json:"owner"`
	Expires         string   `json:"expires,omitempty"`
	MatchedFindings []string `json:"matched_findings"`
}

// Analysis section.
type Analysis struct {
	Status                 string           `json:"status"`
	Index                  *int             `json:"index,omitempty"`
	IndexWithoutExceptions *int             `json:"index_without_exceptions,omitempty"`
	Range                  *Range           `json:"range,omitempty"`
	Grade                  string           `json:"grade,omitempty"`
	Confidence             string           `json:"confidence,omitempty"`
	Dimensions             map[string]*int  `json:"dimensions,omitempty"`
	Anchors                []Anchor         `json:"anchors"`
	Lever                  *Lever           `json:"lever"`
	Coverage               Coverage         `json:"coverage"`
	Waves                  []Wave           `json:"waves"`
	Chains                 []Chain          `json:"chains"`
	Exceptions             []ExceptionState `json:"exceptions"`
	Notes                  []string         `json:"notes"`
}

// Document is a scan, merge, or graph export result.
type Document struct {
	Format   string      `json:"format"`
	Kind     string      `json:"kind"`
	Versions Versions    `json:"versions"`
	Inputs   []Input     `json:"inputs"`
	Facts    model.Facts `json:"facts"`
	Graph    Graph       `json:"graph"`
	Analysis Analysis    `json:"analysis"`
}

// NodeByID returns the serialized node with the given ID or alias.
func (d *Document) NodeByID(id string) *Node {
	for i := range d.Graph.Nodes {
		n := &d.Graph.Nodes[i]
		if n.ID == id {
			return n
		}
		for _, a := range n.Aliases {
			if a == id {
				return n
			}
		}
	}
	return nil
}

// DisplayAddress returns the address of a node as shown to users. A
// component at a repository root is named after its repository, so merged
// results stay readable; core-group Kubernetes objects drop the empty group
// prefix ("Namespace/_/shop", not "/Namespace/_/shop").
func DisplayAddress(n *Node) string {
	switch {
	case n.Kind == "component":
		dir := n.NativeAddress
		if dir == "" || dir == "." {
			if repo, _, ok := strings.Cut(n.ID, "//"); ok && repo != "" {
				dir = repo
			}
		}
		return "component " + dir
	case n.Kind == "implied_service":
		return n.Service + " (used by code, not declared)"
	case n.Class == "context" && n.NativeAddress != "":
		return strings.TrimPrefix(n.NativeAddress, "/") + " (not scored)"
	case n.NativeAddress != "":
		return strings.TrimPrefix(n.NativeAddress, "/")
	}
	return n.ID
}

// AlternativeViews converts catalog mappings (already ordered) to views.
func AlternativeViews(ix *catalog.Index, ms []catalog.AlternativeMapping) []AlternativeView {
	out := make([]AlternativeView, 0, len(ms))
	for _, m := range ms {
		v := AlternativeView{ID: m.Ref, Name: m.Ref, Difficulty: m.Difficulty,
			Compatibility: m.Compatibility, Recommended: m.Recommended, Notes: m.Notes}
		if ix != nil {
			if a := ix.Alternative(m.Ref); a != nil {
				v.Name, v.Kind = a.Name, a.Kind
			}
		}
		out = append(out, v)
	}
	return out
}

// Normalize sorts every array by its documented key and replaces nil slices
// with empty ones so that encoding is byte-stable.
func (d *Document) Normalize() {
	d.Facts.Sort()
	sort.SliceStable(d.Inputs, func(i, j int) bool { return d.Inputs[i].Repo < d.Inputs[j].Repo })
	g := &d.Graph
	sort.SliceStable(g.Nodes, func(i, j int) bool { return g.Nodes[i].ID < g.Nodes[j].ID })
	for i := range g.Nodes {
		n := &g.Nodes[i]
		sort.Strings(n.Aliases)
		sort.Strings(n.Sources)
		sort.Strings(n.Dependents)
		sort.Strings(n.Notes)
		if n.Sources == nil {
			n.Sources = []string{}
		}
		if n.Findings == nil {
			n.Findings = []graph.Finding{}
		}
		sort.SliceStable(n.Findings, func(a, b int) bool { return n.Findings[a].ID < n.Findings[b].ID })
	}
	sort.SliceStable(g.Edges, func(i, j int) bool {
		a, b := g.Edges[i], g.Edges[j]
		if a.From != b.From {
			return a.From < b.From
		}
		if a.To != b.To {
			return a.To < b.To
		}
		return a.Kind < b.Kind
	})
	for i := range g.Edges {
		if g.Edges[i].Evidence == nil {
			g.Edges[i].Evidence = []model.Evidence{}
		}
	}
	sort.SliceStable(g.Groups, func(i, j int) bool { return g.Groups[i].ID < g.Groups[j].ID })
	sort.SliceStable(g.SCCs, func(i, j int) bool { return g.SCCs[i].ID < g.SCCs[j].ID })
	if g.Nodes == nil {
		g.Nodes = []Node{}
	}
	if g.Edges == nil {
		g.Edges = []Edge{}
	}
	if g.Groups == nil {
		g.Groups = []Group{}
	}
	if g.SCCs == nil {
		g.SCCs = []SCC{}
	}
	a := &d.Analysis
	if a.Anchors == nil {
		a.Anchors = []Anchor{}
	}
	for i := range a.Anchors {
		if a.Anchors[i].Alternatives == nil {
			a.Anchors[i].Alternatives = []AlternativeView{}
		}
	}
	if a.Waves == nil {
		a.Waves = []Wave{}
	}
	if a.Chains == nil {
		a.Chains = []Chain{}
	}
	if a.Exceptions == nil {
		a.Exceptions = []ExceptionState{}
	}
	sort.SliceStable(a.Exceptions, func(i, j int) bool { return a.Exceptions[i].ID < a.Exceptions[j].ID })
	if a.Notes == nil {
		a.Notes = []string{}
	}
	sort.Strings(a.Notes)
	sort.Strings(a.Coverage.UnverifiedMatches)
}

// Encode writes the document as deterministic, indented JSON with a trailing
// newline. encoding/json sorts map keys; Normalize sorts slices.
//
// The document is streamed to w with v1 semantics (byte-identical to
// json.Encoder with SetEscapeHTML(false) and two-space indent) instead of
// being marshaled and then re-indented in memory: for large graphs the
// buffered marshal+indent dominated scan time and peak memory (T097).
func Encode(w io.Writer, d *Document) error {
	d.Normalize()
	bw := bufio.NewWriterSize(w, 256<<10)
	if err := jsonv2.MarshalWrite(bw, d,
		json.DefaultOptionsV1(),
		jsontext.EscapeForHTML(false),
		jsontext.WithIndent("  "),
	); err != nil {
		return err
	}
	if err := bw.WriteByte('\n'); err != nil {
		return err
	}
	return bw.Flush()
}

// Marshal returns the encoded document.
func Marshal(d *Document) ([]byte, error) {
	var buf bytes.Buffer
	if err := Encode(&buf, d); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Round4 rounds to 4 decimal places (stored floats, research R15).
func Round4(x float64) float64 { return math.Floor(x*10000+0.5) / 10000 }

// IntPtr returns a pointer to v.
func IntPtr(v int) *int { return &v }
