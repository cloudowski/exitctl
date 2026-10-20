// Package pulumi finds Pulumi resources in application code (TypeScript,
// JavaScript, Python, Go) and in Pulumi YAML programs, and reports them under
// the Terraform type of the bridged provider (aws, gcp, azure classic). It
// never runs the program, so resources created in loops or by helpers are
// counted once and their properties are unknown. Only the resource class
// and its name are read.
package pulumi

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/cloudowski/exitctl/internal/model"
	"github.com/cloudowski/exitctl/internal/redact"
	"github.com/cloudowski/exitctl/internal/source"
	"github.com/cloudowski/exitctl/internal/source/srcscan"
	"github.com/cloudowski/exitctl/internal/walk"
)

// isPulumiPackage reports whether a dependency marks a component as a
// Pulumi program.
func isPulumiPackage(eco model.Ecosystem, name string) bool {
	switch eco {
	case model.EcoNPM:
		return name == "@pulumi/pulumi" || strings.HasPrefix(name, "@pulumi/aws") ||
			strings.HasPrefix(name, "@pulumi/gcp") || strings.HasPrefix(name, "@pulumi/azure")
	case model.EcoPyPI:
		return name == "pulumi" || strings.HasPrefix(name, "pulumi-")
	case model.EcoGo:
		return strings.HasPrefix(name, "github.com/pulumi/pulumi")
	}
	return false
}

// CodeDetector reads Pulumi programs written in a general-purpose language.
// It is a dependent detector: it needs opts.Packages.
type CodeDetector struct{}

// New returns the detector for Pulumi programs in code.
func New() source.Detector { return &CodeDetector{} }

// Name implements source.Detector.
func (*CodeDetector) Name() string { return "pulumi" }

type resource struct {
	provider, module, class, name string
	line                          int
	excerpt                       string
}

func node(repo, file string, r resource, typ string, seen map[string]int) model.RawNode {
	addr := r.provider + "." + strings.ToLower(strings.TrimRight(r.module, "_")) + "." + r.class + "." + r.name
	if seen[addr]++; seen[addr] > 1 {
		addr = fmt.Sprintf("%s@%d", addr, r.line)
	}
	return model.RawNode{
		ID:            model.CanonicalID(repo, file, addr),
		NodeKind:      model.NodeResource,
		SourceKind:    model.KindPulumi,
		Type:          typ,
		NativeAddress: addr,
		Attributes:    map[string]any{},
		Multiplicity:  model.MultOne,
		Evidence:      []model.Evidence{model.NewEvidence(repo, file, r.line, r.excerpt, model.KindPulumi)},
	}
}

// Detect implements source.Detector.
func (*CodeDetector) Detect(ctx context.Context, idx walk.Index, opts source.Options) (model.Facts, error) {
	var facts model.Facts
	root := idx.Root
	if root == "" {
		root = opts.Root
	}
	for _, cf := range source.CodeComponents(idx, opts) {
		isApp := false
		for _, p := range cf.Packages {
			if isPulumiPackage(p.Ecosystem, p.Name) {
				isApp = true
				break
			}
		}
		if !isApp {
			continue
		}
		for _, f := range cf.Files {
			if err := ctx.Err(); err != nil {
				return facts, err
			}
			sx := langs[strings.ToLower(path.Ext(f))]
			if sx == nil {
				continue
			}
			seen := map[string]int{}
			for _, r := range scanFile(root, f, sx) {
				if typ, ok := tfType(r.provider, r.module, r.class); ok {
					facts.RawNodes = append(facts.RawNodes, node(opts.Repo, f, r, typ, seen))
				}
			}
		}
	}
	facts.Sort()
	return facts, nil
}

// ---------------------------------------------------------------- code syntax

// A target is what an import alias stands for: a provider package, or one of
// its modules.
type target struct{ provider, module string }

type state struct {
	alias map[string]target
	cont  string // provider of a multi-line "from pulumi_x import (" list
}

