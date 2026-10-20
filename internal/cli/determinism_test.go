package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/cloudowski/exitctl/internal/testutil"
)

// Determinism suite (T095, SC-003, R15). Every fixture under
// testdata/fixtures is scanned and rendered twice per output format and the
// bytes are compared. Merge input-order independence is covered by
// TestMergePermutations (merge_test.go); `make determinism` runs both.

// fixtureGroups lists fixture directories whose children are separate repos.
var fixtureGroups = map[string]bool{"split": true}

// determinismFixtures discovers fixtures dynamically and returns their paths
// relative to testdata/fixtures, sorted.
func determinismFixtures(t *testing.T) []string {
	t.Helper()
	base := filepath.Join(testutil.RepoRoot(t), "testdata", "fixtures")
	var out []string
	entries, err := os.ReadDir(base)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if !fixtureGroups[e.Name()] {
			out = append(out, e.Name())
			continue
		}
		kids, err := os.ReadDir(filepath.Join(base, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, k := range kids {
			if k.IsDir() {
				out = append(out, e.Name()+"/"+k.Name())
			}
		}
	}
	sort.Strings(out)
	if len(out) == 0 {
		t.Fatal("no fixtures found")
	}
	return out
}

// detScan runs `scan --format json` on path and returns stdout and the exit
// code. Exit 2 ("nothing scored") is accepted; its output is still compared.
func detScan(t *testing.T, path, repo, resultFile string) ([]byte, int) {
	t.Helper()
	var so, se bytes.Buffer
	code := Run([]string{"scan", path, "--format", "json",
		"--include-drafts", "--as-of", "2026-10-06", "--repo-name", repo,
		"--emoji", "never", "--no-color", "--no-store", "--output", resultFile}, &so, &se)
	if code != 0 && code != ExitNothing {
		t.Fatalf("scan %s exited %d: %s", path, code, se.String())
	}
	return so.Bytes(), code
}

// detRender runs a report or graph command against input and returns the
// rendered bytes.
func detRender(t *testing.T, input string, args ...string) []byte {
	t.Helper()
	var so, se bytes.Buffer
	full := append([]string{"--input", input, "--emoji", "never", "--no-color"}, args...)
	if args[0] == "report" {
		out := filepath.Join(t.TempDir(), "report")
		full = append(full, "-o", out)
		if code := Run(full, &so, &se); code != 0 {
			t.Fatalf("%v exited %d: %s", args, code, se.String())
		}
		b, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	if code := Run(full, &so, &se); code != 0 {
		t.Fatalf("%v exited %d: %s", args, code, se.String())
	}
	return so.Bytes()
}

func TestDeterminism(t *testing.T) {
	root := testutil.RepoRoot(t)
	for _, fx := range determinismFixtures(t) {
		t.Run(fx, func(t *testing.T) {
			repo := filepath.Base(fx)
			abs := filepath.Join(root, "testdata", "fixtures", filepath.FromSlash(fx))
			dir := t.TempDir()
			res := filepath.Join(dir, "result.json")
			var code int
			js := testutil.RunTwice(t, func() []byte {
				var b []byte
				b, code = detScan(t, abs, repo, res)
				return b
			})
			if len(js) == 0 {
				t.Fatal("scan --format json produced no output")
			}
			assertNoPathLeak(t, js, root, dir)
			if code == ExitNothing {
				t.Logf("%s: nothing scored (exit 2); rendering skipped", fx)
				return
			}
			for _, args := range [][]string{
				{"report", "--format", "html"},
				{"report", "--format", "md"},
				{"graph", "--format", "dot"},
				{"graph", "--format", "json"},
			} {
				t.Run(strings.Join(args, "-"), func(t *testing.T) {
					out := testutil.RunTwice(t, func() []byte { return detRender(t, res, args...) })
					if len(out) == 0 {
						t.Fatalf("%v produced no output", args)
					}
					assertNoPathLeak(t, out, root, dir)
				})
			}
		})
	}
}

// TestDeterminismPaths: the result does not depend on the working directory
// or on whether the scanned path is absolute or relative.
func TestDeterminismPaths(t *testing.T) {
	root := testutil.RepoRoot(t)
	for _, fx := range determinismFixtures(t) {
		t.Run(fx, func(t *testing.T) {
			repo := filepath.Base(fx)
			rel := filepath.Join("testdata", "fixtures", filepath.FromSlash(fx))
			abs := filepath.Join(root, rel)
			dir := t.TempDir()
			other := t.TempDir()

			type variant struct {
				name, cwd, path string
			}
			variants := []variant{
				{"absolute-from-tempdir", other, abs},
				{"relative-from-root", root, rel},
				{"dot-from-fixture", abs, "."},
				{"relative-from-tempdir", other, mustRel(t, other, abs)},
			}
			var first []byte
			for i, v := range variants {
				t.Chdir(v.cwd)
				js, _ := detScan(t, v.path, repo, filepath.Join(dir, v.name+".json"))
				assertNoPathLeak(t, js, root, dir, other)
				if i == 0 {
					first = js
					continue
				}
				if !bytes.Equal(first, js) {
					t.Fatalf("scan JSON differs between %s and %s:\n--- %s ---\n%s\n--- %s ---\n%s",
						variants[0].name, v.name, variants[0].name, first, v.name, js)
				}
			}
		})
	}
}

func mustRel(t *testing.T, from, to string) string {
	t.Helper()
	// Resolve symlinks (macOS /var -> /private/var) so the relative path is valid.
	f, err := filepath.EvalSymlinks(from)
	if err != nil {
		t.Fatal(err)
	}
	tt, err := filepath.EvalSymlinks(to)
	if err != nil {
		t.Fatal(err)
	}
	r, err := filepath.Rel(f, tt)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// assertNoPathLeak fails when out contains any of the given absolute
// directories (or their symlink-resolved forms).
func assertNoPathLeak(t *testing.T, out []byte, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		cands := []string{d}
		if r, err := filepath.EvalSymlinks(d); err == nil && r != d {
			cands = append(cands, r)
		}
		for _, c := range cands {
			if bytes.Contains(out, []byte(c)) {
				t.Fatalf("output leaks absolute path %s", c)
			}
		}
	}
}
