package cloudformation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/cloudowski/exitctl/internal/model"
	"github.com/cloudowski/exitctl/internal/redact"
	"github.com/cloudowski/exitctl/internal/source"
	"github.com/cloudowski/exitctl/internal/walk"
)

// New returns the CloudFormation detector: one RawNode per Resources entry
// and edges from Ref, Fn::GetAtt, Fn::Sub variables, and DependsOn.
func New() source.Detector { return detector{} }

type detector struct{}

func (detector) Name() string { return "cloudformation" }

func (detector) Detect(ctx context.Context, idx walk.Index, opts source.Options) (model.Facts, error) {
	var facts model.Facts
	for _, rel := range idx.Files[model.KindCloudFormation] {
		if err := ctx.Err(); err != nil {
			return facts, err
		}
		facts.Append(detectFile(idx.Root, rel, opts.Repo))
	}
	return facts, nil
}

// identityProps are properties whose literal value names the physical
// resource and becomes the provider identity.
var identityProps = []string{"TableName", "BucketName", "QueueName", "FunctionName", "TopicName"}

type template struct {
	repo, path string
	lines      []string
	params     map[string]bool
	resources  map[string]bool
	edges      map[edgeKey]*model.RawEdge
	gaps       map[string]int
}

type edgeKey struct{ from, to, attr, hint string }

func detectFile(root, rel, repo string) model.Facts {
	var facts model.Facts
	kind := model.KindCloudFormation
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		facts.Sources = append(facts.Sources, source.FailedSource(repo, rel, kind, "file could not be read"))
		return facts
	}
	doc, err := parse(data)
	if err != nil {
		facts.Sources = append(facts.Sources, source.FailedSource(repo, rel, kind, "could not be parsed as a CloudFormation template"))
		return facts
	}
	t := &template{
		repo: repo, path: rel,
		lines:     strings.Split(string(data), "\n"),
		params:    map[string]bool{},
		resources: map[string]bool{},
		edges:     map[edgeKey]*model.RawEdge{},
		gaps:      map[string]int{},
	}
	status, note := model.StatusParsed, ""
	if p := get(doc, "Parameters"); p != nil && p.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(p.Content); i += 2 {
			t.params[p.Content[i].Value] = true
		}
	}
	res := get(doc, "Resources")
	if res != nil && res.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(res.Content); i += 2 {
			t.resources[res.Content[i].Value] = true
		}
		for i := 0; i+1 < len(res.Content); i += 2 {
			key, body := res.Content[i], res.Content[i+1]
			if strings.HasPrefix(key.Value, "Fn::ForEach::") {
				status, note = model.StatusPartial, "Fn::ForEach loops are not expanded"
				continue
			}
			n, ok := t.resource(key, body)
			if !ok {
				if note == "" {
					status, note = model.StatusPartial, "some resources have no type"
				}
				continue
			}
			facts.RawNodes = append(facts.RawNodes, n)
		}
	}
	keys := make([]edgeKey, 0, len(t.edges))
	for k := range t.edges {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		return fmt.Sprint(a) < fmt.Sprint(b)
	})
	for _, k := range keys {
		facts.RawEdges = append(facts.RawEdges, *t.edges[k])
	}
	subjects := make([]string, 0, len(t.gaps))
	for s := range t.gaps {
		subjects = append(subjects, s)
	}
	sort.Strings(subjects)
	for _, s := range subjects {
		facts.Gaps = append(facts.Gaps, model.Gap{Kind: model.GapUnresolvedReference, Subject: s, Repo: repo, Path: rel, Count: t.gaps[s]})
	}
	facts.Sources = append(facts.Sources, model.Source{Repo: repo, Path: rel, Kind: kind, Status: status, Note: note})
	return facts
}

func (t *template) id(logicalID string) string {
	return model.CanonicalID(t.repo, t.path, logicalID)
}

func (t *template) evidence(line int) model.Evidence {
	raw := ""
	if line >= 1 && line <= len(t.lines) {
		raw = t.lines[line-1]
	}
	return model.NewEvidence(t.repo, t.path, line, redact.Line(raw), model.KindCloudFormation)
}

func (t *template) resource(key, body *yaml.Node) (model.RawNode, bool) {
	typ, ok := scalar(get(body, "Type"))
	if !ok || typ == "" {
		return model.RawNode{}, false
	}
	logical := key.Value
	attrs := map[string]any{}
	n := model.RawNode{
		ID:            t.id(logical),
		NodeKind:      model.NodeResource,
		SourceKind:    model.KindCloudFormation,
		Type:          typ,
		NativeAddress: logical,
		Attributes:    attrs,
		Multiplicity:  model.MultOne,
		Evidence:      []model.Evidence{t.evidence(key.Line)},
	}
	if props := get(body, "Properties"); props != nil && props.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(props.Content); i += 2 {
			name, val := props.Content[i].Value, props.Content[i+1]
			attrs[name] = redact.ResourceValue(typ, name, literal(val))
			t.refs(logical, name, val)
		}
		for _, p := range identityProps {
			if s, ok := attrs[p].(string); ok && s != "" {
				n.ProviderIdentity = "aws:" + typ + "/" + s
				break
			}
		}
	}
	if dep := get(body, "DependsOn"); dep != nil {
		deps := []*yaml.Node{dep}
		if dep.Kind == yaml.SequenceNode {
			deps = dep.Content
		}
		for _, d := range deps {
			name, ok := scalar(d)
			if !ok {
				continue
			}
			if t.resources[name] {
				t.edge(logical, name, "DependsOn", "ordering", d.Line)
			} else {
				t.gaps[name]++
			}
		}
	}
	return n, true
}

