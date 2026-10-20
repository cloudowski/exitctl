package kubernetes

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
	"sigs.k8s.io/kustomize/api/krusty"
	"sigs.k8s.io/kustomize/kyaml/filesys"

	"github.com/cloudowski/exitctl/internal/model"
	"github.com/cloudowski/exitctl/internal/source"
	"github.com/cloudowski/exitctl/internal/walk"
)

// Neutral notes for Kustomize sources.
const (
	noteKustRemote  = "uses remote resources; not built"
	noteKustHelm    = "uses helmCharts; not built"
	noteKustBuild   = "kustomization could not be built"
	noteKustParse   = "kustomization could not be parsed"
	noteKustMissing = "a referenced resource is missing; not built"
	noteKustOutside = "references files outside the scan root; not built"
	noteKustOutput  = "some build output could not be parsed as YAML"
)

var kustomizationFiles = []string{"kustomization.yaml", "kustomization.yml", "Kustomization"}

type kustomizeDetector struct{}

// NewKustomize returns the detector that builds local kustomizations with
// krusty. Overlays that reference anything remote (or use helmCharts) are
// reported as gaps and never built, so no clone or exec is ever attempted.
func NewKustomize() source.Detector { return kustomizeDetector{} }

func (kustomizeDetector) Name() string { return "kustomize" }

func (kustomizeDetector) Detect(ctx context.Context, idx walk.Index, opts source.Options) (model.Facts, error) {
	var f model.Facts
	dirs := append([]string(nil), idx.Kustomizations...)
	sort.Strings(dirs)
	for _, dir := range dirs {
		if err := ctx.Err(); err != nil {
			return f, err
		}
		f.Append(buildKustomization(idx.Root, dir, opts))
	}
	// A remote reference in a shared base is found from every overlay that
	// includes it; report it once.
	seen := map[model.Gap]bool{}
	gaps := f.Gaps[:0]
	for _, g := range f.Gaps {
		if !seen[g] {
			seen[g] = true
			gaps = append(gaps, g)
		}
	}
	f.Gaps = gaps
	return f, nil
}

// kustomizationFile returns the kustomization file name in absDir, or "".
func kustomizationFile(absDir string) string {
	for _, n := range kustomizationFiles {
		if fi, err := os.Stat(filepath.Join(absDir, n)); err == nil && fi.Mode().IsRegular() {
			return n
		}
	}
	return ""
}

// isRemoteRef reports whether a kustomization reference points to a remote
// location (URL, git reference, or hosted repository path).
func isRemoteRef(r string) bool {
	return strings.Contains(r, "://") || strings.HasPrefix(r, "github.com/") ||
		strings.HasPrefix(r, "git::") || strings.HasPrefix(r, "git@") || strings.Contains(r, "?ref=")
}

// looksHosted reports whether a missing reference looks like a
// "host.tld/org/repo" path that kustomize would try to clone.
func looksHosted(r string) bool {
	first, _, ok := strings.Cut(r, "/")
	return ok && strings.Contains(first, ".") && first != "." && first != ".."
}

// prescan describes why an overlay must not be built.
type prescan struct {
	gaps []model.Gap
	note string // non-empty: do not build
}

// scanKustomization checks a kustomization tree for remote references,
// helmCharts, missing references and references outside the root, without
// building it. visited guards against cycles.
func scanKustomization(root, absDir string, opts source.Options, visited map[string]bool, res *prescan) {
	if visited[absDir] {
		return
	}
	visited[absDir] = true
	name := kustomizationFile(absDir)
	if name == "" {
		return
	}
	relFile := relPath(root, filepath.Join(absDir, name))
	data, err := os.ReadFile(filepath.Join(absDir, name))
	if err != nil {
		setNote(res, noteKustParse)
		return
	}
	var k map[string]any
	if err := yaml.Unmarshal(data, &k); err != nil {
		setNote(res, noteKustParse)
		return
	}
	for _, key := range []string{"helmCharts", "helmChartInflationGenerator", "helmGlobals"} {
		if v, ok := k[key]; ok && v != nil {
			res.gaps = append(res.gaps, model.Gap{Kind: model.GapRemoteKustomize, Subject: "helmCharts", Repo: opts.Repo, Path: relFile, Count: 1})
			setNote(res, noteKustHelm)
			break
		}
	}
	for _, key := range []string{"resources", "bases", "components", "generators", "transformers", "validators", "crds"} {
		list, _ := k[key].([]any)
		for _, it := range list {
			r, ok := it.(string)
			if !ok {
				continue // inline plugin configuration
			}
			r = strings.TrimSpace(r)
			if r == "" {
				continue
			}
			if isRemoteRef(r) {
				res.gaps = append(res.gaps, model.Gap{Kind: model.GapRemoteKustomize, Subject: r, Repo: opts.Repo, Path: relFile, Count: 1})
				setNote(res, noteKustRemote)
				continue
			}
			p := filepath.Clean(filepath.Join(absDir, filepath.FromSlash(r)))
			if !within(root, p) {
				setNote(res, noteKustOutside)
				continue
			}
			fi, err := os.Stat(p)
			if err != nil {
				if looksHosted(r) {
					res.gaps = append(res.gaps, model.Gap{Kind: model.GapRemoteKustomize, Subject: r, Repo: opts.Repo, Path: relFile, Count: 1})
					setNote(res, noteKustRemote)
				} else {
					setNote(res, noteKustMissing)
				}
				continue
			}
			if fi.IsDir() {
				scanKustomization(root, p, opts, visited, res)
			}
		}
	}
}

