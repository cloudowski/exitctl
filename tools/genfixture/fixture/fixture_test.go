package fixture

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func treeHash(t *testing.T, dir string) string {
	t.Helper()
	h := sha256.New()
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		h.Write([]byte(filepath.ToSlash(rel) + "\x00"))
		h.Write(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func TestGenerateDeterministicAndCounts(t *testing.T) {
	o := Options{Resources: 123, Modules: 4, SourceFiles: 57, FilesPerComponent: 10, Seed: 7}
	a, b := filepath.Join(t.TempDir(), "a"), filepath.Join(t.TempDir(), "b")
	sa, err := Generate(a, o)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(b, o); err != nil {
		t.Fatal(err)
	}
	if treeHash(t, a) != treeHash(t, b) {
		t.Fatal("same options produced different trees")
	}
	if sa.Resources != 123 || sa.Modules != 4 || sa.SourceFiles != 57 || sa.Components != 6 {
		t.Fatalf("stats %+v", sa)
	}
	c := filepath.Join(t.TempDir(), "c")
	o.Seed = 8
	if _, err := Generate(c, o); err != nil {
		t.Fatal(err)
	}
	if treeHash(t, a) == treeHash(t, c) {
		t.Fatal("different seeds produced identical trees")
	}
}