type syntax struct {
	scanImport func(line string, st *state)
	create     *regexp.Regexp // groups: 1 qualifier chain ("a.b."), 2 class, 3 name
}

var langs = map[string]*syntax{}

func init() {
	for _, e := range []string{".ts", ".tsx", ".js", ".mjs", ".cjs", ".jsx"} {
		langs[e] = tsSyntax
	}
	langs[".py"] = pySyntax
	langs[".go"] = goSyntax
}

func scanFile(root, rel string, sx *syntax) []resource {
	st := &state{alias: map[string]target{}}
	var out []resource
	srcscan.LinesJoined(root, rel, srcscan.ForFile(rel), func(no int, line, joined string) {
		sx.scanImport(line, st)
		m := sx.create.FindStringSubmatch(joined)
		if m == nil {
			return
		}
		chain := strings.Split(strings.TrimSuffix(m[1], "."), ".")
		if len(chain) == 1 && chain[0] == "" {
			return
		}
		t, ok := st.alias[chain[0]]
		if !ok {
			return
		}
		module := t.module
		if module == "" && len(chain) > 1 {
			module = chain[1] // aws.s3.Bucket
		}
		if module == "" && len(chain) == 1 {
			return // aws.Thing: not a resource class of a module
		}
		out = append(out, resource{provider: t.provider, module: module, class: m[2], name: m[3], line: no, excerpt: redact.Line(line)})
	})
	return out
}

var (
	reNamed = regexp.MustCompile(`(\w+)(?:\s+as\s+(\w+))?`)

	reTSStar    = regexp.MustCompile(`import\s*\*\s*as\s+(\w+)\s+from\s*['"]@pulumi/(aws|gcp|azure)(?:/([a-z0-9]+))?['"]`)
	reTSNamed   = regexp.MustCompile(`import\s*\{([^}]*)\}\s*from\s*['"]@pulumi/(aws|gcp|azure)['"]`)
	reTSRequire = regexp.MustCompile(`(?:const|let|var)\s+(\w+)\s*=\s*require\(\s*['"]@pulumi/(aws|gcp|azure)(?:/([a-z0-9]+))?['"]\s*\)`)
	reTSCreate  = regexp.MustCompile("\\bnew\\s+((?:[A-Za-z_$][\\w$]*\\.)+)([A-Z][\\w$]*)\\s*\\(\\s*['\"`]([^'\"`]+)['\"`]")

	rePyAlias  = regexp.MustCompile(`^\s*import\s+pulumi_(aws|gcp|azure)(?:\.([a-z0-9_]+))?(?:\s+as\s+(\w+))?\s*$`)
	rePyFrom   = regexp.MustCompile(`^\s*from\s+pulumi_(aws|gcp|azure)\s+import\s+\(?(.*)$`)
	rePyCreate = regexp.MustCompile(`\b((?:[A-Za-z_]\w*\.)+)([A-Z]\w*)\(\s*(?:resource_name\s*=\s*)?["']([^"']+)["']`)

	reGoImport = regexp.MustCompile(`(?:(\w+)\s+)?"github\.com/pulumi/pulumi-(aws|gcp|azure)/sdk(?:/v\d+)?/go/\w+/([a-z0-9]+)"`)
	reGoCreate = regexp.MustCompile(`\b(\w+\.)New([A-Z]\w*)\(\s*ctx\s*,\s*"([^"]+)"`)
)

func addNamed(st *state, provider, list string) {
	for _, it := range reNamed.FindAllStringSubmatch(list, -1) {
		name := it[1]
		if it[2] != "" {
			name = it[2]
		}
		st.alias[name] = target{provider, strings.ToLower(it[1])}
	}
}

var tsSyntax = &syntax{
	scanImport: func(line string, st *state) {
		for _, re := range []*regexp.Regexp{reTSStar, reTSRequire} {
			if m := re.FindStringSubmatch(line); m != nil {
				st.alias[m[1]] = target{m[2], m[3]}
			}
		}
		if m := reTSNamed.FindStringSubmatch(line); m != nil {
			addNamed(st, m[2], m[1])
		}
	},
	create: reTSCreate,
}

