package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudowski/exitctl/tools/genfixture/fixture"
)

// Performance tests (T097, SC-001). They are opt-in so that a plain
// `go test ./...` stays fast: they run only when EXITCTL_PERF=1 is set and
// are always skipped with -short.
//
//	EXITCTL_PERF=1 go test ./internal/cli -run TestPerf -count=1 -v
func TestPerf(t *testing.T) {
	if testing.Short() {
		t.Skip("perf test skipped with -short")
	}
	if os.Getenv("EXITCTL_PERF") != "1" {
		t.Skip("perf test skipped: set EXITCTL_PERF=1 to run")
	}
	cases := []struct {
		name  string
		opts  fixture.Options
		limit time.Duration
	}{
		{"500-resources", fixture.Options{Resources: 500, SourceFiles: 1000, Seed: 1}, 10 * time.Second},
		{"5000-resources-20000-files", fixture.Options{Resources: 5000, SourceFiles: 20000, Seed: 1}, 60 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "repo")
			st, err := fixture.Generate(dir, tc.opts)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("fixture: %d resources in %d modules, %d components, %d source files, %.1f MiB",
				st.Resources, st.Modules, st.Components, st.SourceFiles, float64(st.Bytes)/(1<<20))

			out := filepath.Join(t.TempDir(), "result.json")
			peak := sampleHeap()
			start := time.Now()
			var so, se bytes.Buffer
			code := Run([]string{"scan", dir, "--include-drafts", "--no-store", "--as-of", "2026-10-06",
				"--repo-name", "perf", "--emoji", "never", "--no-color", "--output", out}, &so, &se)
			elapsed := time.Since(start)
			maxHeap := peak()
			if code != 0 {
				t.Fatalf("scan exited %d: %s", code, se.String())
			}
			t.Logf("scan took %s (limit %s), peak heap in use ~%.0f MiB", elapsed.Round(time.Millisecond), tc.limit, float64(maxHeap)/(1<<20))
			checkResult(t, out, st)
			if elapsed > tc.limit {
				t.Fatalf("scan took %s, limit %s (SC-001)", elapsed, tc.limit)
			}
		})
	}
}

// checkResult makes sure the scan actually saw the generated content, so a
// fast but empty scan cannot pass.
func checkResult(t *testing.T, path string, st fixture.Stats) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Graph struct {
			Nodes []json.RawMessage `json:"nodes"`
			Edges []json.RawMessage `json:"edges"`
		} `json:"graph"`
		Facts struct {
			CodeUses []json.RawMessage `json:"code_uses"`
		} `json:"facts"`
		Analysis struct {
			Status string `json:"status"`
		} `json:"analysis"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	t.Logf("result: %d nodes, %d edges, %d code uses, status %s, %.1f MiB JSON",
		len(doc.Graph.Nodes), len(doc.Graph.Edges), len(doc.Facts.CodeUses), doc.Analysis.Status, float64(len(b))/(1<<20))
	if doc.Analysis.Status != "ok" {
		t.Fatalf("analysis status %q, want ok", doc.Analysis.Status)
	}
	if len(doc.Graph.Nodes) < st.Resources/2 {
		t.Fatalf("result has %d nodes for %d generated resources", len(doc.Graph.Nodes), st.Resources)
	}
}

// sampleHeap polls runtime.MemStats every 20 ms until the returned function
// is called, which stops polling and returns the largest HeapInuse seen.
func sampleHeap() func() uint64 {
	var maxv atomic.Uint64
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		var ms runtime.MemStats
		for {
			runtime.ReadMemStats(&ms)
			if ms.HeapInuse > maxv.Load() {
				maxv.Store(ms.HeapInuse)
			}
			select {
			case <-stop:
				return
			case <-tick.C:
			}
		}
	}()
	return func() uint64 {
		close(stop)
		wg.Wait()
		return maxv.Load()
	}
}
