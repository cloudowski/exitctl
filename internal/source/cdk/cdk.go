// Package cdk finds AWS CDK constructs in application code and reports them
// as CloudFormation resources (source kind cdk). It reads TypeScript,
// JavaScript, Python, Java, Kotlin and Go with line-oriented patterns and
// never runs the program, so constructs created in loops or by helpers are
// counted once and their properties are unknown.
package cdk

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/cloudowski/exitctl/internal/model"
	"github.com/cloudowski/exitctl/internal/redact"
	"github.com/cloudowski/exitctl/internal/source"
	"github.com/cloudowski/exitctl/internal/source/srcscan"
	"github.com/cloudowski/exitctl/internal/walk"
)

// Detector emits one CloudFormation-typed RawNode per CDK construct. It is a
// dependent detector: it needs opts.Packages to find CDK components.
type Detector struct{}

// New returns the CDK detector.
func New() source.Detector { return &Detector{} }

// Name implements source.Detector.
func (*Detector) Name() string { return "cdk" }

// isCDKPackage reports whether a dependency marks a component as a CDK app.
func isCDKPackage(eco model.Ecosystem, name string) bool {
	switch eco {
	case model.EcoNPM:
		return name == "aws-cdk-lib" || strings.HasPrefix(name, "@aws-cdk/")
	case model.EcoPyPI:
		return name == "aws-cdk-lib" || strings.HasPrefix(name, "aws-cdk.")
	case model.EcoMaven:
		return strings.HasPrefix(name, "software.amazon.awscdk:")
	case model.EcoGo:
		return strings.HasPrefix(name, "github.com/aws/aws-cdk-go/")
	}
	return false
}

// langs maps a source file extension to its syntax.
var langs = map[string]*syntax{}

func init() {
	for _, e := range []string{".ts", ".tsx", ".js", ".mjs", ".cjs", ".jsx"} {
		langs[e] = tsSyntax
	}
	langs[".py"] = pySyntax
	langs[".java"] = javaSyntax
	langs[".kt"] = ktSyntax
	langs[".go"] = goSyntax
}

// A construct is a resolved CDK construct creation.
type construct struct {
	module, class, id string
	line              int
	excerpt           string
}

