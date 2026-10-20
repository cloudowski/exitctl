package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	yaml "go.yaml.in/yaml/v3"

	"github.com/cloudowski/exitctl/internal/catalog"
	"github.com/cloudowski/exitctl/internal/testutil"
)

const (
	dynamo  = "services/aws/dynamodb.yaml"
	scylla  = "alternatives/scylladb.yaml"
	reviewD = "2026-10-07"
)

// testStore copies the built-in catalog into a temp dir.
func testStore(t *testing.T, reviewer string) *Store {
	t.Helper()
	src := filepath.Join(testutil.RepoRoot(t), "catalog")
	dst := t.TempDir()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	s := &Store{dir: dst, reviewer: "cloudowski", now: func() time.Time { return now }}
	// Start every test from an all-draft catalog, independent of how far
	// the real catalog's review has progressed.
	st, err := s.State()
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, e := range st.Entries {
		files = append(files, e.File)
	}
	if err := s.SetStatus(files, statusDraft); err != nil {
		t.Fatal(err)
	}
	s.reviewer = reviewer
	return s
}

func read(t *testing.T, s *Store, file string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(s.dir, file))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func loadCat(t *testing.T, s *Store) *catalog.Catalog {
	t.Helper()
	cat, probs, err := catalog.LoadAndValidate(os.DirFS(s.dir))
	if err != nil || len(probs) > 0 {
		t.Fatalf("catalog invalid: %v %v", err, probs)
	}
	return cat
}

func service(cat *catalog.Catalog, id string) *catalog.Service {
	for i := range cat.Services {
		if cat.Services[i].ID == id {
			return &cat.Services[i]
		}
	}
	return nil
}

// TestRoundTripKeepsLayout: decoding and re-encoding any catalog file without
// changes yields the same bytes, so edits only touch what they change.
func TestRoundTripKeepsLayout(t *testing.T) {
	root := filepath.Join(testutil.RepoRoot(t), "catalog")
	n := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".yaml") || filepath.Dir(p) == root {
			return err // top-level files (manifest, providers, categories) are never edited
		}
		doc, err := loadDoc(p)
		if err != nil {
			return err
		}
		out, err := doc.encode()
		if err != nil {
			return err
		}
		if !bytes.Equal(out, doc.orig) {
			t.Errorf("%s changes on a no-op round trip", p)
		}
		n++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n < 200 {
		t.Fatalf("only %d files checked", n)
	}
}

func TestVerifyService(t *testing.T) {
	s := testStore(t, "cloudowski")
	before := read(t, s, dynamo)
	if err := s.SetStatus([]string{dynamo}, statusVerified); err != nil {
		t.Fatal(err)
	}
	after := read(t, s, dynamo)
	sv := service(loadCat(t, s), "aws.dynamodb")
	p := sv.Provenance
	if p.Status != statusVerified || p.ReviewedBy != "cloudowski" || p.ReviewedOn != reviewD || !p.LLM.EvalPassed {
		t.Fatalf("provenance = %+v", p)
	}
	for _, m := range sv.Alternatives {
		if m.ApprovedBy != "cloudowski" {
			t.Errorf("mapping %s not approved", m.Ref)
		}
	}
	if !strings.Contains(after, "reviewed_on: '"+reviewD+"'") {
		t.Error("reviewed_on is not a quoted date")
	}
	// Only provenance and mapping lines change; everything else is untouched.
	if a, b := stable(before), stable(after); a != b {
		t.Errorf("lines outside provenance and mappings changed:\n--- before ---\n%s\n--- after ---\n%s", a, b)
	}

	// Back to draft removes the review record.
	if err := s.SetStatus([]string{dynamo}, statusDraft); err != nil {
		t.Fatal(err)
	}
	p = service(loadCat(t, s), "aws.dynamodb").Provenance
	if p.Status != statusDraft || p.ReviewedBy != "" || p.ReviewedOn != "" || p.LLM.EvalPassed {
		t.Fatalf("after draft: %+v", p)
	}
}

