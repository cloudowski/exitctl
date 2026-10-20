package source

import (
	"context"
	"runtime"
	"sync"

	"github.com/cloudowski/exitctl/internal/catalog"
	"github.com/cloudowski/exitctl/internal/model"
	"github.com/cloudowski/exitctl/internal/walk"
)

// Options are shared by all detectors for one repository.
type Options struct {
	Repo string // repository identity used in IDs and evidence
	Root string // absolute path of the scanned root
	// HelmValues maps a chart directory (repo-relative, '/' separators) to
	// extra values files (paths relative to the root).
	HelmValues map[string][]string
	// Catalog is used only for SDK patterns (code detector). Detectors never
	// classify resources; that happens in the graph builder.
	Catalog *catalog.Index
	// Packages are the package uses found by the packages detector; the code
	// detector uses them to select SDK patterns. Filled by RunAll.
	Packages []model.PackageUse
}

// Detector parses one family of sources into facts.
type Detector interface {
	Name() string
	Detect(ctx context.Context, idx walk.Index, opts Options) (model.Facts, error)
}

// FailedSource returns a neutral, user-safe source record for a file that
// could not be parsed. Raw parser errors never reach reports.
func FailedSource(repo, path string, kind model.SourceKind, note string) model.Source {
	if note == "" {
		note = "could not be parsed"
	}
	return model.Source{Repo: repo, Path: path, Kind: kind, Status: model.StatusFailed, Note: note}
}

// RunAll runs detectors in a bounded worker pool, then runs the dependent
// detectors (which receive the package uses found in the first stage), and
// returns the sorted union of facts. A detector error never aborts the scan:
// it is recorded as a failed source named after the detector.
func RunAll(ctx context.Context, first, dependent []Detector, idx walk.Index, opts Options) model.Facts {
	var all model.Facts
	run := func(ds []Detector, o Options) {
		var mu sync.Mutex
		sem := make(chan struct{}, runtime.GOMAXPROCS(0))
		var wg sync.WaitGroup
		results := make([]model.Facts, len(ds))
		for i, d := range ds {
			wg.Add(1)
			sem <- struct{}{}
			go func(i int, d Detector) {
				defer wg.Done()
				defer func() { <-sem }()
				defer func() {
					if r := recover(); r != nil {
						mu.Lock()
						results[i] = model.Facts{Sources: []model.Source{FailedSource(o.Repo, d.Name(), model.KindCode, "detector failed")}}
						mu.Unlock()
					}
				}()
				f, err := d.Detect(ctx, idx, o)
				if err != nil {
					f.Sources = append(f.Sources, FailedSource(o.Repo, d.Name(), model.KindCode, "detector failed"))
				}
				mu.Lock()
				results[i] = f
				mu.Unlock()
			}(i, d)
		}
		wg.Wait()
		for _, f := range results { // in detector order: deterministic
			all.Append(f)
		}
	}
	run(first, opts)
	if len(dependent) > 0 {
		o := opts
		o.Packages = append([]model.PackageUse(nil), all.PackageUses...)
		run(dependent, o)
	}
	for _, s := range idx.Skipped {
		s.Repo = opts.Repo
		all.Sources = append(all.Sources, s)
	}
	all.Sort()
	return all
}