// Detect implements source.Detector.
func (*Detector) Detect(ctx context.Context, idx walk.Index, opts source.Options) (model.Facts, error) {
	var facts model.Facts
	root := idx.Root
	if root == "" {
		root = opts.Root
	}
	for _, cf := range source.CodeComponents(idx, opts) {
		isApp := false
		for _, p := range cf.Packages {
			if isCDKPackage(p.Ecosystem, p.Name) {
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
			for _, c := range scanFile(root, f, sx) {
				typ, ok := cfnType(c.module, c.class)
				if !ok {
					continue
				}
				addr := c.module + "." + c.class + "." + c.id
				if seen[addr]++; seen[addr] > 1 {
					addr = fmt.Sprintf("%s@%d", addr, c.line)
				}
				facts.RawNodes = append(facts.RawNodes, model.RawNode{
					ID:            model.CanonicalID(opts.Repo, f, addr),
					NodeKind:      model.NodeResource,
					SourceKind:    model.KindCDK,
					Type:          typ,
					NativeAddress: addr,
					Attributes:    map[string]any{},
					Multiplicity:  model.MultOne,
					Evidence:      []model.Evidence{model.NewEvidence(opts.Repo, f, c.line, c.excerpt, model.KindCDK)},
				})
			}
		}
	}
	facts.Sort()
	return facts, nil
}

// syntax describes how one language imports CDK modules and creates
// constructs. Each import pattern fills the alias and class maps; each
// create pattern yields (qualifier, class, id).
type syntax struct {
	// scanImport records module aliases (alias -> module) and directly
	// imported classes (class -> module) found on a line.
	scanImport func(line string, st *state)
	create     []*regexp.Regexp // groups: 1 qualifier ("a.b." or ""), 2 class, 3 id
	// resolveQualifier maps a qualifier ("s3", "cdk.aws_s3", "awss3") to a CDK module.
	resolveQualifier func(q string, st *state) string
}

type state struct {
	alias   map[string]string // alias -> module
	classes map[string]string // class -> module
	wild    []string          // modules imported with a wildcard
}

func newState() *state { return &state{alias: map[string]string{}, classes: map[string]string{}} }

// moduleName turns "aws-s3" / "aws_s3" / "awss3" / "s3" into "s3".
func moduleName(s string) string {
	s = strings.ToLower(s)
	s = strings.TrimPrefix(s, "aws")
	s = strings.NewReplacer("-", "", "_", "").Replace(s)
	return s
}

func scanFile(root, rel string, sx *syntax) []construct {
	st := newState()
	var out []construct
	srcscan.LinesJoined(root, rel, srcscan.ForFile(rel), func(no int, line, joined string) {
		sx.scanImport(line, st)
		for _, re := range sx.create {
			m := re.FindStringSubmatch(joined)
			if m == nil {
				continue
			}
			q, class, id := strings.TrimSuffix(m[1], "."), m[2], m[3]
			module := ""
			switch {
			case q != "":
				module = sx.resolveQualifier(q, st)
			case st.classes[class] != "":
				module = st.classes[class]
			case len(st.wild) == 1:
				module = st.wild[0]
			}
			if module == "" {
				continue
			}
			out = append(out, construct{module: module, class: class, id: id, line: no, excerpt: redact.Line(line)})
			return
		}
	})
	return out
}

// ---------------------------------------------------------------- TypeScript / JavaScript

var (
	reTSStar    = regexp.MustCompile(`import\s*\*\s*as\s+(\w+)\s+from\s*['"](?:aws-cdk-lib|@aws-cdk)/aws-([a-z0-9-]+)(?:/[^'"]*)?['"]`)
	reTSNamed   = regexp.MustCompile(`import\s*\{([^}]*)\}\s*from\s*['"](?:aws-cdk-lib|@aws-cdk)/aws-([a-z0-9-]+)(?:/[^'"]*)?['"]`)
	reTSRoot    = regexp.MustCompile(`import\s*\{([^}]*)\}\s*from\s*['"]aws-cdk-lib['"]`)
	reTSAwsAs   = regexp.MustCompile(`\baws_([a-z0-9_]+)(?:\s+as\s+(\w+))?`)
	reTSCreate  = regexp.MustCompile("\\bnew\\s+((?:[A-Za-z_$][\\w$]*\\.)*)([A-Z][\\w$]*)\\s*\\(\\s*[\\w.$]+\\s*,\\s*['\"`]([^'\"`]*)['\"`]")
	reNamedItem = regexp.MustCompile(`(\w+)(?:\s+as\s+\w+)?`)
)

var tsSyntax = &syntax{
	scanImport: func(line string, st *state) {
		if m := reTSStar.FindStringSubmatch(line); m != nil {
			st.alias[m[1]] = moduleName(m[2])
		}
		if m := reTSNamed.FindStringSubmatch(line); m != nil {
			for _, it := range reNamedItem.FindAllStringSubmatch(m[1], -1) {
				st.classes[it[1]] = moduleName(m[2])
			}
		}
		if m := reTSRoot.FindStringSubmatch(line); m != nil {
			for _, it := range reTSAwsAs.FindAllStringSubmatch(m[1], -1) {
				name := "aws_" + it[1]
				if it[2] != "" {
					name = it[2]
				}
				st.alias[name] = moduleName(it[1])
			}
		}
	},
	create: []*regexp.Regexp{reTSCreate},
	resolveQualifier: func(q string, st *state) string {
		last := q[strings.LastIndex(q, ".")+1:]
		if m, ok := st.alias[last]; ok {
			return m
		}
		if strings.HasPrefix(last, "aws_") { // cdk.aws_s3.Bucket
			return moduleName(last)
		}
		return ""
	},
}

// ---------------------------------------------------------------- Python

var (
	rePyAs     = regexp.MustCompile(`\baws_([a-z0-9_]+)(?:\s+as\s+(\w+))?`)
	rePyFrom   = regexp.MustCompile(`^\s*from\s+aws_cdk\.aws_([a-z0-9_]+)\s+import\s+(.+)$`)
	rePyCreate = regexp.MustCompile(`\b((?:[A-Za-z_]\w*\.)*)([A-Z]\w*)\(\s*[\w.]+\s*,\s*(?:id\s*=\s*)?["']([^"']+)["']`)
)

var pySyntax = &syntax{
	scanImport: func(line string, st *state) {
		if m := rePyFrom.FindStringSubmatch(line); m != nil {
			for _, it := range reNamedItem.FindAllStringSubmatch(strings.Trim(m[2], "() "), -1) {
				st.classes[it[1]] = moduleName(m[1])
			}
			return
		}
		if !strings.Contains(line, "aws_") {
			return
		}
		for _, it := range rePyAs.FindAllStringSubmatch(line, -1) {
			name := "aws_" + it[1]
			if it[2] != "" {
				name = it[2]
			}
			st.alias[name] = moduleName(it[1])
		}
	},
	create: []*regexp.Regexp{rePyCreate},
	resolveQualifier: func(q string, st *state) string {
		last := q[strings.LastIndex(q, ".")+1:]
		return st.alias[last]
	},
}

// ---------------------------------------------------------------- Java / Kotlin

var (
	reJavaImport  = regexp.MustCompile(`^\s*import\s+software\.amazon\.awscdk\.(?:services|aws)\.([a-z0-9]+)\.(\*|[A-Z]\w*)`)
	reJavaNew     = regexp.MustCompile(`\bnew\s+()([A-Z]\w*)\s*\(\s*[\w.]+\s*,\s*"([^"]*)"`)
	reJavaBuilder = regexp.MustCompile(`\b()([A-Z]\w*)\.Builder\.create\(\s*[\w.]+\s*,\s*"([^"]*)"`)
	reKtCreate    = regexp.MustCompile(`\b()([A-Z]\w*)\(\s*[\w.]+\s*,\s*"([^"]*)"`)
)

func javaImport(line string, st *state) {
	m := reJavaImport.FindStringSubmatch(line)
	if m == nil {
		return
	}
	if m[2] == "*" {
		st.wild = append(st.wild, moduleName(m[1]))
		return
	}
	st.classes[m[2]] = moduleName(m[1])
}

var javaSyntax = &syntax{
	scanImport:       javaImport,
	create:           []*regexp.Regexp{reJavaBuilder, reJavaNew},
	resolveQualifier: func(string, *state) string { return "" },
}

var ktSyntax = &syntax{
	scanImport:       javaImport,
	create:           []*regexp.Regexp{reJavaBuilder, reKtCreate},
	resolveQualifier: func(string, *state) string { return "" },
}

// ---------------------------------------------------------------- Go

var (
	reGoImport = regexp.MustCompile(`"github\.com/aws/aws-cdk-go/awscdk(?:/v2)?/aws([a-z0-9]+)"`)
	reGoCreate = regexp.MustCompile(`\b(aws[a-z0-9]+\.)New([A-Z]\w*)\(\s*[\w.]+\s*,\s*jsii\.String\("([^"]*)"\)`)
)

var goSyntax = &syntax{
	scanImport: func(line string, st *state) {
		if m := reGoImport.FindStringSubmatch(line); m != nil {
			st.alias["aws"+m[1]] = moduleName(m[1])
		}
	},
	create: []*regexp.Regexp{reGoCreate},
	resolveQualifier: func(q string, st *state) string {
		return st.alias[q]
	},
}
