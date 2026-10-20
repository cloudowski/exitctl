// Package walk does the single deterministic directory walk of a scan and
// classifies files into source kinds for the detectors.
package walk

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"

	"github.com/cloudowski/exitctl/internal/model"
)

// MaxFileSize is the largest file the walk hands to detectors (5 MiB).
const MaxFileSize = 5 << 20

// Neutral notes for skipped sources.
const (
	NoteTooLarge     = "file larger than 5 MiB"
	NoteNotUTF8      = "not UTF-8 text"
	NoteUnreadable   = "file could not be read"
	NoteOutsideRoot  = "symlink target outside scan root"
	NoteSymlinkError = "symlink could not be resolved"
)

// sniffLimit bounds how much of an oversized YAML/JSON file is read to decide
// whether it would have been a Kubernetes or CloudFormation source.
const sniffLimit = 64 << 10

// Index is the result of one walk. All paths are relative to Root with '/'
// separators, and every slice is sorted.
type Index struct {
	// Root is the absolute, cleaned scan root.
	Root string
	// Files lists classified files per source kind (only non-empty kinds).
	Files map[model.SourceKind][]string
	// Skipped lists files that would have been sources but were not read.
	// Repo is left empty for the caller to fill.
	Skipped []model.Source
	// HelmCharts lists directories containing Chart.yaml ("." for the root).
	// Charts vendored under another chart's charts/ directory are not listed.
	HelmCharts []string
	// Kustomizations lists directories containing kustomization.yaml,
	// kustomization.yml or Kustomization ("." for the root).
	Kustomizations []string
}

// defaultSkipDirs are directory names never descended into.
var defaultSkipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, ".venv": true, "venv": true,
	"__pycache__": true, "dist": true, "build": true, "target": true, "bin": true,
	"obj": true, ".exitctl": true, ".idea": true, ".vscode": true,
}

var codeExts = map[string]bool{
	".js": true, ".mjs": true, ".cjs": true, ".ts": true, ".tsx": true, ".py": true,
	".go": true, ".java": true, ".kt": true, ".cs": true,
	".rb": true, ".rake": true, ".php": true, ".rs": true, ".scala": true, ".sc": true,
}

var manifestNames = map[string]bool{
	"package.json": true, "pyproject.toml": true, "Pipfile": true, "poetry.lock": true,
	"go.mod": true, "pom.xml": true, "build.gradle": true, "build.gradle.kts": true,
	"packages.lock.json": true, "setup.py": true,
	"Gemfile": true, "Gemfile.lock": true, "composer.json": true, "composer.lock": true,
	"Cargo.toml": true, "Cargo.lock": true, "build.sbt": true,
}

// IsSBOMName reports whether a file name looks like an SBOM document
// (the CycloneDX, SPDX, and syft JSON names syft's SBOM cataloger reads).
func IsSBOMName(rel string) bool {
	n := strings.ToLower(path.Base(rel))
	if n == "bom" || n == "sbom" || strings.HasSuffix(n, ".syft.json") {
		return true
	}
	for _, s := range []string{".bom", ".sbom", ".cdx", ".spdx"} {
		if strings.HasSuffix(n, s) || strings.Contains(n, s+".") {
			return true
		}
	}
	return false
}

var kustomizationNames = []string{"kustomization.yaml", "kustomization.yml", "Kustomization"}

var (
	reAPIVersion = regexp.MustCompile(`(?m)^["']?apiVersion["']?\s*:\s*\S`)
	reKind       = regexp.MustCompile(`(?m)^["']?kind["']?\s*:\s*\S`)
	reCFNVersion = regexp.MustCompile(`(?m)^\s*["']?AWSTemplateFormatVersion["']?\s*:`)
	reCFNType    = regexp.MustCompile(`["']?Type["']?\s*:\s*["']?AWS::`)
	reCFNRes     = regexp.MustCompile(`(?m)^\s*["']?Resources["']?\s*:`)
)

