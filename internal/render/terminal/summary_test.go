package terminal

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/cloudowski/exitctl/internal/model"
	"github.com/cloudowski/exitctl/internal/result"
)

func ip(v int) *int { return &v }

func plainOpts() Options { return Options{UTF8: true, Emoji: true, Width: 80} }

func baseDoc() *result.Document {
	d := &result.Document{
		Versions: result.Versions{Tool: "0.1.0", Catalog: "2026.10.1", Rules: "1.0.0", ConfigFingerprint: "3f2a9c1e"},
	}
	for i := 0; i < 3; i++ {
		d.Facts.RawNodes = append(d.Facts.RawNodes, model.RawNode{
			ID: fmt.Sprintf("tf-%d", i), NodeKind: model.NodeResource, SourceKind: model.KindTerraform, Type: "aws_x",
		})
	}
	// Duplicate ID is counted once.
	d.Facts.RawNodes = append(d.Facts.RawNodes,
		model.RawNode{ID: "tf-0", NodeKind: model.NodeResource, SourceKind: model.KindTerraform, Type: "aws_x"},
		model.RawNode{ID: "k8s-1", NodeKind: model.NodeResource, SourceKind: model.KindHelm, Type: "Deployment"},
		model.RawNode{ID: "k8s-2", NodeKind: model.NodeResource, SourceKind: model.KindKubernetes, Type: "Service"},
		model.RawNode{ID: "img-1", NodeKind: model.NodeResource, SourceKind: model.KindDockerfile, Type: model.TypeContainerImage},
		model.RawNode{ID: "comp-1", NodeKind: model.NodeComponent, SourceKind: model.KindPackageManifest, Type: model.TypeComponent},
	)
	d.Graph.Nodes = []result.Node{
		{ID: "aws://dynamodb/orders", NativeAddress: "aws_dynamodb_table.orders", Service: "aws.dynamodb", Type: "aws_dynamodb_table"},
		{ID: "aws://sqs/events", NativeAddress: "aws_sqs_queue.events", Service: "aws.sqs", Type: "aws_sqs_queue"},
		{ID: "aws://cognito/main", NativeAddress: "aws_cognito_user_pool.main", Service: "aws.cognito", Type: "aws_cognito_user_pool"},
	}
	d.Analysis = result.Analysis{
		Status: "ok", Index: ip(62), IndexWithoutExceptions: ip(62), Grade: "D", Confidence: "normal",
		Dimensions: map[string]*int{"technical": ip(70), "data": ip(75), "operational": ip(54), "skills": ip(25), "economic": nil},
		Anchors: []result.Anchor{
			{Rank: 1, Node: "aws://dynamodb/orders", DependentsCount: 41, SDKFiles: 34, ComponentsUsing: 2,
				Alternatives: []result.AlternativeView{
					{ID: "scylladb", Name: "ScyllaDB", Recommended: true},
					{ID: "yugabytedb", Name: "YugabyteDB"},
					{ID: "cassandra", Name: "Cassandra"},
				}},
			{Rank: 2, Node: "aws://sqs/events", DependentsCount: 27,
				Alternatives: []result.AlternativeView{{ID: "rabbitmq", Name: "RabbitMQ", Recommended: true}, {ID: "nats", Name: "NATS"}}},
			{Rank: 3, Node: "aws://cognito/main", DependentsCount: 9,
				Alternatives: []result.AlternativeView{{ID: "keycloak", Name: "Keycloak"}, {ID: "zitadel", Name: "Zitadel"}}},
		},
		Lever:    &result.Lever{Service: "aws.cognito", Alternative: "keycloak", Delta: 9},
		Coverage: result.Coverage{Types: 0.94, Resources: 0.975, Weighted: 1, UnknownTypes: 8},
	}
	return d
}

