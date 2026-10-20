package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultFingerprintStable(t *testing.T) {
	a, b := DefaultScoring().Fingerprint(), DefaultScoring().Fingerprint()
	if a != b || len(a) != 8 {
		t.Fatalf("fingerprint unstable or wrong length: %q %q", a, b)
	}
	if DefaultScoring().Custom() {
		t.Fatal("defaults must not be custom")
	}
}

func TestOverrideChangesFingerprint(t *testing.T) {
	cfg, err := Parse(".exitctl.yaml", []byte("version: 1\nscoring:\n  dimension_weights:\n    technical: 0.5\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Scoring.Custom() {
		t.Fatal("override must mark scoring as custom")
	}
	if cfg.Scoring.Fingerprint() == DefaultScoring().Fingerprint() {
		t.Fatal("override must change the fingerprint")
	}
	if cfg.Scoring.DimensionWeights["data"] != 1.0 {
		t.Fatal("non-overridden weights must keep defaults")
	}
}

func TestInvalidBandsRejected(t *testing.T) {
	_, err := Parse("bad.yaml", []byte(`version: 1
scoring:
  grade_bands:
    - {grade: A, min: 0, max: 40}
    - {grade: F, min: 50, max: 100}
`))
	if err == nil || !strings.Contains(err.Error(), "bad.yaml") {
		t.Fatalf("expected error naming the file, got %v", err)
	}
}

func TestDiscoverPrefersScanRoot(t *testing.T) {
	root, cwd := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".exitctl.yaml"), []byte("version: 1\nrepo_name: from-root\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".exitctl.yaml"), []byte("version: 1\nrepo_name: from-cwd\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Discover(root, cwd, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RepoName != "from-root" {
		t.Fatalf("got %q", cfg.RepoName)
	}
	cfg, err = Discover(t.TempDir(), cwd, "")
	if err != nil || cfg.RepoName != "from-cwd" {
		t.Fatalf("fallback to cwd failed: %v %q", err, cfg.RepoName)
	}
	cfg, err = Discover(t.TempDir(), t.TempDir(), "")
	if err != nil || cfg.Scoring.Custom() {
		t.Fatalf("defaults expected: %v", err)
	}
}
