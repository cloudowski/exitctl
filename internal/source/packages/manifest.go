package packages

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"

	"github.com/cloudowski/exitctl/internal/model"
)

// ManifestReader is a small built-in PackageDetector for direct dependency
// manifests. It reports the exact line of every dependency.
type ManifestReader struct{}

// errUnsupported marks a file the reader has no parser for.
var errUnsupported = errors.New("unsupported manifest")

// Packages reads every supported manifest. Unsupported files are ignored;
// files that cannot be parsed are skipped and reported in the joined error.
func (r ManifestReader) Packages(ctx context.Context, root string, manifests []string) ([]Pkg, error) {
	var out []Pkg
	var errs []error
	for _, m := range manifests {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		ps, err := r.ReadFile(root, m)
		if errors.Is(err, errUnsupported) {
			continue
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", m, err))
			continue
		}
		out = append(out, ps...)
	}
	sortPkgs(out)
	return out, errors.Join(errs...)
}

// Supports reports whether the reader has a parser for the file name.
func (ManifestReader) Supports(rel string) bool { return readerFor(path.Base(rel)) != nil }

// ReadFile parses one manifest (rel is repo-relative with '/').
func (ManifestReader) ReadFile(root, rel string) ([]Pkg, error) {
	fn := readerFor(path.Base(rel))
	if fn == nil {
		return nil, errUnsupported
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return nil, err
	}
	ps, err := fn(rel, data)
	if err != nil {
		return nil, err
	}
	for i := range ps {
		ps[i].ManifestPath = rel
		if ps[i].Line < 1 {
			ps[i].Line = 1
		}
	}
	return ps, nil
}

type parseFunc func(rel string, data []byte) ([]Pkg, error)

func readerFor(name string) parseFunc {
	lower := strings.ToLower(name)
	switch {
	case name == "package.json":
		return parsePackageJSON
	case strings.HasPrefix(name, "requirements") && strings.HasSuffix(lower, ".txt"):
		return parseRequirements
	case name == "pyproject.toml":
		return parsePyProject
	case name == "setup.py":
		return parseSetupPy
	case name == "Pipfile":
		return parsePipfile
	case name == "poetry.lock":
		return parsePoetryLock
	case name == "go.mod":
		return parseGoMod
	case name == "pom.xml":
		return parsePom
	case name == "build.gradle" || name == "build.gradle.kts":
		return parseGradle
	case strings.HasSuffix(lower, ".csproj"):
		return parseCsproj
	case name == "packages.lock.json":
		return parseNuGetLock
	case name == "Gemfile":
		return parseGemfile
	case name == "Gemfile.lock":
		return parseGemfileLock
	case name == "composer.json":
		return parseComposerJSON
	case name == "composer.lock":
		return parseComposerLock
	case name == "Cargo.toml":
		return parseCargoToml
	case name == "Cargo.lock":
		return parseCargoLock
	case name == "build.sbt":
		return parseSbt
	}
	return nil
}

func splitLines(data []byte) []string {
	s := strings.ReplaceAll(string(data), "\r\n", "\n")
	return strings.Split(s, "\n")
}

// ---------------------------------------------------------------- npm

func parsePackageJSON(_ string, data []byte) ([]Pkg, error) {
	var doc struct {
		Dependencies    map[string]any `json:"dependencies"`
		DevDependencies map[string]any `json:"devDependencies"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	lines := splitLines(data)
	var out []Pkg
	add := func(m map[string]any) {
		names := make([]string, 0, len(m))
		for n := range m {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			v, _ := m[n].(string)
			out = append(out, Pkg{Ecosystem: model.EcoNPM, Name: n, Version: v, Line: jsonKeyLine(lines, n)})
		}
	}
	add(doc.Dependencies)
	add(doc.DevDependencies)
	return out, nil
}

// jsonKeyLine returns the first line whose trimmed text starts with the
// quoted key followed by ':', else the first line mentioning the name.
func jsonKeyLine(lines []string, key string) int {
	q := `"` + key + `"`
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, q) && strings.HasPrefix(strings.TrimSpace(t[len(q):]), ":") {
			return i + 1
		}
	}
	return FindLine(lines, key)
}

// ---------------------------------------------------------------- python

var (
	rePyName    = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9._-]*)\s*(\[[^\]]*\])?\s*(.*)$`)
	rePyPinned  = regexp.MustCompile(`===?\s*([^\s;,]+)`)
	rePyNormSep = regexp.MustCompile(`[-_.]+`)
)

// normalizePyPI applies PEP 503 name normalization.
func normalizePyPI(n string) string {
	return rePyNormSep.ReplaceAllString(strings.ToLower(strings.TrimSpace(n)), "-")
}

