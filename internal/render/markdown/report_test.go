package markdown

import (
	"bytes"
	"fmt"
	"path/filepath"
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

func renderDoc(t *testing.T, doc *result.Document, o Options) string {
	t.Helper()
	var buf bytes.Buffer
	if err := Render(&buf, doc, o); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

var sectionOrder = []string{
	"## Executive Summary",
	"## Dimensions",
	"## Biggest lever",
	"## Anchors",
	"## Dependents of top anchors",
	"## Alternatives",
	"## Coverage",
	"## Exceptions",
	ExitReadinessLine,
	"Support: ",
	"exitctl ",
}

func TestReportGolden(t *testing.T) {
	opts := Options{SupportEmail: "support@example.com", Names: map[string]string{
		"aws.dynamodb": "Amazon DynamoDB",
		"aws.iam":      "AWS Identity and Access Management (IAM)",
	}}
	out := testutil.RunTwice(t, func() []byte {
		return []byte(renderDoc(t, loadShop(t), opts))
	})
	testutil.Golden(t, "report/shop-aws.md", out)

	s := string(out)
	pos := 0
	for _, sec := range sectionOrder {
		i := strings.Index(s[pos:], sec)
		if i < 0 {
			t.Fatalf("section %q missing or out of order", sec)
		}
		pos += i + len(sec)
	}
	if strings.ContainsAny(s, "\u2014\u2013") {
		t.Error("report contains an em or en dash")
	}
	a := loadShop(t).Analysis
	grade := fmt.Sprintf("**Grade %s** - lock-in index **%d/100**", a.Grade, *a.Index)
	for _, want := range []string{grade, "| economic | not assessed |", "```text"} {
		if !strings.Contains(s, want) {
			t.Errorf("report lacks %q", want)
		}
	}
}

func TestReportOmitsEmptySupportEmail(t *testing.T) {
	s := renderDoc(t, loadShop(t), Options{})
	if strings.Contains(s, "Support:") {
		t.Error("support line shown without an email")
	}
	if !strings.Contains(s, ExitReadinessLine) {
		t.Error("Exit Readiness line missing")
	}
}

func TestReportNoRawErrors(t *testing.T) {
	doc := loadShop(t)
	const raw = "hcl: Unexpected token at infra/broken.tf:3,7: Argument or block definition required"
	doc.Facts.Sources = append(doc.Facts.Sources, model.Source{
		Repo: "shop-aws", Path: "infra/broken.tf", Kind: model.KindTerraform,
		Status: model.StatusFailed, Note: "could not be parsed",
	})
	doc.Facts.Gaps = append(doc.Facts.Gaps, model.Gap{
		Kind: model.GapParseFailure, Subject: raw, Repo: "shop-aws", Path: "infra/broken.tf", Count: 1,
	})
	s := renderDoc(t, doc, Options{})
	for _, bad := range []string{raw, "Unexpected token", "hcl:"} {
		if strings.Contains(s, bad) {
			t.Errorf("report contains raw error text %q", bad)
		}
	}
	if !strings.Contains(s, "`infra/broken.tf` (terraform): could not be parsed") {
		t.Errorf("failed source not reported neutrally:\n%s", s)
	}
}

func TestReportEscapesPipes(t *testing.T) {
	doc := loadShop(t)
	doc.Analysis.Anchors[0].Alternatives[0].Notes = "a | b"
	s := renderDoc(t, doc, Options{})
	if !strings.Contains(s, `a \| b`) {
		t.Error("pipe in table cell not escaped")
	}
}

func TestReportWithheldAndNothingScored(t *testing.T) {
	doc := loadShop(t)
	doc.Analysis.Confidence = "withheld"
	doc.Analysis.Grade = ""
	doc.Analysis.Range = &result.Range{Lower: 40, Upper: 77}
	doc.Analysis.Coverage.Weighted = 0.62
	s := renderDoc(t, doc, Options{})
	if !strings.Contains(s, "**No grade** - lock-in index range **40-77**") || strings.Contains(s, "Grade ") {
		t.Errorf("withheld grade not rendered as expected:\n%s", s[:min(len(s), 800)])
	}

	doc = loadShop(t)
	doc.Analysis.Status = "nothing_scored"
	doc.Analysis.Index = nil
	doc.Analysis.Grade = ""
	doc.Analysis.Anchors = nil
	doc.Analysis.Lever = nil
	s = renderDoc(t, doc, Options{})
	if !strings.Contains(s, "**Nothing to score**") {
		t.Error("nothing_scored not rendered")
	}
}
