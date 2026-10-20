// Command calibrate scores the calibration corpus (FR-041, SC-004, SC-005,
// research R21) and writes a Markdown deviation report.
//
// It is a developer-only tool: it is not part of the exitctl binary and, unlike
// everything under internal/, it uses the network (git fetch). For each corpus
// entry it shallow-fetches the pinned commit into a cache directory, runs the
// scan pipeline in-process with the built-in catalog and default scoring, and
// compares the grade with the maintainer's expected band.
//
// Usage:
//
//	go run ./tools/calibrate [-corpus calibration/corpus.yaml] [-out calibration-report.md]
//	                         [-cache DIR] [-include-drafts] [-only SUBSTRING]
//
// -include-drafts classifies with draft catalog entries as if they were
// verified (the same as `exitctl --include-drafts`). It is off by default so
// the gate measures what users get, but while the built-in catalog is still
// mostly drafts it is needed to get meaningful grades.
//
// -only keeps the corpus entries whose URL contains the substring (or any of
// several comma-separated substrings); the gate is then evaluated on that
// subset only.
//
// The exit code is 0 when the release gate passes, 1 when it fails (fewer
// than 90% of repos within band, any repo more than one grade outside its
// band, or median weighted coverage below 90%), and 2 on usage or setup
// errors. A repo that cannot be fetched or receives no grade counts as not
// within band.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	catalogdata "github.com/cloudowski/exitctl/catalog"
	"github.com/cloudowski/exitctl/internal/catalog"
	"github.com/cloudowski/exitctl/internal/config"
	"github.com/cloudowski/exitctl/internal/engine"
	"github.com/cloudowski/exitctl/internal/model"
	"github.com/cloudowski/exitctl/internal/scanner"
	"github.com/cloudowski/exitctl/internal/source"
)

// Gate thresholds (SC-004, SC-005).
const (
	minWithinBand   = 0.90
	maxDeviation    = 1
	minMedianCovPct = 0.90
)

// grades is the grade scale in order (data model grade_bands).
var grades = []string{"A", "B", "C", "D", "E"}

// Corpus is calibration/corpus.yaml.
type Corpus struct {
	Version int    `yaml:"version"`
	Repos   []Repo `yaml:"repos"`
}

// Repo is one corpus entry.
type Repo struct {
	URL                string   `yaml:"url"`
	Commit             string   `yaml:"commit"`
	ExpectedBand       string   `yaml:"expected_band"`
	ExpectedBandStatus string   `yaml:"expected_band_status"`
	Clouds             []string `yaml:"clouds"`
	Notes              string   `yaml:"notes"`
}

// Band is an inclusive range of grade positions in grades.
type Band struct{ Lo, Hi int }

func (b Band) String() string {
	if b.Lo == b.Hi {
		return grades[b.Lo]
	}
	return grades[b.Lo] + "-" + grades[b.Hi]
}

// Row is the calibration outcome of one repo.
type Row struct {
	Repo       Repo
	Band       Band
	Index      *int
	Grade      string
	Confidence string
	Coverage   float64
	Scanned    bool   // the pipeline ran (Coverage is meaningful)
	Err        string // fetch or scan failure
	Deviation  int    // grades outside the band; valid when HasDev
	HasDev     bool
}

// Summary aggregates rows for the gate.
type Summary struct {
	Total          int
	Within         int
	WithinPct      float64
	MaxDeviation   int
	Ungraded       int // no grade or error
	MedianCoverage float64
	Unconfirmed    int
}

var shaPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("calibrate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	defCache := filepath.Join(os.TempDir(), "exitctl-calibrate")
	if d, err := os.UserCacheDir(); err == nil {
		defCache = filepath.Join(d, "exitctl-calibrate")
	}
	corpusPath := fs.String("corpus", "calibration/corpus.yaml", "corpus file")
	cacheDir := fs.String("cache", defCache, "directory for fetched repositories")
	out := fs.String("out", "calibration-report.md", "Markdown report to write")
	fs.StringVar(out, "report", "calibration-report.md", "alias for -out")
	drafts := fs.Bool("include-drafts", false, "classify with draft catalog entries as if verified (needed while the built-in catalog is mostly drafts)")
	only := fs.String("only", "", "only corpus entries whose URL contains this substring (comma-separated: any of them)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	corpus, err := loadCorpus(*corpusPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "calibrate:", err)
		return 2
	}
	var repos []Repo
	for _, r := range corpus.Repos {
		if selected(r.URL, *only) {
			repos = append(repos, r)
		}
	}
	if len(repos) == 0 {
		_, _ = fmt.Fprintln(stderr, "calibrate: no corpus entries selected")
		return 2
	}

	ix, err := builtinIndex(*drafts)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "calibrate:", err)
		return 2
	}
	cfg := config.Defaults()
	asOf := time.Now().Format("2006-01-02")
	ctx := context.Background()

	rows := make([]Row, 0, len(repos))
	for i, r := range repos {
		_, _ = fmt.Fprintf(stderr, "[%d/%d] %s@%s\n", i+1, len(repos), r.URL, short(r.Commit))
		band, _ := ParseBand(r.ExpectedBand) // validated in loadCorpus
		row := Row{Repo: r, Band: band}
		dir := filepath.Join(*cacheDir, cacheName(r.URL))
		if err := fetch(ctx, dir, r.URL, r.Commit, stderr); err != nil {
			row.Err = "fetch failed: " + err.Error()
			rows = append(rows, row)
			continue
		}
		scanRepo(ctx, &row, dir, ix, cfg, asOf)
		rows = append(rows, row)
	}

	sum := Summarize(rows)
	failures := Gate(sum)
	var buf bytes.Buffer
	writeReport(&buf, reportMeta{
		Corpus: *corpusPath, Only: *only, Drafts: *drafts, Date: asOf,
		Versions: engine.Versions(ix, cfg).Catalog,
	}, rows, sum, failures)
	if err := os.WriteFile(*out, buf.Bytes(), 0o644); err != nil {
		_, _ = fmt.Fprintln(stderr, "calibrate:", err)
		return 2
	}
	_, _ = fmt.Fprintf(stdout, "%d/%d within band (%.0f%%), max deviation %d, median weighted coverage %s; report: %s\n",
		sum.Within, sum.Total, sum.WithinPct*100, sum.MaxDeviation, pct(sum.MedianCoverage), *out)
	if len(failures) > 0 {
		for _, f := range failures {
			_, _ = fmt.Fprintln(stdout, "GATE FAILED:", f)
		}
		return 1
	}
	_, _ = fmt.Fprintln(stdout, "gate passed")
	return 0
}

// selected reports whether url matches the -only filter: empty matches all,
// otherwise any of the comma-separated substrings.
func selected(url, only string) bool {
	if only == "" {
		return true
	}
	for _, s := range strings.Split(only, ",") {
		if s = strings.TrimSpace(s); s != "" && strings.Contains(url, s) {
			return true
		}
	}
	return false
}

func loadCorpus(path string) (*Corpus, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Corpus
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	if c.Version != 1 {
		return nil, fmt.Errorf("%s: unsupported version %d (want 1)", path, c.Version)
	}
	for i, r := range c.Repos {
		if r.URL == "" {
			return nil, fmt.Errorf("%s: repos[%d]: missing url", path, i)
		}
		if !shaPattern.MatchString(r.Commit) {
			return nil, fmt.Errorf("%s: %s: commit must be a 40-character lowercase SHA", path, r.URL)
		}
		if _, err := ParseBand(r.ExpectedBand); err != nil {
			return nil, fmt.Errorf("%s: %s: %v", path, r.URL, err)
		}
	}
	return &c, nil
}

func builtinIndex(drafts bool) (*catalog.Index, error) {
	cat, probs, err := catalog.LoadAndValidate(catalogdata.FS)
	if err != nil {
		return nil, fmt.Errorf("built-in catalog: %v", err)
	}
	if len(probs) > 0 {
		return nil, fmt.Errorf("built-in catalog is invalid (%d problems); run 'exitctl catalog validate'", len(probs))
	}
	return catalog.NewIndex(cat, catalog.IndexOptions{IncludeDrafts: drafts}), nil
}

