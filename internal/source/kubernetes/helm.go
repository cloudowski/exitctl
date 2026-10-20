package kubernetes

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"helm.sh/helm/v4/pkg/chart/common"
	"helm.sh/helm/v4/pkg/chart/common/util"
	chartv2 "helm.sh/helm/v4/pkg/chart/v2"
	"helm.sh/helm/v4/pkg/chart/v2/loader"
	chartutil "helm.sh/helm/v4/pkg/chart/v2/util"
	"helm.sh/helm/v4/pkg/engine"

	"github.com/cloudowski/exitctl/internal/model"
	"github.com/cloudowski/exitctl/internal/redact"
	"github.com/cloudowski/exitctl/internal/source"
	"github.com/cloudowski/exitctl/internal/walk"
)

// Neutral notes for Helm sources.
const (
	noteChartLoad      = "chart could not be loaded"
	noteChartRender    = "chart could not be rendered"
	noteChartValues    = "a values file could not be read"
	noteChartDeps      = "some chart dependencies are not available locally"
	noteChartTemplates = "some rendered templates could not be parsed as YAML"
	noteChartRequired  = "rendered without some required values"
)

// Release settings used for offline rendering.
const (
	helmReleaseName = "exitctl"
	helmNamespace   = "default"
)

type helmDetector struct{}

// NewHelm returns the detector that renders local Helm charts offline (chart
// loader and template engine only: no install, no repository or OCI access,
// no dependency update, no cluster lookups).
func NewHelm() source.Detector { return helmDetector{} }

func (helmDetector) Name() string { return "helm" }

func (helmDetector) Detect(ctx context.Context, idx walk.Index, opts source.Options) (model.Facts, error) {
	var f model.Facts
	charts := append([]string(nil), idx.HelmCharts...)
	sort.Strings(charts)
	for _, dir := range charts {
		if err := ctx.Err(); err != nil {
			return f, err
		}
		f.Append(renderChart(ctx, idx.Root, dir, opts))
	}
	return f, nil
}

// chartRel joins a repo-relative chart dir ("." for the root) and a name.
func chartRel(dir, name string) string {
	if model.NormalizeDir(dir) == "" {
		return name
	}
	return path.Join(model.NormalizeDir(dir), name)
}

