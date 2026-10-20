package result

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/cloudowski/exitctl/internal/catalog"
	"github.com/cloudowski/exitctl/internal/model"
)

// NamedDoc is a decoded merge input together with the name (usually the file
// path) used in messages.
type NamedDoc struct {
	Name string
	Doc  *Document
}

// LoadAll loads and validates every path in argument order. The first error
// is returned as is, so an unsupported format surfaces as *IncompatibleError
// naming the file.
func LoadAll(paths []string) ([]NamedDoc, error) {
	out := make([]NamedDoc, 0, len(paths))
	for _, p := range paths {
		d, err := Load(p)
		if err != nil {
			return nil, err
		}
		out = append(out, NamedDoc{Name: p, Doc: d})
	}
	return out, nil
}

// ServiceResolver maps a service ID to its current canonical ID (following
// catalog aliases and successors). It returns the input when it is unknown.
type ServiceResolver func(id string) string

// CatalogResolver returns a ServiceResolver backed by the catalog index. A nil
// index yields the identity resolver.
func CatalogResolver(ix *catalog.Index) ServiceResolver {
	return func(id string) string {
		if ix == nil || id == "" {
			return id
		}
		if s := ix.ServiceByID(id); s != nil {
			return s.ID
		}
		return id
	}
}

// MergeFacts unions the facts of result and graph documents (FR-076, DM Part 5
// "Merge") and returns them with the flattened inputs, ready for
// engine.Analyze. The output does not depend on the order of docs:
//
//   - documents are processed sorted by their first input repo, then by the
//     canonical encoding of their facts and inputs;
//   - sources are keyed by repo + path + kind (one file may be read by more
//     than one detector); the first document in processing order wins;
//   - raw nodes are keyed by ID; the node from the first document in
//     processing order is kept and the evidence of every duplicate is added
//     to it;
//   - raw edges, package uses, code uses and gaps are deduplicated by their
//     full value;
//   - CodeUse.Service is re-resolved through resolve (nil = unchanged), so
//     facts recorded with a renamed or superseded service ID match the
//     current catalog;
//   - inputs of every document are flattened (a merged document may carry
//     several), keep their original versions, are deduplicated and sorted by
//     repo.
//
// Only documents of kind "result" and "graph" are accepted.
func MergeFacts(docs []NamedDoc, resolve ServiceResolver) (model.Facts, []Input, error) {
	if len(docs) == 0 {
		return model.Facts{}, nil, fmt.Errorf("merge needs at least one result file")
	}
	type entry struct {
		doc      NamedDoc
		repo     string
		tiebreak string
	}
	entries := make([]entry, 0, len(docs))
	for _, nd := range docs {
		if nd.Doc == nil {
			return model.Facts{}, nil, fmt.Errorf("%s: empty document", nd.Name)
		}
		if nd.Doc.Kind != KindResult && nd.Doc.Kind != KindGraph {
			return model.Facts{}, nil, fmt.Errorf("%s has kind %q; merge accepts %q and %q documents",
				nd.Name, nd.Doc.Kind, KindResult, KindGraph)
		}
		facts := cloneFacts(nd.Doc.Facts)
		facts.Sort()
		inputs := append([]Input(nil), nd.Doc.Inputs...)
		sortInputs(inputs)
		repo := ""
		if len(inputs) > 0 {
			repo = inputs[0].Repo
		}
		entries = append(entries, entry{doc: nd, repo: repo, tiebreak: canonical(facts) + "\n" + canonical(inputs)})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].repo != entries[j].repo {
			return entries[i].repo < entries[j].repo
		}
		return entries[i].tiebreak < entries[j].tiebreak
	})

	var out model.Facts
	var inputs []Input
	sourceSeen := map[string]bool{}
	nodeAt := map[string]int{}
	seen := map[string]bool{}
	firstTime := func(kind string, v any) bool {
		k := kind + "\x00" + canonical(v)
		if seen[k] {
			return false
		}
		seen[k] = true
		return true
	}
	for _, e := range entries {
		f := cloneFacts(e.doc.Doc.Facts)
		f.Sort() // sorted evidence makes value keys canonical
		for _, s := range f.Sources {
			k := s.Repo + "\x00" + s.Path + "\x00" + string(s.Kind)
			if sourceSeen[k] {
				continue
			}
			sourceSeen[k] = true
			out.Sources = append(out.Sources, s)
		}
		for _, n := range f.RawNodes {
			if i, ok := nodeAt[n.ID]; ok {
				out.RawNodes[i].Evidence = append(out.RawNodes[i].Evidence, n.Evidence...)
				continue
			}
			nodeAt[n.ID] = len(out.RawNodes)
			out.RawNodes = append(out.RawNodes, n)
		}
		for _, r := range f.RawEdges {
			if firstTime("edge", r) {
				out.RawEdges = append(out.RawEdges, r)
			}
		}
		for _, p := range f.PackageUses {
			if firstTime("pkg", p) {
				out.PackageUses = append(out.PackageUses, p)
			}
		}
		for _, c := range f.CodeUses {
			if resolve != nil && c.Service != "" {
				c.Service = resolve(c.Service)
			}
			if firstTime("code", c) {
				out.CodeUses = append(out.CodeUses, c)
			}
		}
		for _, g := range f.Gaps {
			if firstTime("gap", g) {
				out.Gaps = append(out.Gaps, g)
			}
		}
		for _, in := range e.doc.Doc.Inputs {
			if firstTime("input", in) {
				inputs = append(inputs, in)
			}
		}
	}
	out.Sort()
	sortInputs(inputs)
	if inputs == nil {
		inputs = []Input{}
	}
	return out, inputs, nil
}

// sortInputs orders inputs by repo, then by their canonical encoding.
func sortInputs(in []Input) {
	sort.SliceStable(in, func(i, j int) bool {
		if in[i].Repo != in[j].Repo {
			return in[i].Repo < in[j].Repo
		}
		return canonical(in[i]) < canonical(in[j])
	})
}

// cloneFacts copies the slices (and per-node evidence) so merging never
// mutates the input documents.
func cloneFacts(f model.Facts) model.Facts {
	c := model.Facts{
		Sources:     append([]model.Source(nil), f.Sources...),
		RawNodes:    append([]model.RawNode(nil), f.RawNodes...),
		RawEdges:    append([]model.RawEdge(nil), f.RawEdges...),
		PackageUses: append([]model.PackageUse(nil), f.PackageUses...),
		CodeUses:    append([]model.CodeUse(nil), f.CodeUses...),
		Gaps:        append([]model.Gap(nil), f.Gaps...),
	}
	for i := range c.RawNodes {
		c.RawNodes[i].Evidence = append([]model.Evidence(nil), c.RawNodes[i].Evidence...)
	}
	for i := range c.RawEdges {
		c.RawEdges[i].Evidence = append([]model.Evidence(nil), c.RawEdges[i].Evidence...)
	}
	return c
}

// canonical is a deterministic JSON encoding used as a dedup and sort key.
// encoding/json sorts map keys, so attribute maps encode stably.
func canonical(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return fmt.Sprintf("%#v", v)
	}
	return buf.String()
}