// scanRepo runs the same pipeline as `exitctl scan <dir>` with default
// configuration (a repo's own .exitctl.yaml is ignored on purpose).
func scanRepo(ctx context.Context, row *Row, dir string, ix *catalog.Index, cfg config.Config, asOf string) {
	repo := model.DetectRepoName(dir, "")
	facts, err := scanner.Collect(ctx, cfg.Ignore, source.Options{Repo: repo, Root: dir, Catalog: ix})
	if err != nil {
		row.Err = "scan failed: " + err.Error()
		return
	}
	an := engine.Analyze(facts, ix, cfg, []engine.Input{{Repo: repo, AsOf: asOf}})
	a := an.Doc.Analysis
	row.Scanned = true
	row.Index = a.Index
	row.Grade = a.Grade
	row.Confidence = a.Confidence
	row.Coverage = a.Coverage.Weighted
	if a.Status != "ok" && row.Confidence == "" {
		row.Confidence = a.Status
	}
	if g := gradePos(row.Grade); g >= 0 {
		row.Deviation = Deviation(g, row.Band)
		row.HasDev = true
	}
}

// fetch makes dir a shallow checkout of commit from url, reusing it when
// HEAD is already at commit.
func fetch(ctx context.Context, dir, url, commit string, log io.Writer) error {
	if head, err := git(ctx, dir, "rev-parse", "HEAD"); err == nil && strings.TrimSpace(head) == commit {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		if _, err := git(ctx, dir, "init", "-q"); err != nil {
			return err
		}
	}
	if _, err := git(ctx, dir, "remote", "get-url", "origin"); err != nil {
		if _, err := git(ctx, dir, "remote", "add", "origin", url); err != nil {
			return err
		}
	} else if _, err := git(ctx, dir, "remote", "set-url", "origin", url); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(log, "  fetching %s\n", short(commit))
	if _, err := git(ctx, dir, "fetch", "-q", "--depth", "1", "origin", commit); err != nil {
		return err
	}
	if _, err := git(ctx, dir, "-c", "advice.detachedHead=false", "checkout", "-q", "--force", "FETCH_HEAD"); err != nil {
		return err
	}
	_, err := git(ctx, dir, "clean", "-q", "-fdx")
	return err
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New("git " + args[0] + ": " + msg)
	}
	return out.String(), nil
}

// cacheName turns a clone URL into a directory name.
func cacheName(url string) string {
	s := strings.TrimSuffix(url, ".git")
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, s)
}

// ParseBand parses "X-Y" or "X" over grades A, B, C, D, E.
func ParseBand(s string) (Band, error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	lo, hi, found := strings.Cut(s, "-")
	if !found {
		hi = lo
	}
	l, h := gradePos(strings.TrimSpace(lo)), gradePos(strings.TrimSpace(hi))
	if l < 0 || h < 0 {
		return Band{}, fmt.Errorf("invalid expected_band %q (want X or X-Y over A,B,C,D,E)", s)
	}
	if l > h {
		return Band{}, fmt.Errorf("invalid expected_band %q (lower grade first)", s)
	}
	return Band{Lo: l, Hi: h}, nil
}

func gradePos(g string) int {
	for i, x := range grades {
		if x == g {
			return i
		}
	}
	return -1
}

// Deviation returns how many grade steps g lies outside b (0 = within).
func Deviation(g int, b Band) int {
	switch {
	case g < b.Lo:
		return b.Lo - g
	case g > b.Hi:
		return g - b.Hi
	}
	return 0
}

// Median returns the median of xs (0 for none).
func Median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

// Summarize aggregates rows. Coverage counts every repo the pipeline ran on.
func Summarize(rows []Row) Summary {
	s := Summary{Total: len(rows)}
	var cov []float64
	for _, r := range rows {
		if r.Repo.ExpectedBandStatus != "confirmed" {
			s.Unconfirmed++
		}
		if r.Scanned {
			cov = append(cov, r.Coverage)
		}
		if !r.HasDev {
			s.Ungraded++
			continue
		}
		if r.Deviation == 0 {
			s.Within++
		}
		s.MaxDeviation = max(s.MaxDeviation, r.Deviation)
	}
	if s.Total > 0 {
		s.WithinPct = float64(s.Within) / float64(s.Total)
	}
	s.MedianCoverage = Median(cov)
	return s
}

