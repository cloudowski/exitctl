package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/cloudowski/exitctl/internal/catalog"
	"github.com/cloudowski/exitctl/internal/config"
	"github.com/cloudowski/exitctl/internal/engine"
	"github.com/cloudowski/exitctl/internal/model"
	"github.com/cloudowski/exitctl/internal/render/terminal"
	"github.com/cloudowski/exitctl/internal/result"
	"github.com/cloudowski/exitctl/internal/scanner"
	"github.com/cloudowski/exitctl/internal/source"
)

type scanFlags struct {
	all      bool
	wide     bool
	output   string
	repoName string
	ignores  []string
	values   []string
	asOf     string
	noStore  bool
	format   string
}

var datePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

func init() { register(newScanCmd) }

func newScanCmd(app *App) *cobra.Command {
	var f scanFlags
	cmd := &cobra.Command{
		Use:   "scan [path...]",
		Short: "Scan directories offline and summarize vendor lock-in",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				args = []string{"."}
			}
			return app.runScan(cmd.Context(), args, f)
		},
	}
	fl := cmd.Flags()
	fl.BoolVar(&f.all, "all", false, "show all anchors instead of the top 5")
	fl.BoolVar(&f.wide, "wide", false, "show full addresses and all alternatives regardless of terminal width; prefix addresses shared by several anchors with their location")
	fl.StringVar(&f.output, "output", "", "also write the result JSON to this file")
	fl.StringVar(&f.repoName, "repo-name", "", "repository identity (single path only)")
	fl.StringArrayVar(&f.ignores, "ignore", nil, "extra ignore glob (repeatable)")
	fl.StringArrayVar(&f.values, "values", nil, "Helm values file as <chart-dir>=<file> (repeatable)")
	fl.StringVar(&f.asOf, "as-of", "", "reference date YYYY-MM-DD for exception expiry (default today)")
	fl.BoolVar(&f.noStore, "no-store", false, "do not write .exitctl/last-result.json")
	fl.StringVar(&f.format, "format", "text", "output format: text or json")
	return cmd
}

func (app *App) runScan(ctx context.Context, paths []string, f scanFlags) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if f.format != "text" && f.format != "json" {
		return Errorf(ExitInvalidInput, "unknown --format %q (use text or json)", f.format)
	}
	if f.repoName != "" && len(paths) > 1 {
		return Errorf(ExitInvalidInput, "--repo-name can only be used with a single path")
	}
	asOf := f.asOf
	if asOf == "" {
		asOf = time.Now().Format("2006-01-02")
	} else if !datePattern.MatchString(asOf) {
		return Errorf(ExitInvalidInput, "--as-of must be YYYY-MM-DD")
	}
	ix, err := app.loadIndex()
	if err != nil {
		return err
	}
	cwd, _ := os.Getwd()

	var facts model.Facts
	var inputs []engine.Input
	var cfg config.Config
	for i, p := range paths {
		root, err := filepath.Abs(p)
		if err != nil {
			return Errorf(ExitInvalidInput, "invalid path %s", p)
		}
		if st, err := os.Stat(root); err != nil || !st.IsDir() {
			return Errorf(ExitInvalidInput, "%s is not a directory", p)
		}
		c, err := config.Discover(root, cwd, app.Opts.ConfigPath)
		if err != nil {
			return Errorf(ExitInvalidInput, "%v", err)
		}
		if i == 0 {
			cfg = c // scoring and exceptions come from the first path's config
		}
		repoName := f.repoName
		if repoName == "" {
			repoName = c.RepoName
		}
		repo := model.DetectRepoName(root, repoName)
		hv, err := helmValues(f.values, c)
		if err != nil {
			return err
		}
		ignores := append(append([]string{}, c.Ignore...), f.ignores...)
		rf, err := scanner.Collect(ctx, ignores, source.Options{Repo: repo, Root: root, HelmValues: hv, Catalog: ix})
		if err != nil {
			return Errorf(ExitInvalidInput, "cannot read %s", p)
		}
		if app.Opts.Verbose {
			for _, s := range rf.Sources {
				if s.Status != model.StatusParsed {
					_, _ = fmt.Fprintf(app.Err, "%s: %s %s (%s)\n", s.Status, s.Kind, s.Path, s.Note)
				}
			}
		}
		facts.Append(rf)
		inputs = append(inputs, engine.Input{Repo: repo, AsOf: asOf})
	}
	if len(facts.RawNodes) == 0 && len(facts.CodeUses) == 0 {
		_, _ = fmt.Fprintln(app.Out, "Offline, read-only scan: no supported sources found.")
		_, _ = fmt.Fprintln(app.Out, "Supported: Terraform/OpenTofu, CloudFormation, Kubernetes manifests, Helm charts,")
		_, _ = fmt.Fprintln(app.Out, "Kustomize, Dockerfiles, GitHub Actions, GitLab CI, and dependency manifests for")
		_, _ = fmt.Fprintln(app.Out, "JavaScript/TypeScript, Python, Go, Java and C#.")
		return &ExitError{Code: ExitNothing, Msg: "nothing to scan"}
	}

	an := engine.Analyze(facts, ix, cfg, inputs)
	return app.emitResult(an.Doc, ix, f)
}

func (app *App) emitResult(doc *result.Document, ix *catalog.Index, f scanFlags) error {
	for _, n := range terminal.ExceptionNotices(doc) {
		_, _ = fmt.Fprintln(app.Err, n)
	}
	if !f.noStore {
		if err := result.Save(doc, app.Opts.InputPath); err != nil {
			return Errorf(ExitInternal, "cannot write %s: %v", app.Opts.InputPath, err)
		}
	}
	if f.output != "" {
		if err := result.Save(doc, f.output); err != nil {
			return Errorf(ExitInvalidInput, "cannot write %s: %v", f.output, err)
		}
	}
	if f.format == "json" {
		if err := result.Encode(app.Out, doc); err != nil {
			return err
		}
	} else if err := terminal.Summary(app.Out, doc, app.scanTermOptions(ix, f)); err != nil {
		return err
	}
	if doc.Analysis.Status != "ok" {
		return &ExitError{Code: ExitNothing, Msg: "nothing scored"}
	}
	return nil
}

// helmValues merges --values chart=file flags with config helm.values_files.
func helmValues(flags []string, c config.Config) (map[string][]string, error) {
	out := map[string][]string{}
	for chart, files := range c.Helm.ValuesFiles {
		out[model.NormalizeDir(chart)] = append(out[model.NormalizeDir(chart)], files...)
	}
	for _, v := range flags {
		chart, file, ok := strings.Cut(v, "=")
		if !ok || chart == "" || file == "" {
			return nil, Errorf(ExitInvalidInput, "--values must be <chart-dir>=<file>, got %q", v)
		}
		k := model.NormalizeDir(chart)
		out[k] = append(out[k], file)
	}
	return out, nil
}

// scanTermOptions returns the terminal options for the scan summary.
func (app *App) scanTermOptions(ix *catalog.Index, f scanFlags) terminal.Options {
	o := app.termOptions(ix, f.all)
	o.Wide = f.wide
	return o
}