// parseRequirement parses a PEP 508 requirement string ("boto3>=1.2; ...").
func parseRequirement(s string) (name, version string, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasPrefix(s, "-") {
		return "", "", false
	}
	m := rePyName.FindStringSubmatch(s)
	if m == nil {
		return "", "", false
	}
	rest := strings.TrimSpace(m[3])
	if strings.Contains(s, "://") && !strings.HasPrefix(rest, "@") {
		return "", "", false
	}
	if v := rePyPinned.FindStringSubmatch(rest); v != nil {
		version = v[1]
	}
	return normalizePyPI(m[1]), version, true
}

func parseRequirements(_ string, data []byte) ([]Pkg, error) {
	var out []Pkg
	for i, l := range splitLines(data) {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if j := strings.Index(t, " #"); j >= 0 {
			t = strings.TrimSpace(t[:j])
		}
		t = strings.TrimSpace(strings.TrimSuffix(t, "\\"))
		if n, v, ok := parseRequirement(t); ok {
			out = append(out, Pkg{Ecosystem: model.EcoPyPI, Name: n, Version: v, Line: i + 1})
		}
	}
	return out, nil
}

var (
	reTOMLSection = regexp.MustCompile(`^\[\[?\s*([^\]]+?)\s*\]\]?\s*(#.*)?$`)
	reTOMLKey     = regexp.MustCompile(`^("[^"]+"|'[^']+'|[A-Za-z0-9_.-]+)\s*=\s*(.*)$`)
	reTOMLString  = regexp.MustCompile(`"((?:[^"\\]|\\.)*)"|'([^']*)'`)
)

// tomlStrings returns the string literals on a line.
func tomlStrings(l string) []string {
	var out []string
	for _, m := range reTOMLString.FindAllStringSubmatch(l, -1) {
		if m[1] != "" {
			out = append(out, m[1])
		} else {
			out = append(out, m[2])
		}
	}
	return out
}

// tomlArrayClosed reports whether a line closes an array (']' outside strings).
func tomlArrayClosed(l string) bool {
	s := reTOMLString.ReplaceAllString(l, "")
	if i := strings.Index(s, "#"); i >= 0 {
		s = s[:i]
	}
	return strings.Contains(s, "]")
}

// tomlVisitor is a minimal line-based TOML scanner that extracts PyPI
// dependencies with their line numbers.
type tomlVisitor struct {
	// arrayKey decides whether string items of the array at section/key are
	// PEP 508 requirements.
	arrayKey func(section, key string) bool
	// tableKey decides whether keys of the section name packages.
	tableKey func(section string) bool
}

func (v tomlVisitor) walk(data []byte) []Pkg {
	var out []Pkg
	section := ""
	inArray := false
	for i, raw := range splitLines(data) {
		l := strings.TrimSpace(raw)
		if inArray {
			for _, s := range tomlStrings(l) {
				if n, ver, ok := parseRequirement(s); ok {
					out = append(out, Pkg{Ecosystem: model.EcoPyPI, Name: n, Version: ver, Line: i + 1})
				}
			}
			if tomlArrayClosed(l) {
				inArray = false
			}
			continue
		}
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		if m := reTOMLSection.FindStringSubmatch(l); m != nil && strings.HasPrefix(l, "[") {
			section = strings.ReplaceAll(m[1], " ", "")
			continue
		}
		m := reTOMLKey.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		key := strings.Trim(m[1], `"'`)
		val := strings.TrimSpace(m[2])
		if v.arrayKey != nil && v.arrayKey(section, key) && strings.HasPrefix(val, "[") {
			for _, s := range tomlStrings(val) {
				if n, ver, ok := parseRequirement(s); ok {
					out = append(out, Pkg{Ecosystem: model.EcoPyPI, Name: n, Version: ver, Line: i + 1})
				}
			}
			inArray = !tomlArrayClosed(val[1:])
			continue
		}
		if v.tableKey != nil && v.tableKey(section) && !strings.EqualFold(key, "python") {
			ver := ""
			if strings.HasPrefix(val, `"`) || strings.HasPrefix(val, `'`) {
				if ss := tomlStrings(val); len(ss) > 0 && ss[0] != "*" {
					ver = ss[0]
				}
			}
			out = append(out, Pkg{Ecosystem: model.EcoPyPI, Name: normalizePyPI(key), Version: ver, Line: i + 1})
		}
	}
	return out
}

func parsePyProject(_ string, data []byte) ([]Pkg, error) {
	v := tomlVisitor{
		arrayKey: func(section, key string) bool {
			switch {
			case section == "project" && key == "dependencies":
				return true
			case section == "project.optional-dependencies", section == "dependency-groups":
				return true
			case section == "build-system" && key == "requires":
				return false
			}
			return false
		},
		tableKey: func(section string) bool {
			if section == "tool.poetry.dependencies" || section == "tool.poetry.dev-dependencies" {
				return true
			}
			return strings.HasPrefix(section, "tool.poetry.group.") && strings.HasSuffix(section, ".dependencies")
		},
	}
	return v.walk(data), nil
}