type walker struct {
	root    string
	ignores []string
	idx     Index
	files   map[model.SourceKind]map[string]bool
	charts  []string // chart dirs, used to exclude templates/ and charts/
}

// Walk walks root once in sorted order and classifies files. extraIgnores are
// glob patterns relative to root: "**" matches any number of path segments,
// other segments use path.Match; a pattern without "/" matches the base name
// at any depth; a trailing "/" is ignored.
func Walk(root string, extraIgnores []string) (Index, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return Index{}, fmt.Errorf("scan root %s: %w", root, err)
	}
	abs = filepath.Clean(abs)
	fi, err := os.Stat(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Index{}, fmt.Errorf("scan root %s does not exist", root)
		}
		return Index{}, fmt.Errorf("scan root %s cannot be read", root)
	}
	if !fi.IsDir() {
		return Index{}, fmt.Errorf("scan root %s is not a directory", root)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return Index{}, fmt.Errorf("scan root %s cannot be resolved", root)
	}
	w := &walker{
		root:  real,
		files: map[model.SourceKind]map[string]bool{},
	}
	for _, p := range extraIgnores {
		p = strings.TrimSpace(strings.ReplaceAll(p, "\\", "/"))
		p = strings.TrimSuffix(strings.TrimPrefix(p, "./"), "/")
		if p != "" {
			w.ignores = append(w.ignores, p)
		}
	}
	w.idx.Root = abs
	w.walkDir(real, "", []string{real})

	w.idx.Files = map[model.SourceKind][]string{}
	for k, set := range w.files {
		list := make([]string, 0, len(set))
		for p := range set {
			list = append(list, p)
		}
		sort.Strings(list)
		w.idx.Files[k] = list
	}
	sort.SliceStable(w.idx.Skipped, func(i, j int) bool {
		a, b := w.idx.Skipped[i], w.idx.Skipped[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Kind < b.Kind
	})
	sort.Strings(w.idx.HelmCharts)
	sort.Strings(w.idx.Kustomizations)
	if w.idx.Skipped == nil {
		w.idx.Skipped = []model.Source{}
	}
	if w.idx.HelmCharts == nil {
		w.idx.HelmCharts = []string{}
	}
	if w.idx.Kustomizations == nil {
		w.idx.Kustomizations = []string{}
	}
	return w.idx, nil
}

