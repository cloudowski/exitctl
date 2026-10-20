// Package testutil provides golden-file and determinism helpers for tests.
package testutil

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "update golden files")

// RepoRoot returns the repository root (the directory containing go.mod).
func RepoRoot(t testing.TB) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

// Golden compares got with testdata/golden/<name> at the repository root,
// rewriting the file when -update is set.
func Golden(t testing.TB, name string, got []byte) {
	t.Helper()
	path := filepath.Join(RepoRoot(t), "testdata", "golden", filepath.FromSlash(name))
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden %s missing (run make golden-update): %v", name, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("output differs from golden %s (run make golden-update if intended)\n--- got ---\n%s", name, got)
	}
}

// RunTwice runs fn twice and fails unless both outputs are byte-identical.
// It returns the first output.
func RunTwice(t testing.TB, fn func() []byte) []byte {
	t.Helper()
	a, b := fn(), fn()
	if !bytes.Equal(a, b) {
		t.Fatalf("output is not deterministic:\n--- run 1 ---\n%s\n--- run 2 ---\n%s", a, b)
	}
	return a
}
