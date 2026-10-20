package packages

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/cloudowski/exitctl/internal/model"
)

// MaxLockSize bounds lockfile and SBOM reads; larger files are skipped.
const MaxLockSize = 64 << 20

// LockReader reads lockfiles that sit next to dependency manifests, and SBOM
// files, with small built-in parsers (research R7, revised: syft linked
// about 54 MB of cloud SDKs and network clients for this). It reads local
// files only.
type LockReader struct{}

// NewLockReader returns the built-in lockfile and SBOM reader.
func NewLockReader() LockReader { return LockReader{} }

// ParseErrors lists files that could not be parsed.
type ParseErrors []string

func (e ParseErrors) Error() string { return "could not parse: " + strings.Join(e, ", ") }

// IsLockName reports whether the reader has a lockfile parser for a file
// name. poetry.lock and packages.lock.json are read by ManifestReader.
func IsLockName(name string) bool { return lockParserFor(name) != nil }

func lockParserFor(name string) parseFunc {
	lower := strings.ToLower(name)
	switch {
	case name == "package-lock.json", name == "npm-shrinkwrap.json":
		return parseNPMLock
	case name == "yarn.lock":
		return parseYarnLock
	case name == "pnpm-lock.yaml":
		return parsePnpmLock
	case name == "bun.lock":
		return parseBunLock
	case name == "Pipfile.lock":
		return parsePipfileLock
	case name == "uv.lock", name == "pdm.lock":
		return parsePoetryLock // same [[package]] name/version layout
	case name == "go.sum":
		return parseGoSum
	case strings.HasPrefix(name, "gradle.lockfile"):
		return parseGradleLock
	case strings.HasSuffix(lower, ".deps.json"):
		return parseDepsJSON
	}
	return nil
}

// Packages reads the given repo-relative lockfiles and SBOM files. Files that
// fail to parse are reported in a ParseErrors error; the packages of the
// other files are still returned.
func (LockReader) Packages(ctx context.Context, root string, files []string) ([]Pkg, error) {
	var out []Pkg
	var failed ParseErrors
	for _, rel := range files {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		parse := lockParserFor(path.Base(rel))
		if IsSBOMFile(rel) {
			parse = parseSBOM
		}
		if parse == nil {
			continue
		}
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if st, err := os.Stat(abs); err != nil || st.Size() > MaxLockSize {
			failed = append(failed, rel)
			continue
		}
		b, err := os.ReadFile(abs)
		if err != nil {
			failed = append(failed, rel)
			continue
		}
		ps, err := parse(rel, b)
		if err != nil {
			failed = append(failed, rel)
			continue
		}
		for _, p := range ps {
			p.ManifestPath = rel
			out = append(out, p)
		}
	}
	out = dedupPkgs(out)
	if len(failed) > 0 {
		return out, failed
	}
	return out, nil
}

// lockFilesNear returns the lockfiles in the directories of the given
// manifests (the same places syft used to read), excluding files already in
// manifests. Sorted, repo-relative.
func lockFilesNear(root string, manifests []string) []string {
	known := map[string]bool{}
	dirs := map[string]bool{}
	for _, m := range manifests {
		known[m] = true
		dirs[path.Dir(m)] = true
	}
	var out []string
	for dir := range dirs {
		entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(dir)))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.Type().IsRegular() || !IsLockName(e.Name()) {
				continue
			}
			rel := e.Name()
			if dir != "." {
				rel = dir + "/" + e.Name()
			}
			if !known[rel] {
				out = append(out, rel)
			}
		}
	}
	sort.Strings(out)
	return out
}

func dedupPkgs(ps []Pkg) []Pkg {
	sortPkgs(ps)
	out := ps[:0]
	seen := map[[4]string]bool{}
	for _, p := range ps {
		k := [4]string{p.ManifestPath, string(p.Ecosystem), p.Name, p.Version}
		if !seen[k] {
			seen[k] = true
			out = append(out, p)
		}
	}
	return out
}

// ---------------------------------------------------------------- npm