// Gate returns the release-gate failures (empty = pass).
func Gate(s Summary) []string {
	var f []string
	if s.Total == 0 {
		return []string{"no repositories"}
	}
	if s.WithinPct < minWithinBand {
		f = append(f, fmt.Sprintf("%d of %d repos (%.0f%%) within expected band, need at least %.0f%% (SC-004)",
			s.Within, s.Total, s.WithinPct*100, minWithinBand*100))
	}
	if s.MaxDeviation > maxDeviation {
		f = append(f, fmt.Sprintf("a repo is %d grades outside its expected band, at most %d allowed (SC-004)",
			s.MaxDeviation, maxDeviation))
	}
	if s.MedianCoverage < minMedianCovPct {
		f = append(f, fmt.Sprintf("median weighted coverage %s, need at least %.0f%% (SC-005)",
			pct(s.MedianCoverage), minMedianCovPct*100))
	}
	return f
}

// Status is the per-repo status label.
func (r Row) Status() string {
	switch {
	case r.Err != "":
		return "error"
	case !r.HasDev:
		return "no grade"
	case r.Deviation == 0:
		return "within band"
	case r.Deviation == 1:
		return "off by 1"
	}
	return fmt.Sprintf("off by %d (gate)", r.Deviation)
}

type reportMeta struct {
	Corpus, Only, Date, Versions string
	Drafts                       bool
}

func writeReport(w io.Writer, m reportMeta, rows []Row, s Summary, failures []string) {
	p := func(format string, a ...any) { _, _ = fmt.Fprintf(w, format+"\n", a...) }
	p("# exitctl calibration report")
	p("")
	p("- Date: %s", m.Date)
	filter, drafts := "", "excluded"
	if m.Only != "" {
		filter = fmt.Sprintf(" (filtered: `-only %s`)", m.Only)
	}
	if m.Drafts {
		drafts = "included (`-include-drafts`)"
	}
	p("- Corpus: `%s`%s", m.Corpus, filter)
	p("- Catalog: built-in %s, drafts %s", m.Versions, drafts)
	p("- Scoring: defaults")
	p("")
	p("## Repositories")
	p("")
	p("| Repository | Commit | Index | Grade | Confidence | Weighted coverage | Expected band | Deviation | Status |")
	p("|---|---|---:|:---:|---|---:|:---:|---:|---|")
	for _, r := range rows {
		idx, grade, conf, cov, dev := "-", "-", "-", "-", "-"
		if r.Index != nil {
			idx = fmt.Sprint(*r.Index)
		}
		if r.Grade != "" {
			grade = r.Grade
		}
		if r.Confidence != "" {
			conf = r.Confidence
		}
		if r.Scanned {
			cov = pct(r.Coverage)
		}
		if r.HasDev {
			dev = fmt.Sprint(r.Deviation)
		}
		band := r.Band.String()
		if r.Repo.ExpectedBandStatus != "confirmed" {
			band += " (unconfirmed)"
		}
		p("| %s | `%s` | %s | %s | %s | %s | %s | %s | %s |",
			strings.TrimPrefix(strings.TrimSuffix(r.Repo.URL, ".git"), "https://"), short(r.Repo.Commit),
			idx, grade, conf, cov, band, dev, r.Status())
	}
	var errs []Row
	for _, r := range rows {
		if r.Err != "" {
			errs = append(errs, r)
		}
	}
	if len(errs) > 0 {
		p("")
		p("### Errors")
		p("")
		for _, r := range errs {
			p("- %s: %s", r.Repo.URL, r.Err)
		}
	}
	p("")
	p("## Summary")
	p("")
	p("- Repositories: %d", s.Total)
	p("- Within expected band: %d (%.0f%%, gate >= %.0f%%)", s.Within, s.WithinPct*100, minWithinBand*100)
	p("- Max deviation: %d grade(s) (gate <= %d)", s.MaxDeviation, maxDeviation)
	p("- Without a grade (error or withheld): %d", s.Ungraded)
	p("- Median weighted coverage: %s (gate >= %.0f%%)", pct(s.MedianCoverage), minMedianCovPct*100)
	if s.Unconfirmed > 0 {
		p("- Unconfirmed expected bands: %d (the gate is only meaningful once a maintainer confirms every band)", s.Unconfirmed)
	}
	p("")
	if len(failures) == 0 {
		p("**Gate: PASSED** (SC-004, SC-005)")
		return
	}
	p("**Gate: FAILED** (SC-004, SC-005)")
	p("")
	for _, f := range failures {
		p("- %s", f)
	}
}

// pct formats a 0..1 fraction like the product renderers (floor).
func pct(f float64) string {
	v := int(f*100 + 1e-9)
	return fmt.Sprintf("%d%%", max(0, min(100, v)))
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