// stable returns the lines of a service file outside its provenance block and
// alternative mapping lines.
func stable(s string) string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, "provenance:") {
			break
		}
		if !strings.HasPrefix(l, "- {ref:") {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

func TestRecommend(t *testing.T) {
	s := testStore(t, "cloudowski")
	if err := s.SetRecommended(dynamo, "scylladb", true); err != nil {
		t.Fatal(err)
	}
	m := service(loadCat(t, s), "aws.dynamodb").Alternatives[0]
	if m.Ref != "scylladb" || !m.Recommended || m.ApprovedBy != "cloudowski" {
		t.Fatalf("mapping = %+v", m)
	}
	if err := s.SetRecommended(dynamo, "scylladb", false); err != nil {
		t.Fatal(err)
	}
	if service(loadCat(t, s), "aws.dynamodb").Alternatives[0].Recommended {
		t.Fatal("still recommended")
	}
	if err := s.SetRecommended(dynamo, "no-such", true); err == nil {
		t.Fatal("unknown ref accepted")
	}

	outsider := testStore(t, "someone")
	if err := outsider.SetRecommended(dynamo, "scylladb", true); err == nil || !strings.Contains(err.Error(), "not a catalog maintainer") {
		t.Fatalf("non-maintainer could recommend: %v", err)
	}
}

// TestRemoveMappingRollsBack: removing the last alternative of a verified
// service would break validation (FR-020), so the file is restored.
func TestRemoveMappingRollsBack(t *testing.T) {
	s := testStore(t, "cloudowski")
	if err := s.SetStatus([]string{dynamo}, statusVerified); err != nil {
		t.Fatal(err)
	}
	refs := []string{"scylladb", "cassandra", "mongodb"}
	for _, r := range refs {
		if err := s.RemoveMapping(dynamo, r); err != nil {
			t.Fatalf("remove %s: %v", r, err)
		}
	}
	before := read(t, s, dynamo)
	err := s.RemoveMapping(dynamo, "postgresql")
	var ve *ValidationError
	if !errors.As(err, &ve) || !strings.Contains(strings.Join(ve.Problems, " "), "no alternatives") {
		t.Fatalf("want rollback, got %v", err)
	}
	if read(t, s, dynamo) != before {
		t.Fatal("file not restored after rollback")
	}
	if n := len(service(loadCat(t, s), "aws.dynamodb").Alternatives); n != 1 {
		t.Fatalf("alternatives = %d, want 1", n)
	}
}

// TestDeprecateHidesAlternative: a deprecated alternative is no longer
// offered for any service.
func TestDeprecateHidesAlternative(t *testing.T) {
	s := testStore(t, "cloudowski")
	if err := s.SetStatus([]string{dynamo, scylla}, statusVerified); err != nil {
		t.Fatal(err)
	}
	ix := catalog.NewIndex(loadCat(t, s), catalog.IndexOptions{})
	if ix.Alternatives("aws.dynamodb")[0].Ref != "scylladb" {
		t.Fatal("verified alternative not offered")
	}
	if err := s.SetStatus([]string{scylla}, statusDeprecated); err != nil {
		t.Fatal(err)
	}
	ix = catalog.NewIndex(loadCat(t, s), catalog.IndexOptions{})
	if len(ix.Alternatives("aws.dynamodb")) != 0 {
		t.Fatal("deprecated alternative still offered")
	}
	st, err := s.State()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range st.Entries {
		if e.File == scylla && !strings.Contains(strings.Join(e.Warnings, " "), "hidden everywhere") {
			t.Errorf("warnings = %v", e.Warnings)
		}
		if e.File == dynamo && !strings.Contains(strings.Join(e.Warnings, " "), "no alternative is shown") {
			t.Errorf("service warnings = %v", e.Warnings)
		}
	}
}

func TestBulkAndUnknownFile(t *testing.T) {
	s := testStore(t, "cloudowski")
	files := []string{"traits/proprietary-api.yaml", "traits/proprietary-sdk.yaml"}
	if err := s.SetStatus(files, statusVerified); err != nil {
		t.Fatal(err)
	}
	st, _ := s.State()
	if st.Stats["trait"][statusVerified] != 2 {
		t.Fatalf("stats = %v", st.Stats)
	}
	if err := s.SetStatus([]string{"../../etc/passwd"}, statusVerified); err == nil {
		t.Fatal("path outside the catalog accepted")
	}
	if err := s.SetStatus(files, "approved"); err == nil {
		t.Fatal("unknown status accepted")
	}
}

func TestDetail(t *testing.T) {
	s := testStore(t, "cloudowski")
	d, err := s.Detail(dynamo)
	if err != nil {
		t.Fatal(err)
	}
	if d.ID != "aws.dynamodb" || len(d.Traits) == 0 || len(d.Mappings) != 4 || d.Mappings[0].Name != "ScyllaDB" {
		t.Fatalf("detail = %+v", d.Summary)
	}
	if d.LLM == nil || d.LLM.Batch == "" || d.LLM.Model == "" {
		t.Fatalf("llm provenance missing: %+v", d.LLM)
	}
	a, err := s.Detail(scylla)
	if err != nil || len(a.UsedByList) == 0 {
		t.Fatalf("alternative detail: %v %+v", err, a)
	}
	if n, err := s.Detail("neutral/aws-context.yaml"); err != nil || !n.NotScored {
		t.Fatalf("aws-context should be not scored: %v %+v", err, n)
	}
	if n, err := s.Detail("neutral/kubernetes-core.yaml"); err != nil || n.NotScored {
		t.Fatalf("kubernetes-core should be scored: %v", err)
	}
	if _, err := s.Detail("nope.yaml"); err == nil {
		t.Fatal("unknown file accepted")
	}
}

func TestServerGuards(t *testing.T) {
	s := testStore(t, "cloudowski")
	h := newServer(s, "tok", "127.0.0.1:7788").routes()
	do := func(method, path, host, token string, body any) *httptest.ResponseRecorder {
		var b bytes.Buffer
		if body != nil {
			_ = json.NewEncoder(&b).Encode(body)
		}
		r := httptest.NewRequest(method, path, &b)
		r.Host = host
		if token != "" {
			r.Header.Set("X-Review-Token", token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := do("GET", "/", "127.0.0.1:7788", "", nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"tok"`) {
		t.Fatalf("index: %d", w.Code)
	}
	if w := do("GET", "/", "evil.example:7788", "", nil); w.Code != http.StatusForbidden {
		t.Fatalf("foreign host allowed: %d", w.Code)
	}
	if w := do("GET", "/api/state", "localhost:7788", "", nil); w.Code != http.StatusForbidden {
		t.Fatalf("api without token allowed: %d", w.Code)
	}
	if w := do("GET", "/api/state", "localhost:7788", "tok", nil); w.Code != 200 {
		t.Fatalf("state: %d %s", w.Code, w.Body)
	}
	w := do("POST", "/api/status", "127.0.0.1:7788", "tok", map[string]any{"files": []string{dynamo}, "status": "verified"})
	if w.Code != 200 {
		t.Fatalf("status: %d %s", w.Code, w.Body)
	}
	if w := do("POST", "/api/status", "127.0.0.1:7788", "tok", map[string]any{"files": []string{dynamo}, "bogus": 1}); w.Code != 400 {
		t.Fatalf("unknown field accepted: %d", w.Code)
	}
}

// TestDateStaysString guards the YAML detail that an unquoted date would
// decode as a timestamp and fail schema validation.
func TestDateStaysString(t *testing.T) {
	var n yaml.Node
	m := &yaml.Node{Kind: yaml.MappingNode}
	mapSet(m, "reviewed_on", dateNode(reviewD), "")
	n.Kind, n.Content = yaml.DocumentNode, []*yaml.Node{m}
	d := &doc{root: n}
	out, err := d.encode()
	if err != nil || strings.TrimSpace(string(out)) != "reviewed_on: '"+reviewD+"'" {
		t.Fatalf("got %q %v", out, err)
	}
}

func traitRef(t *testing.T, s *Store, svc, trait string) catalog.TraitRef {
	t.Helper()
	for _, r := range service(loadCat(t, s), svc).Traits {
		if r.ID == trait {
			return r
		}
	}
	t.Fatalf("%s has no trait %s", svc, trait)
	return catalog.TraitRef{}
}

func TestSetTraitRefLevel(t *testing.T) {
	s := testStore(t, "cloudowski")
	// proprietary-sdk has no override on DynamoDB.
	if r := traitRef(t, s, "aws.dynamodb", "proprietary-sdk"); r.Level != "" {
		t.Fatalf("fixture changed: %+v", r)
	}
	if err := s.SetTraitRefLevel(dynamo, "proprietary-sdk", "high", ""); err == nil {
		t.Fatal("override without reason accepted")
	}
	if err := s.SetTraitRefLevel(dynamo, "proprietary-sdk", "extreme", "x"); err == nil {
		t.Fatal("unknown level accepted")
	}
	// A reason with commas and colons must not break the flow mapping.
	reason := "SDK calls are everywhere: repos, handlers, and tests"
	if err := s.SetTraitRefLevel(dynamo, "proprietary-sdk", "high", reason); err != nil {
		t.Fatal(err)
	}
	if r := traitRef(t, s, "aws.dynamodb", "proprietary-sdk"); r.Level != "high" || r.Reason != reason {
		t.Fatalf("override = %+v", r)
	}
	// Changing an existing override keeps one level and one reason key.
	if err := s.SetTraitRefLevel(dynamo, "proprietary-api", "medium", "Alternator covers most calls"); err != nil {
		t.Fatal(err)
	}
	if r := traitRef(t, s, "aws.dynamodb", "proprietary-api"); r.Level != "medium" || r.Reason != "Alternator covers most calls" {
		t.Fatalf("override = %+v", r)
	}
	// Clearing removes both keys.
	if err := s.SetTraitRefLevel(dynamo, "proprietary-sdk", "", ""); err != nil {
		t.Fatal(err)
	}
	if r := traitRef(t, s, "aws.dynamodb", "proprietary-sdk"); r.Level != "" || r.Reason != "" {
		t.Fatalf("override not cleared: %+v", r)
	}
	if err := s.SetTraitRefLevel(dynamo, "no-such-trait", "low", "x"); err == nil {
		t.Fatal("unknown trait accepted")
	}
	tr, _ := s.Detail("traits/proprietary-api.yaml")
	for _, u := range tr.UsedByList {
		if u.ID == "aws.dynamodb" && u.Override != "medium" {
			t.Errorf("used-by override = %q", u.Override)
		}
	}
	d, _ := s.Detail(dynamo)
	for _, tv := range d.Traits {
		if tv.ID == "proprietary-api" && (tv.Override != "medium" || tv.Default == "" || tv.Level != "medium") {
			t.Errorf("trait view = %+v", tv)
		}
	}
}

func TestSetTraitLevelStampsReview(t *testing.T) {
	s := testStore(t, "cloudowski")
	const file = "traits/proprietary-sdk.yaml"
	if err := s.SetStatus([]string{file}, statusVerified); err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC) }
	if err := s.SetTraitLevel(file, "high"); err != nil {
		t.Fatal(err)
	}
	cat := loadCat(t, s)
	for _, tr := range cat.Traits {
		if tr.ID == "proprietary-sdk" {
			if tr.Level != "high" || tr.Provenance.Status != statusVerified || tr.Provenance.ReviewedOn != "2026-11-02" {
				t.Fatalf("trait = level %s, provenance %+v", tr.Level, tr.Provenance)
			}
		}
	}
	if err := s.SetTraitLevel(dynamo, "high"); err == nil {
		t.Fatal("trait level set on a service")
	}
	if err := s.SetTraitLevel(file, "huge"); err == nil {
		t.Fatal("unknown level accepted")
	}
	st, _ := s.State()
	if len(st.Levels) != 5 {
		t.Fatalf("levels = %v", st.Levels)
	}
}