func (t *template) edge(from, to, attr, hint string, line int) {
	if from == to {
		return
	}
	k := edgeKey{from, to, attr, hint}
	ev := t.evidence(line)
	if e, ok := t.edges[k]; ok {
		e.Evidence = append(e.Evidence, ev)
		return
	}
	t.edges[k] = &model.RawEdge{From: t.id(from), To: t.id(to), Attribute: attr, KindHint: hint, Evidence: []model.Evidence{ev}}
}

// target links a reference to name: resources become edges, parameters and
// pseudo parameters are ignored, anything else is an unresolved reference.
func (t *template) target(from, attr, name string, line int) {
	switch {
	case name == "":
	case t.resources[name]:
		t.edge(from, name, attr, "", line)
	case t.params[name] || strings.HasPrefix(name, "AWS::"):
	default:
		t.gaps[name]++
	}
}

var reSubVar = regexp.MustCompile(`\$\{([^}]*)\}`)

// refs walks a property value and records references to other resources.
func (t *template) refs(from, attr string, n *yaml.Node) {
	if n == nil {
		return
	}
	switch n.Kind {
	case yaml.SequenceNode:
		for _, c := range n.Content {
			t.refs(from, attr, c)
		}
	case yaml.MappingNode:
		if len(n.Content) == 2 {
			fn, arg := n.Content[0].Value, n.Content[1]
			switch fn {
			case "Ref":
				if s, ok := scalar(arg); ok {
					t.target(from, attr, s, n.Line)
					return
				}
			case "Fn::GetAtt":
				var res string
				if s, ok := scalar(arg); ok {
					res, _, _ = strings.Cut(s, ".")
				} else if arg.Kind == yaml.SequenceNode && len(arg.Content) > 0 {
					res, _ = scalar(arg.Content[0])
				}
				if res != "" {
					t.target(from, attr, res, n.Line)
				}
				if arg.Kind == yaml.SequenceNode && len(arg.Content) > 1 {
					t.refs(from, attr, arg.Content[1])
				}
				return
			case "Fn::Sub":
				str, locals := arg, (*yaml.Node)(nil)
				if arg.Kind == yaml.SequenceNode && len(arg.Content) > 0 {
					str = arg.Content[0]
					if len(arg.Content) > 1 {
						locals = arg.Content[1]
					}
				}
				local := map[string]bool{}
				if locals != nil && locals.Kind == yaml.MappingNode {
					for i := 0; i+1 < len(locals.Content); i += 2 {
						local[locals.Content[i].Value] = true
						t.refs(from, attr, locals.Content[i+1])
					}
				}
				if s, ok := scalar(str); ok {
					for _, m := range reSubVar.FindAllStringSubmatch(s, -1) {
						v := strings.TrimSpace(m[1])
						if v == "" || strings.HasPrefix(v, "!") {
							continue
						}
						name, _, _ := strings.Cut(v, ".")
						if local[v] || local[name] {
							continue
						}
						t.target(from, attr, name, str.Line)
					}
				}
				return
			}
		}
		for i := 1; i < len(n.Content); i += 2 {
			t.refs(from, attr, n.Content[i])
		}
	}
}

// literal converts a value node to an attribute value; intrinsic functions
// become model.Unknown.
func literal(n *yaml.Node) any {
	switch n.Kind {
	case yaml.ScalarNode:
		var v any
		if err := n.Decode(&v); err != nil {
			return n.Value
		}
		return v
	case yaml.SequenceNode:
		out := make([]any, 0, len(n.Content))
		for _, c := range n.Content {
			out = append(out, literal(c))
		}
		return out
	case yaml.MappingNode:
		if isIntrinsic(n) {
			return model.Unknown
		}
		out := make(map[string]any, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			out[n.Content[i].Value] = literal(n.Content[i+1])
		}
		return out
	case yaml.AliasNode:
		if n.Alias != nil {
			return literal(n.Alias)
		}
	}
	return model.Unknown
}

func isIntrinsic(n *yaml.Node) bool {
	if len(n.Content) != 2 {
		return false
	}
	k := n.Content[0].Value
	return k == "Ref" || k == "Condition" || strings.HasPrefix(k, "Fn::")
}

