package catalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	yaml "go.yaml.in/yaml/v3"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"github.com/cloudowski/exitctl/internal/version"
	"github.com/cloudowski/exitctl/schemas"
)

// SchemaError reports a catalog file whose schema major this build cannot
// read. It is returned as the error of Load (not as a Problem).
type SchemaError struct {
	File   string
	Schema string
	// Newer is true when the file needs a newer exitctl; false when it is
	// an older major without a registered migration.
	Newer bool
}

func (e *SchemaError) Error() string {
	if e.Newer {
		return fmt.Sprintf("catalog schema %s requires a newer exitctl (%s)", e.Schema, e.File)
	}
	return fmt.Sprintf("catalog schema %s is no longer supported: no migration available (%s)", e.Schema, e.File)
}

const entrySchemaFile = "catalog-entry.schema.json"

var (
	entrySchemaOnce sync.Once
	entrySchema     *jsonschema.Schema
	entrySchemaErr  error
)

func compiledEntrySchema() (*jsonschema.Schema, error) {
	entrySchemaOnce.Do(func() {
		b, err := fs.ReadFile(schemas.FS, entrySchemaFile)
		if err != nil {
			entrySchemaErr = err
			return
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
		if err != nil {
			entrySchemaErr = err
			return
		}
		const loc = "https://exitctl.invalid/schemas/" + entrySchemaFile
		c := jsonschema.NewCompiler()
		c.AssertFormat()
		if err := c.AddResource(loc, doc); err != nil {
			entrySchemaErr = err
			return
		}
		entrySchema, entrySchemaErr = c.Compile(loc)
	})
	return entrySchema, entrySchemaErr
}

var yamlLineRe = regexp.MustCompile(`line (\d+)`)

// Load reads every *.yaml file of a catalog tree (data-model.md 1.1) in
// sorted path order. Each file is validated against the embedded catalog
// entry schema and then decoded by its `kind`. Problems are collected rather
// than failing on the first; the returned error is reserved for I/O failures
// and unsupported schema majors (*SchemaError). Non-YAML files are ignored.
// Load does not run the referential checks of Validate; see LoadAndValidate.
func Load(fsys fs.FS) (*Catalog, []Problem, error) {
	sch, err := compiledEntrySchema()
	if err != nil {
		return nil, nil, fmt.Errorf("catalog entry schema: %w", err)
	}
	var files []string
	err = fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && path.Ext(p) == ".yaml" {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("reading catalog: %w", err)
	}
	sort.Strings(files)

	cat := &Catalog{locs: map[string][]Pos{}}
	var probs []Problem
	manifests := 0
	for _, f := range files {
		b, err := fs.ReadFile(fsys, f)
		if err != nil {
			return nil, nil, fmt.Errorf("reading catalog file %s: %w", f, err)
		}
		fp, err := loadFile(cat, sch, f, b, &manifests)
		if err != nil {
			return nil, nil, err
		}
		probs = append(probs, fp...)
	}
	if manifests == 0 {
		probs = append(probs, Problem{File: "catalog.yaml", Message: "catalog manifest (kind: catalog) not found"})
	}
	sortProblems(probs)
	return cat, probs, nil
}

func loadFile(cat *Catalog, sch *jsonschema.Schema, file string, b []byte, manifests *int) ([]Problem, error) {
	prob := func(line int, id, format string, args ...any) Problem {
		return Problem{File: file, Line: line, EntryID: id, Message: fmt.Sprintf(format, args...)}
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return []Problem{prob(0, "", "empty file")}, nil
		}
		return []Problem{prob(yamlErrLine(err), "", "invalid YAML: %s", strings.TrimPrefix(err.Error(), "yaml: "))}, nil
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err == nil {
		return []Problem{prob(extra.Line, "", "multiple YAML documents in one file")}, nil
	} else if !errors.Is(err, io.EOF) {
		return []Problem{prob(yamlErrLine(err), "", "invalid YAML: %s", strings.TrimPrefix(err.Error(), "yaml: "))}, nil
	}
	root := &doc
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		root = root.Content[0]
	}
	if root.Kind != yaml.MappingNode {
		return []Problem{prob(root.Line, "", "catalog file must be a mapping")}, nil
	}
	id := scalarField(root, "id")

	// Schema version handling (FR-011).
	if sv := mapValue(root, "schema"); sv != nil && sv.Kind == yaml.ScalarNode {
		if major, ok := schemaMajor(sv.Value); ok {
			target := slices.Max(version.SupportedCatalogSchemaMajors)
			switch {
			case major > target:
				return nil, &SchemaError{File: file, Schema: sv.Value, Newer: true}
			case major < target:
				for m := major; m < target; m++ {
					fn, ok := migrations[m]
					if !ok {
						return nil, &SchemaError{File: file, Schema: sv.Value}
					}
					if err := fn(root); err != nil {
						return []Problem{prob(sv.Line, id, "migrating schema %d to %d: %v", m, m+1, err)}, nil
					}
				}
			}
		}
	}

	// JSON schema validation.
	inst, err := toJSONValue(root)
	if err != nil {
		return []Problem{prob(root.Line, id, "%v", err)}, nil
	}
	if err := sch.Validate(inst); err != nil {
		var ve *jsonschema.ValidationError
		if !errors.As(err, &ve) {
			return []Problem{prob(root.Line, id, "schema validation: %v", err)}, nil
		}
		var out []Problem
		seen := map[string]bool{}
		for _, leaf := range leafErrors(ve) {
			msg := fmt.Sprintf("schema: at %s: %s", jsonPointer(leaf.InstanceLocation), leaf.ErrorKind.LocalizedString(printer))
			if seen[msg] {
				continue
			}
			seen[msg] = true
			line := root.Line
			if n := nodeAt(root, leaf.InstanceLocation); n != nil {
				line = n.Line
			}
			out = append(out, prob(line, id, "%s", msg))
		}
		return out, nil
	}

	// Typed decoding by kind.
	pos := Pos{File: file, Line: root.Line}
	if n := mapValue(root, "id"); n != nil {
		pos.Line = n.Line
	}
	decodeErr := func(err error) []Problem {
		return []Problem{prob(yamlErrLine(err), id, "decoding %s: %s", scalarField(root, "kind"), strings.TrimPrefix(err.Error(), "yaml: "))}
	}
	switch scalarField(root, "kind") {
	case "catalog":
		*manifests++
		if *manifests > 1 {
			return []Problem{prob(root.Line, "", "duplicate catalog manifest")}, nil
		}
		var m Manifest
		if err := root.Decode(&m); err != nil {
			return decodeErr(err), nil
		}
		m.Pos = Pos{File: file, Line: root.Line}
		cat.Manifest = m
	case "providers":
		var v struct {
			Providers []Provider `yaml:"providers"`
		}
		if err := root.Decode(&v); err != nil {
			return decodeErr(err), nil
		}
		lines := listItemLines(root, "providers")
		for i, p := range v.Providers {
			cat.locs["provider:"+p.ID] = append(cat.locs["provider:"+p.ID], Pos{File: file, Line: lines[i]})
		}
		cat.Providers = append(cat.Providers, v.Providers...)
	case "categories":
		var v struct {
			Categories []Category `yaml:"categories"`
		}
		if err := root.Decode(&v); err != nil {
			return decodeErr(err), nil
		}
		lines := listItemLines(root, "categories")
		for i, c := range v.Categories {
			cat.locs["category:"+c.ID] = append(cat.locs["category:"+c.ID], Pos{File: file, Line: lines[i]})
		}
		cat.Categories = append(cat.Categories, v.Categories...)
	case "trait":
		var t Trait
		if err := root.Decode(&t); err != nil {
			return decodeErr(err), nil
		}
		t.Pos = pos
		cat.Traits = append(cat.Traits, t)
	case "service":
		var s Service
		if err := root.Decode(&s); err != nil {
			return decodeErr(err), nil
		}
		s.Pos = pos
		cat.Services = append(cat.Services, s)
	case "alternative":
		var a Alternative
		if err := root.Decode(&a); err != nil {
			return decodeErr(err), nil
		}
		a.Pos = pos
		cat.Alternatives = append(cat.Alternatives, a)
	case "neutral":
		var n Neutral
		if err := root.Decode(&n); err != nil {
			return decodeErr(err), nil
		}
		n.Pos = pos
		cat.Neutrals = append(cat.Neutrals, n)
	default:
		return []Problem{prob(root.Line, id, "unknown kind %q", scalarField(root, "kind"))}, nil
	}
	return nil, nil
}