// parseSetupPy reads the string literals of install_requires=[...] (and
// setup_requires) in setup.py. It does not execute the file.
func parseSetupPy(_ string, data []byte) ([]Pkg, error) {
	var out []Pkg
	in := false
	for i, raw := range splitLines(data) {
		l := raw
		if !in {
			j := strings.Index(l, "install_requires")
			if j < 0 {
				continue
			}
			k := strings.Index(l[j:], "[")
			if k < 0 {
				continue
			}
			l = l[j+k+1:]
			in = true
		}
		for _, s := range tomlStrings(l) {
			if n, ver, ok := parseRequirement(s); ok {
				out = append(out, Pkg{Ecosystem: model.EcoPyPI, Name: n, Version: ver, Line: i + 1})
			}
		}
		if tomlArrayClosed(l) {
			in = false
		}
	}
	return out, nil
}

func parsePipfile(_ string, data []byte) ([]Pkg, error) {
	v := tomlVisitor{tableKey: func(section string) bool {
		return section == "packages" || section == "dev-packages"
	}}
	return v.walk(data), nil
}

func parsePoetryLock(_ string, data []byte) ([]Pkg, error) {
	var out []Pkg
	inPkg := false
	cur := -1
	for i, raw := range splitLines(data) {
		l := strings.TrimSpace(raw)
		if strings.HasPrefix(l, "[") {
			inPkg = l == "[[package]]"
			cur = -1
			continue
		}
		if !inPkg {
			continue
		}
		m := reTOMLKey.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		ss := tomlStrings(m[2])
		if len(ss) == 0 {
			continue
		}
		switch m[1] {
		case "name":
			out = append(out, Pkg{Ecosystem: model.EcoPyPI, Name: normalizePyPI(ss[0]), Line: i + 1})
			cur = len(out) - 1
		case "version":
			if cur >= 0 {
				out[cur].Version = ss[0]
			}
		}
	}
	return out, nil
}

// ---------------------------------------------------------------- go

func parseGoMod(rel string, data []byte) ([]Pkg, error) {
	f, err := modfile.ParseLax(rel, data, nil)
	if err != nil {
		return nil, err
	}
	var out []Pkg
	for _, r := range f.Require {
		line := 1
		if r.Syntax != nil {
			line = r.Syntax.Start.Line
		}
		out = append(out, Pkg{Ecosystem: model.EcoGo, Name: r.Mod.Path, Version: r.Mod.Version, Line: line})
	}
	return out, nil
}

// ---------------------------------------------------------------- maven

var reProp = regexp.MustCompile(`\$\{([^}]+)\}`)

func parsePom(_ string, data []byte) ([]Pkg, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	var stack []string
	props := map[string]string{}
	var projVersion string
	type dep struct {
		group, artifact, version string
		line                     int
	}
	var deps []dep
	var cur *dep
	var text strings.Builder
	inPlugin := 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			name := t.Name.Local
			stack = append(stack, name)
			text.Reset()
			if name == "plugin" {
				inPlugin++
			}
			if name == "dependency" && inPlugin == 0 {
				line, _ := dec.InputPos()
				cur = &dep{line: line}
			}
			if cur != nil && name == "artifactId" {
				cur.line, _ = dec.InputPos()
			}
		case xml.CharData:
			text.Write(t)
		case xml.EndElement:
			name := t.Name.Local
			val := strings.TrimSpace(text.String())
			depth := len(stack)
			switch {
			case cur != nil && depth >= 2 && stack[depth-2] == "dependency":
				switch name {
				case "groupId":
					cur.group = val
				case "artifactId":
					cur.artifact = val
				case "version":
					cur.version = val
				}
			case depth == 3 && stack[0] == "project" && stack[1] == "properties":
				props[name] = val
			case depth == 2 && stack[0] == "project" && name == "version":
				projVersion = val
			}
			if name == "dependency" && cur != nil {
				deps = append(deps, *cur)
				cur = nil
			}
			if name == "plugin" && inPlugin > 0 {
				inPlugin--
			}
			if depth > 0 {
				stack = stack[:depth-1]
			}
			text.Reset()
		}
	}
	if projVersion != "" {
		props["project.version"] = projVersion
	}
	resolve := func(s string) string {
		s = reProp.ReplaceAllStringFunc(s, func(m string) string {
			if v, ok := props[m[2:len(m)-1]]; ok {
				return v
			}
			return m
		})
		if strings.Contains(s, "${") {
			return ""
		}
		return s
	}
	var out []Pkg
	for _, d := range deps {
		g, a := resolve(d.group), resolve(d.artifact)
		if a == "" {
			continue
		}
		name := a
		if g != "" {
			name = g + ":" + a
		}
		out = append(out, Pkg{Ecosystem: model.EcoMaven, Name: name, Version: resolve(d.version), Line: d.line})
	}
	return out, nil
}