// parseNPMLock reads package-lock.json / npm-shrinkwrap.json, lockfile
// versions 1 ("dependencies", nested) and 2/3 ("packages").
func parseNPMLock(_ string, data []byte) ([]Pkg, error) {
	var doc struct {
		Packages map[string]struct {
			Name    string `json:"name"`
			Version string `json:"version"`
			Link    bool   `json:"link"`
		} `json:"packages"`
		Dependencies map[string]json.RawMessage `json:"dependencies"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	// One package per name; the shallowest copy (the one the project itself
	// resolves) wins over copies nested under other packages.
	type hit struct {
		version string
		depth   int
	}
	best := map[string]hit{}
	see := func(name, version string, depth int) {
		if h, ok := best[name]; !ok || depth < h.depth || (depth == h.depth && version < h.version) {
			best[name] = hit{version, depth}
		}
	}
	if len(doc.Packages) > 0 {
		for key, p := range doc.Packages {
			i := strings.LastIndex(key, "node_modules/")
			if key == "" || p.Link || i < 0 {
				continue // root project, workspace links, workspace folders
			}
			name := p.Name
			if name == "" {
				name = key[i+len("node_modules/"):]
			}
			see(name, p.Version, strings.Count(key, "node_modules/"))
		}
	} else {
		var walk func(deps map[string]json.RawMessage, depth int) error
		walk = func(deps map[string]json.RawMessage, depth int) error {
			for name, raw := range deps {
				var d struct {
					Version      string                     `json:"version"`
					Dependencies map[string]json.RawMessage `json:"dependencies"`
				}
				if err := json.Unmarshal(raw, &d); err != nil {
					return err
				}
				see(name, d.Version, depth)
				if err := walk(d.Dependencies, depth+1); err != nil {
					return err
				}
			}
			return nil
		}
		if err := walk(doc.Dependencies, 1); err != nil {
			return nil, err
		}
	}
	out := make([]Pkg, 0, len(best))
	for name, h := range best {
		out = append(out, Pkg{Ecosystem: model.EcoNPM, Name: name, Version: h.version})
	}
	return out, nil
}

// specName returns the package name of a yarn/pnpm/bun spec such as
// "@aws-sdk/client-s3@^3.0.0" or "lodash@npm:4.17.21".
func specName(s string) string {
	s = strings.Trim(strings.TrimSpace(s), `"'`)
	from := 0
	if strings.HasPrefix(s, "@") {
		from = 1
	}
	if i := strings.Index(s[from:], "@"); i >= 0 {
		return s[:from+i]
	}
	return s
}

// parseYarnLock reads yarn.lock, classic (v1) and Berry (v2+).
func parseYarnLock(_ string, data []byte) ([]Pkg, error) {
	var out []Pkg
	cur := -1
	for i, raw := range splitLines(data) {
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		if raw[0] != ' ' && raw[0] != '\t' {
			cur = -1
			head := strings.TrimSuffix(strings.TrimSpace(raw), ":")
			if head == raw || strings.HasPrefix(head, "__metadata") {
				continue
			}
			first := strings.Split(head, ",")[0]
			name := specName(first)
			if name == "" {
				continue
			}
			out = append(out, Pkg{Ecosystem: model.EcoNPM, Name: name, Line: i + 1})
			cur = len(out) - 1
			continue
		}
		if cur < 0 {
			continue
		}
		l := strings.TrimSpace(raw)
		if v, ok := strings.CutPrefix(l, "version"); ok && (strings.HasPrefix(v, " ") || strings.HasPrefix(v, ":")) {
			out[cur].Version = strings.Trim(strings.TrimSpace(strings.TrimPrefix(v, ":")), `"'`)
		}
	}
	return out, nil
}

// parsePnpmLock reads pnpm-lock.yaml (lockfile versions 5 to 9).
func parsePnpmLock(_ string, data []byte) ([]Pkg, error) {
	var doc struct {
		LockfileVersion any            `yaml:"lockfileVersion"`
		Packages        map[string]any `yaml:"packages"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	v5 := strings.HasPrefix(fmt.Sprint(doc.LockfileVersion), "5")
	var out []Pkg
	for key := range doc.Packages {
		k := strings.TrimPrefix(key, "/")
		if i := strings.Index(k, "("); i > 0 {
			k = k[:i] // peer dependency suffix
		}
		var name, version string
		if v5 {
			i := strings.LastIndex(k, "/")
			if i <= 0 {
				continue
			}
			name, version = k[:i], k[i+1:]
			if j := strings.Index(version, "_"); j > 0 {
				version = version[:j]
			}
		} else {
			i := strings.LastIndex(k, "@")
			if i <= 0 {
				continue
			}
			name, version = k[:i], k[i+1:]
		}
		out = append(out, Pkg{Ecosystem: model.EcoNPM, Name: name, Version: version})
	}
	return out, nil
}

// parseBunLock reads bun.lock (JSON with comments and trailing commas).
func parseBunLock(_ string, data []byte) ([]Pkg, error) {
	var doc struct {
		Packages map[string][]json.RawMessage `json:"packages"`
	}
	if err := json.Unmarshal(jsonc(data), &doc); err != nil {
		return nil, err
	}
	var out []Pkg
	for _, entry := range doc.Packages {
		if len(entry) == 0 {
			continue
		}
		var spec string
		if json.Unmarshal(entry[0], &spec) != nil {
			continue
		}
		name := specName(spec)
		version := ""
		if i := len(name) + 1; i < len(spec) {
			version = strings.TrimPrefix(spec[i:], "npm:")
		}
		if name != "" && !strings.Contains(version, "workspace:") {
			out = append(out, Pkg{Ecosystem: model.EcoNPM, Name: name, Version: version})
		}
	}
	return out, nil
}

// jsonc removes // and /* */ comments and trailing commas outside strings.
func jsonc(b []byte) []byte {
	out := make([]byte, 0, len(b))
	inStr, esc := false, false
	for i := 0; i < len(b); i++ {
		c := b[i]
		if inStr {
			out = append(out, c)
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch {
		case c == '"':
			inStr = true
			out = append(out, c)
		case c == '/' && i+1 < len(b) && b[i+1] == '/':
			for i < len(b) && b[i] != '\n' {
				i++
			}
			out = append(out, '\n')
		case c == '/' && i+1 < len(b) && b[i+1] == '*':
			i += 2
			for i+1 < len(b) && (b[i] != '*' || b[i+1] != '/') {
				i++
			}
			i++
		case c == ',':
			j := i + 1
			for j < len(b) && (b[j] == ' ' || b[j] == '\n' || b[j] == '\r' || b[j] == '\t') {
				j++
			}
			if j < len(b) && (b[j] == '}' || b[j] == ']') {
				continue // trailing comma
			}
			out = append(out, c)
		default:
			out = append(out, c)
		}
	}
	return out
}

// ---------------------------------------------------------------- python

func parsePipfileLock(_ string, data []byte) ([]Pkg, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	var out []Pkg
	for _, section := range []string{"default", "develop"} {
		var deps map[string]struct {
			Version string `json:"version"`
		}
		if raw, ok := doc[section]; ok {
			if err := json.Unmarshal(raw, &deps); err != nil {
				return nil, err
			}
		}
		for name, d := range deps {
			out = append(out, Pkg{Ecosystem: model.EcoPyPI, Name: normalizePyPI(name), Version: strings.TrimPrefix(d.Version, "==")})
		}
	}
	return out, nil
}

// ---------------------------------------------------------------- go

func parseGoSum(_ string, data []byte) ([]Pkg, error) {
	var out []Pkg
	for i, l := range splitLines(data) {
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		// go.sum lists every version that was ever considered; the version
		// in use is the one in go.mod, so no version is taken from here.
		out = append(out, Pkg{Ecosystem: model.EcoGo, Name: f[0], Line: i + 1})
	}
	return out, nil
}

// ---------------------------------------------------------------- java

func parseGradleLock(_ string, data []byte) ([]Pkg, error) {
	var out []Pkg
	for i, raw := range splitLines(data) {
		l := strings.TrimSpace(raw)
		if l == "" || strings.HasPrefix(l, "#") || strings.HasPrefix(l, "empty=") {
			continue
		}
		coord, _, _ := strings.Cut(l, "=")
		parts := strings.Split(coord, ":")
		if len(parts) < 3 {
			continue
		}
		out = append(out, Pkg{Ecosystem: model.EcoMaven, Name: parts[0] + ":" + parts[1], Version: parts[2], Line: i + 1})
	}
	return out, nil
}

// ---------------------------------------------------------------- .NET

func parseDepsJSON(_ string, data []byte) ([]Pkg, error) {
	var doc struct {
		Libraries map[string]struct {
			Type string `json:"type"`
		} `json:"libraries"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	var out []Pkg
	for key, lib := range doc.Libraries {
		name, version, ok := strings.Cut(key, "/")
		if !ok || lib.Type != "package" {
			continue
		}
		out = append(out, Pkg{Ecosystem: model.EcoNuGet, Name: name, Version: version})
	}
	return out, nil
}

// ---------------------------------------------------------------- SBOM

// rePURL finds package URLs in any SBOM format (CycloneDX JSON/XML, SPDX
// JSON/tag-value, syft JSON): every format carries a purl per package.
var rePURL = regexp.MustCompile(`pkg:[A-Za-z][A-Za-z0-9.+-]*/[^\s"'<>,\\]+`)

// parseSBOM extracts packages from the package URLs of an SBOM, with the
// line of their first occurrence.
func parseSBOM(_ string, data []byte) ([]Pkg, error) {
	var out []Pkg
	for i, l := range splitLines(data) {
		l = strings.ReplaceAll(l, `\/`, `/`)
		for _, purl := range rePURL.FindAllString(l, -1) {
			if eco, name, version, ok := purlPackage(purl); ok {
				out = append(out, Pkg{Ecosystem: eco, Name: name, Version: version, Line: i + 1})
			}
		}
	}
	return out, nil
}

func sortPkgs(ps []Pkg) {
	sort.SliceStable(ps, func(i, j int) bool {
		a, b := ps[i], ps[j]
		if a.ManifestPath != b.ManifestPath {
			return a.ManifestPath < b.ManifestPath
		}
		if a.Ecosystem != b.Ecosystem {
			return a.Ecosystem < b.Ecosystem
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Version < b.Version
	})
}