func render(t *testing.T, d *result.Document, o Options) string {
	t.Helper()
	var b bytes.Buffer
	if err := Summary(&b, d, o); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestSummaryNormal(t *testing.T) {
	o := plainOpts()
	o.Names = map[string]string{"aws.cognito": "Cognito", "aws.dynamodb": "DynamoDB"}
	got := render(t, baseDoc(), o)
	want := `Offline, read-only scan: 3 Terraform resources, 2 K8s objects, 1 image, 1 component
  Lock-in index  62/100  grade D
  technical ███████░░░  data ████████░░  operational █████░░░░░  skills ███░░░░░░░
  economic: not assessed

  Top anchors                    dependents  alternatives
  1. aws_dynamodb_table.orders           41  ⭐️ ScyllaDB, YugabyteDB +1
     + 34 files in 2 components use DynamoDB SDK
  2. aws_sqs_queue.events                27  ⭐️ RabbitMQ, NATS
  3. aws_cognito_user_pool.main           9  Keycloak, Zitadel

  🎯 Biggest lever: Cognito -> Keycloak lowers the index by 9 pts
  ✅ Coverage: 94% of types, 97% of resources (8 unknown types: exitctl coverage)
  Explain #1: exitctl explain 1  |  Report: exitctl report --format html --open
  exitctl 0.1.0 · catalog 2026.10.1 · rules 1.0.0 · config 3f2a9c1e
`
	if got != want {
		t.Fatalf("summary mismatch\n--- got\n%s--- want\n%s", got, want)
	}
	if strings.Contains(got, "\x1b[") {
		t.Fatal("ANSI codes without color")
	}
	if strings.ContainsAny(got, "\u2013\u2014") {
		t.Fatal("em/en dash in output")
	}
}

func TestSummaryNoNamesUsesIDs(t *testing.T) {
	got := render(t, baseDoc(), plainOpts())
	for _, s := range []string{"use aws.dynamodb SDK", "Biggest lever: aws.cognito -> Keycloak lowers"} {
		if !strings.Contains(got, s) {
			t.Errorf("missing %q in\n%s", s, got)
		}
	}
}

func TestSummaryProvisionalWithRange(t *testing.T) {
	d := baseDoc()
	d.Analysis.Confidence = "provisional"
	d.Analysis.Coverage.Weighted = 0.81
	d.Analysis.Range = &result.Range{Lower: 58, Upper: 71}
	got := render(t, d, plainOpts())
	want := "  Lock-in index  62/100  grade D (provisional, coverage 81%)  range 58-71\n"
	if !strings.Contains(got, want) {
		t.Fatalf("missing %q in\n%s", want, got)
	}
}

func TestSummaryNormalWithRange(t *testing.T) {
	d := baseDoc()
	d.Analysis.Range = &result.Range{Lower: 60, Upper: 64}
	got := render(t, d, plainOpts())
	if !strings.Contains(got, "  Lock-in index  62/100  grade D  range 60-64\n") {
		t.Fatalf("unexpected index line in\n%s", got)
	}
}

func TestSummaryWithheld(t *testing.T) {
	d := baseDoc()
	d.Analysis.Confidence = "withheld"
	d.Analysis.Grade = ""
	d.Analysis.Coverage.Weighted = 0.62
	d.Analysis.Range = &result.Range{Lower: 40, Upper: 77}
	got := render(t, d, plainOpts())
	want := "  Lock-in index range 40-77 - insufficient coverage for a grade (62%)\n"
	if !strings.Contains(got, want) {
		t.Fatalf("missing %q in\n%s", want, got)
	}
	if strings.Contains(got, "grade D") {
		t.Fatal("grade shown when withheld")
	}
}

func TestSummaryNothingScored(t *testing.T) {
	d := baseDoc()
	d.Analysis = result.Analysis{Status: "nothing_scored", Coverage: result.Coverage{Types: 0, Resources: 0}}
	got := render(t, d, plainOpts())
	want := `Offline, read-only scan: 3 Terraform resources, 2 K8s objects, 1 image, 1 component
  Nothing to score: no recognized cloud resources or SDK usage found.
  ❌ Coverage: 0% of types, 0% of resources
  Unrecognized types: exitctl coverage
  exitctl 0.1.0 · catalog 2026.10.1 · rules 1.0.0 · config 3f2a9c1e
`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestSummaryCustomScoringAndDrafts(t *testing.T) {
	d := baseDoc()
	d.Versions.CustomScoring = true
	d.Versions.CatalogDraftsIncluded = true
	got := render(t, d, plainOpts())
	if !strings.Contains(got, "config 3f2a9c1e (custom scoring) (draft catalog)\n") {
		t.Fatalf("footer missing markers in\n%s", got)
	}
}

func TestSummaryExceptions(t *testing.T) {
	d := baseDoc()
	d.Analysis.IndexWithoutExceptions = ip(66)
	d.Analysis.Exceptions = []result.ExceptionState{{ID: "a", State: "active"}, {ID: "b", State: "active"}, {ID: "c", State: "expired"}}
	got := render(t, d, plainOpts())
	if !strings.Contains(got, "  Exceptions: 2 accepted (index without exceptions: 66)\n") {
		t.Fatalf("exceptions line missing in\n%s", got)
	}
	d.Analysis.Exceptions = []result.ExceptionState{{ID: "c", State: "invalid"}}
	if strings.Contains(render(t, d, plainOpts()), "Exceptions:") {
		t.Fatal("exceptions line shown without active exceptions")
	}
}

func TestExceptionNotices(t *testing.T) {
	d := baseDoc()
	d.Inputs = []result.Input{{Repo: "a", AsOf: "2026-10-01"}, {Repo: "b", AsOf: "2026-10-06"}}
	d.Analysis.Exceptions = []result.ExceptionState{
		{ID: "keep", State: "active", Reason: "r", Owner: "o"},
		{ID: "no-match", State: "invalid", Reason: "r", Owner: "o"},
		{ID: "no-owner", State: "invalid", Reason: "r"},
		{ID: "no-reason", State: "invalid", Owner: "o"},
		{ID: "old", State: "expired", Reason: "r", Owner: "o", Expires: "2026-01-01", MatchedFindings: []string{"x::t"}},
	}
	got := strings.Join(ExceptionNotices(d), "\n")
	want := strings.Join([]string{
		"notice: exception no-match is invalid and ignored: its selector matches no finding",
		"notice: exception no-owner is invalid and ignored: it has no owner",
		"notice: exception no-reason is invalid and ignored: it has no reason",
		"notice: exception old expired on 2026-01-01 (as of 2026-10-06); its 1 finding count again",
	}, "\n")
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	if strings.Contains(render(t, d, plainOpts()), "notice:") {
		t.Fatal("notices must not be part of the summary")
	}
}

func TestSummaryRecommendedMarker(t *testing.T) {
	o := plainOpts()
	o.Emoji = false
	got := render(t, baseDoc(), o)
	if !strings.Contains(got, "[R] ScyllaDB, YugabyteDB +1") || strings.Contains(got, "⭐") {
		t.Fatalf("expected [R] marker in\n%s", got)
	}
}

func TestSummaryASCIIFallback(t *testing.T) {
	o := Options{Width: 80}
	got := render(t, baseDoc(), o)
	if !strings.Contains(got, "technical #######...  data ########..  operational #####.....  skills ###.......") {
		t.Fatalf("ASCII bars missing in\n%s", got)
	}
	if !strings.Contains(got, "exitctl 0.1.0 - catalog 2026.10.1 - rules 1.0.0 - config 3f2a9c1e") {
		t.Fatalf("ASCII footer missing in\n%s", got)
	}
	for _, r := range got {
		if r > 127 {
			t.Fatalf("non-ASCII rune %q in\n%s", r, got)
		}
	}
}

func TestSummaryColorOnlyReinforces(t *testing.T) {
	o := plainOpts()
	o.Color = true
	colored := render(t, baseDoc(), o)
	if !strings.Contains(colored, "\x1b[1;38;5;208mD\x1b[0m") {
		t.Fatalf("grade D not colored orange in\n%q", colored)
	}
	if stripANSI(colored) != render(t, baseDoc(), plainOpts()) {
		t.Fatalf("colored output differs from plain output after stripping colors\n%s", stripANSI(colored))
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func manyAnchorsDoc(n int) *result.Document {
	d := baseDoc()
	d.Analysis.Anchors = nil
	d.Graph.Nodes = nil
	for i := 1; i <= n; i++ {
		id := fmt.Sprintf("node-%02d", i)
		d.Graph.Nodes = append(d.Graph.Nodes, result.Node{ID: id, NativeAddress: "aws_s3_bucket.b" + id, Service: "aws.s3"})
		d.Analysis.Anchors = append(d.Analysis.Anchors, result.Anchor{
			Rank: i, Node: id, DependentsCount: 100 - i, SDKFiles: 3, ComponentsUsing: 1,
			Alternatives: []result.AlternativeView{{ID: "minio", Name: "MinIO", Recommended: true}},
		})
	}
	d.Analysis.Exceptions = []result.ExceptionState{{ID: "a", State: "active"}}
	return d
}

func TestSummaryLineCap(t *testing.T) {
	got := render(t, manyAnchorsDoc(30), plainOpts())
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	if len(lines) > MaxSummaryLines {
		t.Fatalf("%d lines > %d\n%s", len(lines), MaxSummaryLines, got)
	}
	// Fixed sections survive; anchors are truncated with a hint.
	for _, s := range []string{"Biggest lever:", "Coverage:", "Exceptions:", "Explain #1:", "config 3f2a9c1e", "more anchors:"} {
		if !strings.Contains(got, s) {
			t.Fatalf("missing %q in\n%s", s, got)
		}
	}
	// Every shown anchor row keeps its bridge line directly below.
	for i, l := range lines {
		if strings.Contains(l, "aws_s3_bucket.") && (i+1 >= len(lines) || !strings.HasPrefix(lines[i+1], "     + 3 files in 1 component use aws.s3 SDK")) {
			t.Fatalf("anchor row without bridge at line %d\n%s", i+1, got)
		}
	}
}

func TestSummaryAllListsEveryAnchor(t *testing.T) {
	o := plainOpts()
	o.All = true
	got := render(t, manyAnchorsDoc(30), o)
	if n := strings.Count(got, "aws_s3_bucket."); n != 30 {
		t.Fatalf("--all shows %d of 30 anchors\n%s", n, got)
	}
	if strings.Contains(got, "more anchors") {
		t.Fatalf("--all prints a more-anchors hint\n%s", got)
	}
	for _, s := range []string{"Biggest lever:", "Coverage:", "Exceptions:", "Explain #1:", "config 3f2a9c1e", "\n  30. aws_s3_bucket.bnode-30"} {
		if !strings.Contains(got, s) {
			t.Fatalf("missing %q in\n%s", s, got)
		}
	}
}

func TestSummaryDefaultAnchors(t *testing.T) {
	// Without --all at most 5 anchors are shown.
	got := render(t, manyAnchorsDoc(30), plainOpts())
	if strings.Count(got, "aws_s3_bucket.") > DefaultAnchors {
		t.Fatalf("more than %d anchors without --all\n%s", DefaultAnchors, got)
	}
	// Column widths fit the shown anchors only (rank 30 is hidden).
	if !strings.Contains(got, "\n  1. aws_s3_bucket.bnode-01") {
		t.Fatalf("rank column padded for hidden anchors\n%s", got)
	}
	if !strings.Contains(got, "  ... 25 more anchors: exitctl scan --all\n") {
		t.Fatalf("missing more hint\n%s", got)
	}
}

func TestSummaryLongAddressTruncated(t *testing.T) {
	d := baseDoc()
	d.Graph.Nodes[0].NativeAddress = "module.very_long_module_name.module.inner.aws_dynamodb_table.orders"
	got := render(t, d, plainOpts())
	if !strings.Contains(got, "1. module.very_long_module_name.module.i...") {
		t.Fatalf("address not truncated to 40 columns\n%s", got)
	}
}

func TestSummaryNoAnchorsNoLever(t *testing.T) {
	d := baseDoc()
	d.Analysis.Anchors = nil
	d.Analysis.Lever = nil
	d.Analysis.Coverage.UnknownTypes = 0
	got := render(t, d, plainOpts())
	if strings.Contains(got, "Biggest lever") || !strings.Contains(got, "  Unrecognized types: exitctl coverage\n") {
		t.Fatalf("unexpected output\n%s", got)
	}
	if !strings.Contains(got, "  ✅ Coverage: 94% of types, 97% of resources\n") {
		t.Fatalf("coverage line should omit unknown types parenthesis\n%s", got)
	}
}

func TestSummaryDeterministic(t *testing.T) {
	o := plainOpts()
	o.All = true
	d := manyAnchorsDoc(12)
	if a, b := render(t, d, o), render(t, d, o); a != b {
		t.Fatalf("non-deterministic output\n%s\n---\n%s", a, b)
	}
}

func TestBar(t *testing.T) {
	o := Options{}
	cases := map[int]string{0: "..........", 4: "..........", 5: "#.........", 64: "######....", 65: "#######...", 100: "##########"}
	for v, want := range cases {
		if got := o.bar(v); got != want {
			t.Errorf("bar(%d) = %q, want %q", v, got, want)
		}
	}
}

func TestDetectOptions(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	o := detectOptions(false, "auto", true, env(map[string]string{"LANG": "en_US.UTF-8", "TERM": "xterm-256color"}))
	if !o.Color || !o.Emoji || !o.UTF8 {
		t.Fatalf("expected color, emoji and UTF-8: %+v", o)
	}
	o = detectOptions(false, "auto", true, env(map[string]string{"LANG": "en_US.UTF-8", "TERM": "linux", "NO_COLOR": "1"}))
	if o.Color || o.Emoji || !o.UTF8 {
		t.Fatalf("NO_COLOR and TERM=linux: %+v", o)
	}
	o = detectOptions(true, "always", true, env(map[string]string{"LC_ALL": "C", "LANG": "en_US.utf8"}))
	if o.Color || !o.Emoji || o.UTF8 {
		t.Fatalf("--no-color, --emoji=always, LC_ALL=C: %+v", o)
	}
	o = detectOptions(false, "never", false, env(map[string]string{"LC_CTYPE": "UTF-8"}))
	if o.Color || o.Emoji || !o.UTF8 {
		t.Fatalf("not a terminal, --emoji=never: %+v", o)
	}
	var b bytes.Buffer
	if DetectOptions(false, "auto", &b).Color {
		t.Fatal("color enabled for a non-file writer")
	}
}

func dupAnchorsDoc() *result.Document {
	d := baseDoc()
	d.Analysis.Anchors = nil
	d.Graph.Nodes = nil
	ids := []string{
		"repo//manifests/modules/security/irsa/.workshop/terraform#aws_kms_key.cmk_dynamodb",
		"repo//manifests/modules/security/eks-pod-identity/.workshop/terraform#aws_kms_key.cmk_dynamodb",
		"repo//manifests/modules/aiml/kiro-cli/.workshop/terraform#aws_kms_key.cmk_dynamodb",
		"repo//manifests/modules/fastpaths/developers/.workshop/terraform#module.preprovision.aws_dynamodb_table.carts_with_a_very_long_name",
	}
	for i, id := range ids {
		_, addr, _ := strings.Cut(id, "#")
		d.Graph.Nodes = append(d.Graph.Nodes, result.Node{ID: id, NativeAddress: addr, Service: "aws.kms"})
		d.Analysis.Anchors = append(d.Analysis.Anchors, result.Anchor{
			Rank: i + 1, Node: id, DependentsCount: 10 - i,
			Alternatives: []result.AlternativeView{{ID: "a", Name: "Alpha"}, {ID: "b", Name: "Beta"}, {ID: "c", Name: "Gamma"}, {ID: "d", Name: "Delta"}},
		})
	}
	return d
}

func TestAnchorLocations(t *testing.T) {
	got := AnchorLocations(dupAnchorsDoc())
	want := map[string]string{
		"repo//manifests/modules/security/irsa/.workshop/terraform#aws_kms_key.cmk_dynamodb":             "security/irsa",
		"repo//manifests/modules/security/eks-pod-identity/.workshop/terraform#aws_kms_key.cmk_dynamodb": "security/eks-pod-identity",
		"repo//manifests/modules/aiml/kiro-cli/.workshop/terraform#aws_kms_key.cmk_dynamodb":             "aiml/kiro-cli",
	}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %q, want %q", k, got[k], v)
		}
	}
	// A location that is a prefix of another keeps a non-empty label.
	d := baseDoc()
	d.Analysis.Anchors = nil
	d.Graph.Nodes = nil
	for i, id := range []string{"repo//#aws_s3_bucket.b", "repo//infra#aws_s3_bucket.b"} {
		d.Graph.Nodes = append(d.Graph.Nodes, result.Node{ID: id, NativeAddress: "aws_s3_bucket.b"})
		d.Analysis.Anchors = append(d.Analysis.Anchors, result.Anchor{Rank: i + 1, Node: id})
	}
	got = AnchorLocations(d)
	if got["repo//#aws_s3_bucket.b"] == got["repo//infra#aws_s3_bucket.b"] {
		t.Fatalf("labels not distinct: %v", got)
	}
}

func TestSummaryWide(t *testing.T) {
	narrow := render(t, dupAnchorsDoc(), plainOpts())
	if !strings.Contains(narrow, "Alpha, Beta +2") || strings.Contains(narrow, "security/irsa") || !strings.Contains(narrow, "...") {
		t.Fatalf("default view changed\n%s", narrow)
	}
	o := plainOpts()
	o.Wide = true
	got := render(t, dupAnchorsDoc(), o)
	for _, s := range []string{
		"1. security/irsa: aws_kms_key.cmk_dynamodb ",
		"2. security/eks-pod-identity: aws_kms_key.cmk_dynamodb ",
		"3. aiml/kiro-cli: aws_kms_key.cmk_dynamodb ",
		"4. module.preprovision.aws_dynamodb_table.carts_with_a_very_long_name ",
		"Alpha, Beta, Gamma, Delta\n",
	} {
		if !strings.Contains(got, s) {
			t.Fatalf("missing %q in\n%s", s, got)
		}
	}
}