var pySyntax = &syntax{
	scanImport: func(line string, st *state) {
		if st.cont != "" {
			addNamed(st, st.cont, strings.TrimSpace(strings.SplitN(line, ")", 2)[0]))
			if strings.Contains(line, ")") {
				st.cont = ""
			}
			return
		}
		if m := rePyAlias.FindStringSubmatch(line); m != nil {
			alias := "pulumi_" + m[1]
			if m[3] != "" {
				alias = m[3]
			} else if m[2] != "" {
				alias = m[2]
			}
			st.alias[alias] = target{m[1], m[2]}
			return
		}
		if m := rePyFrom.FindStringSubmatch(line); m != nil {
			addNamed(st, m[1], strings.SplitN(m[2], ")", 2)[0])
			if strings.Contains(line, "(") && !strings.Contains(line, ")") {
				st.cont = m[1]
			}
		}
	},
	create: rePyCreate,
}

var goSyntax = &syntax{
	scanImport: func(line string, st *state) {
		if m := reGoImport.FindStringSubmatch(line); m != nil {
			alias := m[3]
			if m[1] != "" {
				alias = m[1]
			}
			st.alias[alias] = target{m[2], m[3]}
		}
	},
	create: reGoCreate,
}

// ---------------------------------------------------------------- Pulumi YAML

// YAMLDetector reads Pulumi YAML programs (Pulumi.yaml).
type YAMLDetector struct{}

// NewYAML returns the detector for Pulumi YAML programs.
func NewYAML() source.Detector { return &YAMLDetector{} }

// Name implements source.Detector.
func (*YAMLDetector) Name() string { return "pulumi-yaml" }

// Detect implements source.Detector.
func (*YAMLDetector) Detect(ctx context.Context, idx walk.Index, opts source.Options) (model.Facts, error) {
	var facts model.Facts
	root := idx.Root
	if root == "" {
		root = opts.Root
	}
	for _, rel := range idx.Files[model.KindPulumi] {
		if err := ctx.Err(); err != nil {
			return facts, err
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			facts.Sources = append(facts.Sources, source.FailedSource(opts.Repo, rel, model.KindPulumi, "file could not be read"))
			continue
		}
		var doc yaml.Node
		if err := yaml.Unmarshal(data, &doc); err != nil || len(doc.Content) == 0 {
			facts.Sources = append(facts.Sources, source.FailedSource(opts.Repo, rel, model.KindPulumi, "could not be parsed as a Pulumi program"))
			continue
		}
		lines := strings.Split(string(data), "\n")
		seen := map[string]int{}
		top := doc.Content[0]
		if res := mapGet(top, "resources"); res != nil && res.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(res.Content); i += 2 {
				key, body := res.Content[i], res.Content[i+1]
				typ := mapGet(body, "type")
				if typ == nil {
					continue
				}
				provider, module, class := parseToken(typ.Value)
				tf, ok := tfType(provider, module, class)
				if !ok {
					continue
				}
				text := ""
				if key.Line >= 1 && key.Line <= len(lines) {
					text = redact.Line(lines[key.Line-1])
				}
				facts.RawNodes = append(facts.RawNodes, node(opts.Repo, rel,
					resource{provider: provider, module: module, class: class, name: key.Value, line: key.Line, excerpt: text}, tf, seen))
			}
		}
		facts.Sources = append(facts.Sources, model.Source{Repo: opts.Repo, Path: rel, Kind: model.KindPulumi, Status: model.StatusParsed})
	}
	facts.Sort()
	return facts, nil
}

func mapGet(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

// parseToken splits a Pulumi type token "aws:s3/bucket:Bucket" or
// "aws:s3:Bucket" into provider, module and class.
func parseToken(tok string) (provider, module, class string) {
	parts := strings.Split(tok, ":")
	if len(parts) != 3 {
		return "", "", ""
	}
	module, _, _ = strings.Cut(parts[1], "/")
	return parts[0], module, parts[2]
}