func relJoin(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

func dirName(rel string) string {
	if rel == "" {
		return "."
	}
	return rel
}

// walkDir visits the real directory abs whose root-relative path is rel.
// stack holds the real paths of the directories being visited, for symlink
// loop detection.
func (w *walker) walkDir(abs, rel string, stack []string) {
	entries, err := os.ReadDir(abs)
	if err != nil {
		return
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	names := map[string]bool{}
	for _, e := range entries {
		names[e.Name()] = true
	}
	if names["Chart.yaml"] && !w.inChartDeps(rel) {
		w.idx.HelmCharts = append(w.idx.HelmCharts, dirName(rel))
		w.charts = append(w.charts, rel)
	}
	for _, n := range kustomizationNames {
		if names[n] {
			w.idx.Kustomizations = append(w.idx.Kustomizations, dirName(rel))
			break
		}
	}

	onlyModules := path.Base("/"+rel) == ".terraform"
	for _, e := range entries {
		name := e.Name()
		childRel := relJoin(rel, name)
		childAbs := filepath.Join(abs, name)
		if w.ignored(childRel) {
			continue
		}
		mode := e.Type()
		isLink := mode&os.ModeSymlink != 0
		targetAbs := childAbs
		var info os.FileInfo
		if isLink {
			t, err := filepath.EvalSymlinks(childAbs)
			if err != nil {
				if k := w.kindByName(childRel); k != "" {
					w.skip(childRel, k, NoteSymlinkError)
				}
				continue
			}
			if !within(w.root, t) {
				if k := w.kindByName(childRel); k != "" {
					w.skip(childRel, k, NoteOutsideRoot)
				}
				continue
			}
			targetAbs = t
			info, err = os.Stat(t)
			if err != nil {
				continue
			}
		} else {
			info, err = e.Info()
			if err != nil {
				continue
			}
		}

		if info.IsDir() {
			if onlyModules && name != "modules" {
				continue
			}
			if defaultSkipDirs[name] {
				continue
			}
			real := targetAbs
			if !isLink {
				real = childAbs
			}
			if onStack(stack, real) {
				continue
			}
			w.walkDir(targetAbs, childRel, append(stack, real))
			continue
		}
		if onlyModules {
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		w.visitFile(targetAbs, childRel, info.Size())
	}
}

func onStack(stack []string, p string) bool {
	for _, s := range stack {
		if s == p {
			return true
		}
	}
	return false
}

func within(root, p string) bool {
	if p == root {
		return true
	}
	r, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator))
}

// inChartDeps reports whether rel lies under the templates/ or charts/
// directory of an already found Helm chart.
func (w *walker) inChartDeps(rel string) bool {
	for _, c := range w.charts {
		for _, sub := range []string{"templates", "charts"} {
			p := relJoin(c, sub)
			if rel == p || strings.HasPrefix(rel, p+"/") {
				return true
			}
		}
	}
	return false
}

// ignored reports whether rel matches one of the extra ignore patterns.
func (w *walker) ignored(rel string) bool {
	for _, p := range w.ignores {
		if Match(p, rel) {
			return true
		}
	}
	return false
}

// Match reports whether the '/'-separated relative path rel matches the
// glob pattern. "**" matches zero or more segments; other segments use
// path.Match. A pattern without '/' matches the last segment of rel.
func Match(pattern, rel string) bool {
	pattern = strings.TrimSuffix(strings.TrimPrefix(pattern, "/"), "/")
	if pattern == "" {
		return false
	}
	if !strings.Contains(pattern, "/") {
		ok, _ := path.Match(pattern, path.Base(rel))
		return ok
	}
	return matchSegs(strings.Split(pattern, "/"), strings.Split(rel, "/"))
}

func matchSegs(pat, segs []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			rest := pat[1:]
			for i := 0; i <= len(segs); i++ {
				if matchSegs(rest, segs[i:]) {
					return true
				}
			}
			return false
		}
		if len(segs) == 0 {
			return false
		}
		if ok, _ := path.Match(pat[0], segs[0]); !ok {
			return false
		}
		pat, segs = pat[1:], segs[1:]
	}
	return len(segs) == 0
}

// kindByName classifies a file by its path alone. It returns "" for files
// that need a content sniff (YAML/JSON) or are not sources.
func (w *walker) kindByName(rel string) model.SourceKind {
	name := path.Base(rel)
	lower := strings.ToLower(name)
	ext := strings.ToLower(path.Ext(name))
	dir := path.Dir(rel)
	switch {
	case IsSBOMName(rel):
		return model.KindSBOM
	case ext == ".tf" || ext == ".tofu":
		return model.KindTerraform
	case name == "Pulumi.yaml" || name == "Pulumi.yml":
		return model.KindPulumi
	case strings.HasPrefix(lower, "dockerfile") || ext == ".dockerfile":
		return model.KindDockerfile
	case (ext == ".yml" || ext == ".yaml") && (dir == ".github/workflows" || strings.HasSuffix(dir, "/.github/workflows")):
		return model.KindGitHubActions
	case name == ".gitlab-ci.yml":
		return model.KindGitLabCI
	case manifestNames[name] || ext == ".csproj" ||
		(strings.HasPrefix(name, "requirements") && ext == ".txt"):
		return model.KindPackageManifest
	case codeExts[ext]:
		return model.KindCode
	}
	return ""
}

// sniffCandidate reports whether a file may be a Kubernetes manifest or a
// CloudFormation template and needs a content sniff.
func sniffCandidate(rel string) bool {
	name := path.Base(rel)
	for _, n := range kustomizationNames {
		if name == n {
			return false
		}
	}
	if name == "Chart.yaml" || name == "Chart.lock" {
		return false
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".yaml", ".yml", ".json", ".template":
		return true
	}
	return false
}

