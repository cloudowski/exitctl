package html

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/cloudowski/exitctl/internal/model"
	"github.com/cloudowski/exitctl/internal/result"
	"github.com/cloudowski/exitctl/internal/testutil"
)

func loadShop(t *testing.T) *result.Document {
	t.Helper()
	doc, err := result.Load(filepath.Join(testutil.RepoRoot(t), "testdata", "golden", "scan", "shop-aws.json"))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

var testNames = map[string]string{
	"aws.dynamodb": "Amazon DynamoDB",
	"aws.iam":      "AWS IAM",
	"aws.lambda":   "AWS Lambda",
}

func render(t *testing.T, doc *result.Document, opts Options) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := Render(&buf, doc, opts); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestReportGolden(t *testing.T) {
	doc := loadShop(t)
	out := testutil.RunTwice(t, func() []byte {
		return render(t, loadShop(t), Options{Names: testNames, SupportEmail: "support@example.com"})
	})
	testutil.Golden(t, "report/shop-aws.html", out)

	s := string(out)
	for _, want := range []string{
		`class="grade-letter" id="grade"`, ">" + doc.Analysis.Grade + "</div>", ExitReadinessLine,
		"support@example.com", "not assessed", `<script type="application/json" id="data">`,
		"aws_dynamodb_table.orders", "rules " + doc.Versions.Rules,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("report misses %q", want)
		}
	}
	for _, r := range []string{"\u2013", "\u2014"} {
		if strings.Contains(s, r) {
			t.Errorf("report contains a dash character %q", r)
		}
	}
}

var (
	srcAttr  = regexp.MustCompile(`(?i)<(script|img|iframe|link|source|video|audio|embed|object)\b[^>]*\b(src|href|data)\s*=`)
	extLoad  = regexp.MustCompile(`(?i)\b(src|href)\s*=\s*["']?\s*(https?:)?//`)
	cssLoads = regexp.MustCompile(`(?i)(@import|url\(\s*["']?\s*(https?:)?//)`)
)

func TestReportSelfContained(t *testing.T) {
	s := string(render(t, loadShop(t), Options{}))
	if m := srcAttr.FindString(s); m != "" {
		t.Errorf("report loads an external resource: %s", m)
	}
	if m := extLoad.FindString(s); m != "" {
		t.Errorf("report references a network URL: %s", m)
	}
	if m := cssLoads.FindString(s); m != "" {
		t.Errorf("report CSS loads a resource: %s", m)
	}
	if strings.Contains(s, "Questions or feedback") {
		t.Error("support line must be omitted when SupportEmail is empty")
	}
}

func TestReportPayload(t *testing.T) {
	s := string(render(t, loadShop(t), Options{}))
	const open = `<script type="application/json" id="data">`
	i := strings.Index(s, open)
	j := strings.Index(s[i:], "</script>")
	var d gData
	if err := json.Unmarshal([]byte(s[i+len(open):i+j]), &d); err != nil {
		t.Fatalf("payload is not valid JSON: %v", err)
	}
	if len(d.Nodes) != 30 || len(d.Edges) != 25 {
		t.Fatalf("payload has %d nodes and %d edges, want 30 and 25", len(d.Nodes), len(d.Edges))
	}
}

// TestReportNeutralCoverage checks that source notes and gap subjects of
// parse failures never appear in the report, and that withheld grades and
// grouped graphs render.
func TestReportNeutralCoverage(t *testing.T) {
	doc := loadShop(t)
	notes := []string{
		"hcl: main.tf:3,7-8: Argument or block definition required",
		"yaml: line 4: mapping values are not allowed in this context",
	}
	doc.Facts.Sources = append(doc.Facts.Sources,
		model.Source{Repo: "shop-aws", Path: "infra/broken.tf", Kind: model.KindTerraform, Status: model.StatusFailed, Note: notes[0]},
		model.Source{Repo: "shop-aws", Path: "k8s/broken.yaml", Kind: model.KindKubernetes, Status: model.StatusPartial, Note: notes[1]},
	)
	doc.Facts.Gaps = append(doc.Facts.Gaps, model.Gap{Kind: model.GapParseFailure, Subject: notes[0], Repo: "shop-aws", Path: "infra/broken.tf", Count: 1})
	doc.Analysis.Confidence = "withheld"
	doc.Analysis.Grade = ""
	doc.Analysis.Range = &result.Range{Lower: 20, Upper: 45}
	doc.Graph.Groups = []result.Group{
		{ID: "g-mod", Kind: "module", Label: "infra", Members: []string{"shop-aws//infra#aws_vpc.main"}},
		{ID: "g-sub", Kind: "type", Label: "aws_subnet x 2", Parent: "g-mod", MaxRisk: 0.225,
			Members: []string{"shop-aws//infra#aws_subnet.a", "shop-aws//infra#aws_subnet.b"}},
	}
	for i := range doc.Graph.Nodes {
		n := &doc.Graph.Nodes[i]
		switch n.ID {
		case "shop-aws//infra#aws_subnet.a", "shop-aws//infra#aws_subnet.b":
			n.Group = "g-sub"
		case "shop-aws//infra#aws_vpc.main":
			n.Group = "g-mod"
		}
	}
	s := string(render(t, doc, Options{}))
	for _, n := range notes {
		if strings.Contains(s, n) {
			t.Errorf("raw source note appears in the report: %q", n)
		}
	}
	for _, want := range []string{"infra/broken.tf", "could not be read", "k8s/broken.yaml", "partly read",
		`class="grade-letter" id="grade"`, "grade withheld", "between 20 and 45", `"label":"aws_subnet x 2"`, `"parent":0`} {
		if !strings.Contains(s, want) {
			t.Errorf("report misses %q", want)
		}
	}
}

func TestReportNothingScored(t *testing.T) {
	doc := &result.Document{Format: "exitctl.result/v1", Kind: result.KindResult}
	doc.Analysis.Status = "nothing_scored"
	doc.Normalize()
	s := string(render(t, doc, Options{}))
	for _, want := range []string{`class="grade-letter" id="grade"`, "Nothing to score", "None. No exceptions are configured."} {
		if !strings.Contains(s, want) {
			t.Errorf("report misses %q", want)
		}
	}
}