var reGradleDep = regexp.MustCompile(`\b(?:implementation|api|compile|compileOnly|runtimeOnly|runtime|testImplementation|testCompile|testRuntimeOnly|annotationProcessor|kapt|ksp)\s*\(?\s*(?:platform\s*\(\s*)?["']([^:"'\s]+):([^:"'\s]+)(?::([^:"'\s@]+))?[^"']*["']`)

func parseGradle(_ string, data []byte) ([]Pkg, error) {
	var out []Pkg
	inBlock := false
	for i, raw := range splitLines(data) {
		l := strings.TrimSpace(raw)
		if inBlock {
			if strings.Contains(l, "*/") {
				inBlock = false
			}
			continue
		}
		if strings.HasPrefix(l, "/*") {
			inBlock = !strings.Contains(l, "*/")
			continue
		}
		if strings.HasPrefix(l, "//") {
			continue
		}
		for _, m := range reGradleDep.FindAllStringSubmatch(l, -1) {
			v := m[3]
			if strings.Contains(v, "$") {
				v = ""
			}
			out = append(out, Pkg{Ecosystem: model.EcoMaven, Name: m[1] + ":" + m[2], Version: v, Line: i + 1})
		}
	}
	return out, nil
}

// ---------------------------------------------------------------- nuget

func parseCsproj(_ string, data []byte) ([]Pkg, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	var out []Pkg
	cur := -1
	inVersion := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch {
			case t.Name.Local == "PackageReference":
				line, _ := dec.InputPos()
				p := Pkg{Ecosystem: model.EcoNuGet, Line: line}
				for _, a := range t.Attr {
					switch a.Name.Local {
					case "Include":
						p.Name = strings.TrimSpace(a.Value)
					case "Version":
						p.Version = strings.TrimSpace(a.Value)
					}
				}
				cur = -1
				if p.Name != "" {
					out = append(out, p)
					cur = len(out) - 1
				}
			case t.Name.Local == "Version" && cur >= 0:
				inVersion = true
			}
		case xml.CharData:
			if inVersion && cur >= 0 {
				out[cur].Version = strings.TrimSpace(string(t))
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "Version":
				inVersion = false
			case "PackageReference":
				cur = -1
			}
		}
	}
	return out, nil
}

func parseNuGetLock(_ string, data []byte) ([]Pkg, error) {
	var doc struct {
		Dependencies map[string]map[string]struct {
			Type     string `json:"type"`
			Resolved string `json:"resolved"`
		} `json:"dependencies"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	lines := splitLines(data)
	seen := map[string]bool{}
	var out []Pkg
	fws := make([]string, 0, len(doc.Dependencies))
	for fw := range doc.Dependencies {
		fws = append(fws, fw)
	}
	sort.Strings(fws)
	for _, fw := range fws {
		names := make([]string, 0, len(doc.Dependencies[fw]))
		for n := range doc.Dependencies[fw] {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			d := doc.Dependencies[fw][n]
			if !strings.EqualFold(d.Type, "Direct") || seen[n] {
				continue
			}
			seen[n] = true
			out = append(out, Pkg{Ecosystem: model.EcoNuGet, Name: n, Version: d.Resolved, Line: jsonKeyLine(lines, n)})
		}
	}
	return out, nil
}

// ---------------------------------------------------------------- lines

// FindLine returns the 1-based number of the first line that mentions name
// as a whole token (not part of a longer package name), or 1. For Maven
// "group:artifact" names the artifact ID, and for names with "/" the last
// segment, are searched when the full name is not found.
func FindLine(lines []string, name string) int {
	if name == "" {
		return 1
	}
	cands := []string{name}
	if g, a, ok := strings.Cut(name, ":"); ok && g != "" && a != "" {
		cands = append(cands, a)
	}
	if i := strings.LastIndex(name, "/"); i >= 0 && i < len(name)-1 {
		cands = append(cands, name[i+1:]) // e.g. scoped npm names split in SBOMs
	}
	for _, c := range cands {
		for i, l := range lines {
			if containsToken(l, c) {
				return i + 1
			}
			if containsToken(strings.ToLower(l), strings.ToLower(c)) {
				return i + 1
			}
		}
	}
	return 1
}

func containsToken(s, tok string) bool {
	for from := 0; from <= len(s)-len(tok); {
		i := strings.Index(s[from:], tok)
		if i < 0 {
			return false
		}
		i += from
		j := i + len(tok)
		if (i == 0 || !nameByte(s[i-1])) && (j == len(s) || !nameByte(s[j])) {
			return true
		}
		from = i + 1
	}
	return false
}

func nameByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
		c == '_' || c == '-' || c == '.' || c == '/' || c == '@'
}