// valuesFiles returns the extra values files configured for a chart dir.
func valuesFiles(opts source.Options, dir string) []string {
	want := model.NormalizeDir(dir)
	keys := make([]string, 0, len(opts.HelmValues))
	for k := range opts.HelmValues {
		if model.NormalizeDir(k) == want {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var out []string
	for _, k := range keys {
		out = append(out, opts.HelmValues[k]...)
	}
	return out
}

func renderChart(ctx context.Context, root, dir string, opts source.Options) (f model.Facts) {
	chartFile := chartRel(dir, "Chart.yaml")
	src := model.Source{Repo: opts.Repo, Path: chartFile, Kind: model.KindHelm, Status: model.StatusParsed}
	defer func() {
		if r := recover(); r != nil {
			f = model.Facts{Sources: []model.Source{source.FailedSource(opts.Repo, chartFile, model.KindHelm, noteChartRender)}}
		}
	}()
	var notes []string
	partial := func(note string) {
		src.Status = model.StatusPartial
		notes = append(notes, note)
	}

	absDir := filepath.Join(root, filepath.FromSlash(model.NormalizeDir(dir)))
	ch, err := loader.Load(absDir)
	if err != nil || ch == nil || ch.Metadata == nil {
		f.Sources = append(f.Sources, source.FailedSource(opts.Repo, chartFile, model.KindHelm, noteChartLoad))
		return f
	}

	// Dependencies declared in Chart.yaml but not vendored under charts/.
	present := map[string]bool{}
	for _, d := range ch.Dependencies() {
		present[d.Name()] = true
	}
	for _, d := range ch.Metadata.Dependencies {
		if d == nil || present[d.Name] {
			continue
		}
		subject := d.Name
		if d.Repository != "" {
			subject += " (" + d.Repository + ")"
		}
		f.Gaps = append(f.Gaps, model.Gap{Kind: model.GapUnresolvedChart, Subject: subject, Repo: opts.Repo, Path: chartFile, Count: 1})
	}
	if len(f.Gaps) > 0 {
		partial(noteChartDeps)
	}

	userVals := map[string]any{}
	for _, vf := range valuesFiles(opts, dir) {
		p := vf
		if !filepath.IsAbs(p) {
			p = filepath.Join(root, filepath.FromSlash(vf))
		}
		data, err := os.ReadFile(p)
		if err != nil {
			partial(noteChartValues)
			continue
		}
		v, err := common.ReadValues(data)
		if err != nil {
			partial(noteChartValues)
			continue
		}
		userVals = loader.MergeMaps(userVals, v)
	}

	if err := chartutil.ProcessDependencies(ch, userVals); err != nil {
		partial(noteChartRender)
	}
	relOpts := common.ReleaseOptions{Name: helmReleaseName, Namespace: helmNamespace, Revision: 1, IsInstall: true}
	// Schema validation is skipped: it may resolve remote $refs.
	top, err := util.ToRenderValuesWithSchemaValidation(ch, userVals, relOpts, nil, true)
	if err != nil {
		f.Sources = append(f.Sources, source.FailedSource(opts.Repo, chartFile, model.KindHelm, noteChartRender))
		return f
	}
	rendered, err := engine.Engine{}.RenderWithContext(ctx, ch, top)
	if err != nil {
		// Retry tolerating `required` and `fail` (charts that need values
		// the scan does not have).
		rendered, err = engine.Engine{LintMode: true}.RenderWithContext(ctx, ch, top)
		if err == nil {
			partial(noteChartRequired)
		}
	}
	if err != nil {
		f.Sources = append(f.Sources, source.FailedSource(opts.Repo, chartFile, model.KindHelm, noteChartRender))
		return f
	}

	names := make([]string, 0, len(rendered))
	for name := range rendered {
		names = append(names, name)
	}
	sort.Strings(names)
	var all []located
	badTemplate := false
	for _, name := range names {
		base := path.Base(name)
		if base == "NOTES.txt" || strings.HasPrefix(base, "_") || strings.TrimSpace(rendered[name]) == "" {
			continue
		}
		objs, perr := parseObjects([]byte(rendered[name]))
		if perr != nil {
			badTemplate = true
		}
		tplPath := templatePath(ch, dir, name)
		tplData, rerr := os.ReadFile(filepath.Join(root, filepath.FromSlash(tplPath)))
		if rerr != nil {
			tplPath = chartFile
			tplData, _ = os.ReadFile(filepath.Join(root, filepath.FromSlash(chartFile)))
		}
		lineFor := templateLines(tplData)
		evAt := lineEvidence(opts.Repo, tplPath, model.KindHelm, tplData)
		for _, o := range objs {
			ev := evAt(lineFor(o.kind))
			all = append(all, located{obj: o, ev: func(int) model.Evidence { return ev }})
		}
	}
	if badTemplate {
		partial(noteChartTemplates)
	}
	if len(notes) > 0 {
		src.Note = strings.Join(dedupe(notes), "; ")
	}
	f.Sources = append(f.Sources, src)
	f.RawNodes, f.RawEdges = buildFacts(opts.Repo, model.KindHelm, all)
	return f
}

// templatePath maps a rendered template name ("<chart>/templates/x.yaml" or
// "<chart>/charts/<sub>/templates/x.yaml") to a repo-relative file path.
func templatePath(ch *chartv2.Chart, dir, name string) string {
	rest := name
	if prefix := ch.Name() + "/"; strings.HasPrefix(rest, prefix) {
		rest = strings.TrimPrefix(rest, prefix)
	} else if i := strings.Index(rest, "/"); i >= 0 {
		rest = rest[i+1:]
	}
	return chartRel(dir, rest)
}

// templateLines returns a function mapping a rendered object's kind to the
// line of a matching `kind:` literal in the template source. Repeated kinds
// use successive matches; anything that cannot be mapped is line 1.
func templateLines(tpl []byte) func(kind string) int {
	lines := strings.Split(string(tpl), "\n")
	used := map[string]int{}
	return func(kind string) int {
		re, err := regexp.Compile(fmt.Sprintf(`^\s*kind:\s*["']?%s["']?\s*(#.*)?$`, regexp.QuoteMeta(kind)))
		if err != nil {
			return 1
		}
		var hits []int
		for i, l := range lines {
			if re.MatchString(strings.TrimRight(l, "\r")) {
				hits = append(hits, i+1)
			}
		}
		if len(hits) == 0 {
			return 1
		}
		n := used[kind]
		used[kind]++
		if n >= len(hits) {
			n = len(hits) - 1
		}
		return hits[n]
	}
}

func dedupe(s []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, x := range s {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// firstLineEvidence returns evidence for line 1 of a file.
func firstLineEvidence(repo, rel string, kind model.SourceKind, data []byte) model.Evidence {
	line := string(data)
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	return model.NewEvidence(repo, rel, 1, redact.Line(line), kind)
}