func get(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func scalar(n *yaml.Node) (string, bool) {
	if n == nil || n.Kind != yaml.ScalarNode {
		return "", false
	}
	return n.Value, true
}

// --- parsing ---

// parse returns the template's top-level mapping. YAML (with short-form
// intrinsic tags) is tried first; JSON that YAML rejects (e.g. tab
// indentation, duplicate keys) falls back to encoding/json with line numbers
// computed from token offsets.
func parse(data []byte) (*yaml.Node, error) {
	var doc yaml.Node
	yerr := yaml.Unmarshal(data, &doc)
	if yerr == nil {
		n := &doc
		if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
			n = n.Content[0]
		}
		if n.Kind != yaml.MappingNode {
			return nil, errors.New("template is not a mapping")
		}
		expandTags(n)
		return n, nil
	}
	if trimmed := bytes.TrimSpace(data); len(trimmed) > 0 && trimmed[0] == '{' {
		return parseJSON(data)
	}
	return nil, yerr
}

// shortTags maps short-form intrinsic tags to their long-form keys.
var shortTags = map[string]string{
	"!Ref": "Ref", "!Condition": "Condition",
	"!GetAtt": "Fn::GetAtt", "!Sub": "Fn::Sub", "!If": "Fn::If", "!Join": "Fn::Join",
	"!Select": "Fn::Select", "!Split": "Fn::Split", "!FindInMap": "Fn::FindInMap",
	"!ImportValue": "Fn::ImportValue", "!Base64": "Fn::Base64", "!Cidr": "Fn::Cidr",
	"!GetAZs": "Fn::GetAZs", "!Equals": "Fn::Equals", "!And": "Fn::And", "!Or": "Fn::Or",
	"!Not": "Fn::Not", "!Transform": "Fn::Transform", "!Length": "Fn::Length",
	"!ToJsonString": "Fn::ToJsonString",
}

// expandTags rewrites every node tagged with a short-form intrinsic (e.g.
// `!Ref X`) in place into its long form (`{Ref: X}`), keeping line numbers.
func expandTags(n *yaml.Node) {
	for _, c := range n.Content {
		expandTags(c)
	}
	if n.Tag == "" || strings.HasPrefix(n.Tag, "!!") || !strings.HasPrefix(n.Tag, "!") {
		return
	}
	key, ok := shortTags[n.Tag]
	if !ok {
		key = "Fn::" + strings.TrimPrefix(n.Tag, "!")
	}
	inner := *n
	inner.Tag = ""
	if inner.Kind == yaml.ScalarNode {
		inner.Tag = "!!str"
	}
	if key == "Fn::GetAtt" && inner.Kind == yaml.ScalarNode {
		res, att, _ := strings.Cut(inner.Value, ".")
		inner = yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Line: n.Line, Column: n.Column, Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: res, Line: n.Line, Column: n.Column},
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: att, Line: n.Line, Column: n.Column},
		}}
	}
	*n = yaml.Node{
		Kind: yaml.MappingNode, Tag: "!!map", Line: n.Line, Column: n.Column,
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: key, Line: n.Line, Column: n.Column},
			&inner,
		},
	}
}

// parseJSON builds a yaml.Node tree from a JSON document using the token
// stream; each node's line is the line where its token ends.
func parseJSON(data []byte) (*yaml.Node, error) {
	nl := []int{}
	for i, b := range data {
		if b == '\n' {
			nl = append(nl, i)
		}
	}
	lineOf := func(off int64) int {
		if off > 0 {
			off-- // offset is just past the token
		}
		return sort.SearchInts(nl, int(off)) + 1
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var build func() (*yaml.Node, error)
	build = func() (*yaml.Node, error) {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		line := lineOf(dec.InputOffset())
		switch v := tok.(type) {
		case json.Delim:
			switch v {
			case '{':
				m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Line: line}
				for dec.More() {
					kt, err := dec.Token()
					if err != nil {
						return nil, err
					}
					ks, _ := kt.(string)
					k := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: ks, Line: lineOf(dec.InputOffset())}
					val, err := build()
					if err != nil {
						return nil, err
					}
					m.Content = append(m.Content, k, val)
				}
				_, err := dec.Token()
				return m, err
			case '[':
				s := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Line: line}
				for dec.More() {
					val, err := build()
					if err != nil {
						return nil, err
					}
					s.Content = append(s.Content, val)
				}
				_, err := dec.Token()
				return s, err
			}
			return nil, errors.New("unexpected delimiter")
		case string:
			return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v, Line: line}, nil
		case json.Number:
			tag := "!!int"
			if strings.ContainsAny(string(v), ".eE") {
				tag = "!!float"
			}
			return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: string(v), Line: line}, nil
		case bool:
			return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: fmt.Sprint(v), Line: line}, nil
		case nil:
			return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null", Line: line}, nil
		}
		return nil, errors.New("unexpected token")
	}
	root, err := build()
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("trailing data")
	}
	if root.Kind != yaml.MappingNode {
		return nil, errors.New("template is not a mapping")
	}
	return root, nil
}