var printer = message.NewPrinter(language.English)

func yamlErrLine(err error) int {
	if m := yamlLineRe.FindStringSubmatch(err.Error()); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return 0
}

// schemaMajor parses the major of a "major.minor" schema string.
func schemaMajor(s string) (int, bool) {
	maj, _, ok := strings.Cut(s, ".")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(maj)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

func mapValue(m *yaml.Node, key string) *yaml.Node {
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

func scalarField(m *yaml.Node, key string) string {
	if n := mapValue(m, key); n != nil && n.Kind == yaml.ScalarNode {
		return n.Value
	}
	return ""
}

// listItemLines returns the line of each item of the sequence under key.
func listItemLines(m *yaml.Node, key string) []int {
	seq := mapValue(m, key)
	var out []int
	if seq != nil {
		for _, it := range seq.Content {
			line := it.Line
			if idn := mapValue(it, "id"); idn != nil {
				line = idn.Line
			}
			out = append(out, line)
		}
	}
	return out
}

// nodeAt follows a JSON-pointer-like path of tokens through a YAML tree.
func nodeAt(n *yaml.Node, toks []string) *yaml.Node {
	for _, t := range toks {
		for n != nil && n.Kind == yaml.AliasNode {
			n = n.Alias
		}
		if n == nil {
			return nil
		}
		switch n.Kind {
		case yaml.MappingNode:
			v := mapValue(n, t)
			if v == nil {
				return n
			}
			n = v
		case yaml.SequenceNode:
			i, err := strconv.Atoi(t)
			if err != nil || i < 0 || i >= len(n.Content) {
				return n
			}
			n = n.Content[i]
		default:
			return n
		}
	}
	return n
}

func jsonPointer(toks []string) string {
	if len(toks) == 0 {
		return "'/'"
	}
	var sb strings.Builder
	sb.WriteByte('\'')
	for _, t := range toks {
		sb.WriteByte('/')
		t = strings.ReplaceAll(t, "~", "~0")
		sb.WriteString(strings.ReplaceAll(t, "/", "~1"))
	}
	sb.WriteByte('\'')
	return sb.String()
}

func leafErrors(e *jsonschema.ValidationError) []*jsonschema.ValidationError {
	if len(e.Causes) == 0 {
		return []*jsonschema.ValidationError{e}
	}
	var out []*jsonschema.ValidationError
	for _, c := range e.Causes {
		out = append(out, leafErrors(c)...)
	}
	return out
}

// toJSONValue converts a YAML node into the value model used by
// encoding/json (map[string]any, []any, string, bool, json.Number, nil).
func toJSONValue(n *yaml.Node) (any, error) {
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return nil, nil
		}
		return toJSONValue(n.Content[0])
	case yaml.AliasNode:
		return toJSONValue(n.Alias)
	case yaml.MappingNode:
		m := make(map[string]any, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			if k.Kind != yaml.ScalarNode {
				return nil, fmt.Errorf("line %d: mapping keys must be scalars", k.Line)
			}
			if k.Value == "<<" && k.ShortTag() == "!!merge" {
				return nil, fmt.Errorf("line %d: YAML merge keys are not supported", k.Line)
			}
			if _, dup := m[k.Value]; dup {
				return nil, fmt.Errorf("line %d: duplicate key %q", k.Line, k.Value)
			}
			v, err := toJSONValue(n.Content[i+1])
			if err != nil {
				return nil, err
			}
			m[k.Value] = v
		}
		return m, nil
	case yaml.SequenceNode:
		s := make([]any, 0, len(n.Content))
		for _, c := range n.Content {
			v, err := toJSONValue(c)
			if err != nil {
				return nil, err
			}
			s = append(s, v)
		}
		return s, nil
	case yaml.ScalarNode:
		switch n.ShortTag() {
		case "!!null":
			return nil, nil
		case "!!bool":
			var b bool
			if err := n.Decode(&b); err != nil {
				return nil, err
			}
			return b, nil
		case "!!int":
			var i int64
			if err := n.Decode(&i); err != nil {
				var f float64
				if err2 := n.Decode(&f); err2 != nil {
					return nil, err
				}
				return json.Number(strconv.FormatFloat(f, 'f', -1, 64)), nil
			}
			return json.Number(strconv.FormatInt(i, 10)), nil
		case "!!float":
			var f float64
			if err := n.Decode(&f); err != nil {
				return nil, err
			}
			if math.IsInf(f, 0) || math.IsNaN(f) {
				return n.Value, nil
			}
			return json.Number(strconv.FormatFloat(f, 'g', -1, 64)), nil
		default:
			// !!str, !!timestamp, !!binary and custom tags keep their text.
			return n.Value, nil
		}
	}
	return nil, fmt.Errorf("line %d: unsupported YAML node", n.Line)
}

func sortProblems(ps []Problem) {
	sort.SliceStable(ps, func(i, j int) bool {
		a, b := ps[i], ps[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.EntryID != b.EntryID {
			return a.EntryID < b.EntryID
		}
		return a.Message < b.Message
	})
}

// String formats a problem as "<file>:<line>  <entry-id>  <message>".
func (p Problem) String() string {
	loc := p.File
	if p.Line > 0 {
		loc = fmt.Sprintf("%s:%d", p.File, p.Line)
	}
	id := p.EntryID
	if id == "" {
		id = "-"
	}
	return fmt.Sprintf("%s  %s  %s", loc, id, p.Message)
}