func (w *walker) add(kind model.SourceKind, rel string) {
	set := w.files[kind]
	if set == nil {
		set = map[string]bool{}
		w.files[kind] = set
	}
	set[rel] = true
}

func (w *walker) skip(rel string, kind model.SourceKind, note string) {
	w.idx.Skipped = append(w.idx.Skipped, model.Source{
		Path: rel, Kind: kind, Status: model.StatusSkipped, Note: note,
	})
}

func (w *walker) visitFile(abs, rel string, size int64) {
	kind := w.kindByName(rel)
	sniff := false
	if kind == "" {
		if !sniffCandidate(rel) {
			return
		}
		sniff = true
	}
	if size > MaxFileSize {
		if sniff {
			kind = w.sniffPrefix(abs, rel)
			if kind == "" {
				return
			}
		}
		w.skip(rel, kind, NoteTooLarge)
		return
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		if !sniff {
			w.skip(rel, kind, NoteUnreadable)
		}
		return
	}
	if sniff {
		kind = w.sniff(rel, data)
		if kind == "" {
			return
		}
	}
	if !utf8.Valid(data) {
		w.skip(rel, kind, NoteNotUTF8)
		return
	}
	w.add(kind, rel)
}

func (w *walker) sniffPrefix(abs, rel string) model.SourceKind {
	f, err := os.Open(abs)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	buf, _ := io.ReadAll(io.LimitReader(f, sniffLimit))
	return w.sniffText(rel, buf)
}

// sniff decides between CloudFormation, Kubernetes, or nothing for a
// YAML/JSON file, by parsing it and falling back to a line-based sniff when
// the file does not parse (e.g. templated YAML).
func (w *walker) sniff(rel string, data []byte) model.SourceKind {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	k8s, parsed := false, false
	for i := 0; i < 1000; i++ {
		var doc yaml.Node
		err := dec.Decode(&doc)
		if err == io.EOF {
			break
		}
		if err != nil {
			parsed = false
			break
		}
		parsed = true
		m := topMapping(&doc)
		if m == nil {
			continue
		}
		if isCFN(m) {
			return model.KindCloudFormation
		}
		if hasScalarKey(m, "apiVersion") && hasScalarKey(m, "kind") {
			k8s = true
		}
	}
	if parsed {
		if k8s && !w.inChartDeps(path.Dir(rel)) {
			return model.KindKubernetes
		}
		return ""
	}
	return w.sniffText(rel, data)
}

func (w *walker) sniffText(rel string, data []byte) model.SourceKind {
	if reCFNVersion.Match(data) || (reCFNRes.Match(data) && reCFNType.Match(data)) {
		return model.KindCloudFormation
	}
	if reAPIVersion.Match(data) && reKind.Match(data) && !w.inChartDeps(path.Dir(rel)) {
		return model.KindKubernetes
	}
	return ""
}

func topMapping(doc *yaml.Node) *yaml.Node {
	n := doc
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		n = n.Content[0]
	}
	if n.Kind != yaml.MappingNode {
		return nil
	}
	return n
}

func mapGet(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func hasScalarKey(m *yaml.Node, key string) bool {
	v := mapGet(m, key)
	return v != nil && v.Kind == yaml.ScalarNode && v.Value != ""
}

func isCFN(m *yaml.Node) bool {
	if mapGet(m, "AWSTemplateFormatVersion") != nil {
		return true
	}
	res := mapGet(m, "Resources")
	if res == nil || res.Kind != yaml.MappingNode {
		return false
	}
	for i := 1; i < len(res.Content); i += 2 {
		r := res.Content[i]
		if r.Kind != yaml.MappingNode {
			continue
		}
		if t := mapGet(r, "Type"); t != nil && t.Kind == yaml.ScalarNode && strings.HasPrefix(t.Value, "AWS::") {
			return true
		}
	}
	return false
}