func setNote(res *prescan, note string) {
	if res.note == "" {
		res.note = note
	}
}

func within(root, p string) bool {
	r, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator))
}

func relPath(root, p string) string {
	r, err := filepath.Rel(root, p)
	if err != nil {
		return filepath.ToSlash(p)
	}
	return filepath.ToSlash(r)
}

// runKrusty builds the kustomization in absDir with builtin plugins only and
// returns the output as multi-document YAML. It is a variable so tests can
// assert it is never reached for remote overlays.
var runKrusty = func(absDir string) ([]byte, error) {
	// krusty prints deprecation warnings ("'commonLabels' is deprecated...")
	// straight to os.Stderr with no option to disable them. They are about
	// the user's manifests, not the scan, so silence them for the build.
	if devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0); err == nil {
		orig := os.Stderr
		os.Stderr = devnull
		defer func() {
			os.Stderr = orig
			_ = devnull.Close()
		}()
	}
	k := krusty.MakeKustomizer(krusty.MakeDefaultOptions())
	resMap, err := k.Run(filesys.MakeFsOnDisk(), absDir)
	if err != nil {
		return nil, err
	}
	return resMap.AsYaml()
}

func buildKustomization(root, dir string, opts source.Options) (f model.Facts) {
	absDir := filepath.Join(root, filepath.FromSlash(model.NormalizeDir(dir)))
	name := kustomizationFile(absDir)
	if name == "" {
		return f
	}
	relFile := chartRel(dir, name)
	defer func() {
		if r := recover(); r != nil {
			f = model.Facts{Sources: []model.Source{source.FailedSource(opts.Repo, relFile, model.KindKustomize, noteKustBuild)}}
		}
	}()

	var pre prescan
	scanKustomization(filepath.Clean(root), filepath.Clean(absDir), opts, map[string]bool{}, &pre)
	f.Gaps = append(f.Gaps, pre.gaps...)
	if pre.note != "" {
		status := model.StatusUnresolved
		if pre.note == noteKustParse {
			status = model.StatusFailed
		}
		f.Sources = append(f.Sources, model.Source{Repo: opts.Repo, Path: relFile, Kind: model.KindKustomize, Status: status, Note: pre.note})
		return f
	}

	out, err := runKrusty(absDir)
	if err != nil {
		f.Sources = append(f.Sources, source.FailedSource(opts.Repo, relFile, model.KindKustomize, noteKustBuild))
		return f
	}
	src := model.Source{Repo: opts.Repo, Path: relFile, Kind: model.KindKustomize, Status: model.StatusParsed}
	objs, perr := parseObjects(out)
	if perr != nil {
		src.Status, src.Note = model.StatusPartial, noteKustOutput
	}
	f.Sources = append(f.Sources, src)
	kdata, _ := os.ReadFile(filepath.Join(absDir, name))
	ev := firstLineEvidence(opts.Repo, relFile, model.KindKustomize, kdata)
	all := make([]located, 0, len(objs))
	for _, o := range objs {
		all = append(all, located{obj: o, ev: func(int) model.Evidence { return ev }})
	}
	f.RawNodes, f.RawEdges = buildFacts(opts.Repo, model.KindKustomize, all)
	return f
}
