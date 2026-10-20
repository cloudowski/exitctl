package graphviz

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudowski/exitctl/internal/result"
	"github.com/cloudowski/exitctl/internal/testutil"
)

func renderDOT(t *testing.T, doc *result.Document, opts Options) []byte {
	t.Helper()
	return testutil.RunTwice(t, func() []byte {
		var buf bytes.Buffer
		if err := DOT(&buf, doc, opts); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	})
}

func TestDOTShopAWS(t *testing.T) {
	doc, err := result.Load(filepath.Join(testutil.RepoRoot(t), "testdata", "golden", "scan", "shop-aws.json"))
	if err != nil {
		t.Fatal(err)
	}
	got := renderDOT(t, doc, Options{Waves: true})
	testutil.Golden(t, "graph/shop-aws.dot", got)
}

// groupedDoc has a type group of three subnets nested in a module container,
// and a nested namespace-like container to exercise cluster nesting.
func groupedDoc() *result.Document {
	return &result.Document{Graph: result.Graph{
		Nodes: []result.Node{
			{ID: "r#aws_vpc.main", Kind: "resource", Type: "aws_vpc", NativeAddress: "aws_vpc.main", Risk: 0.225, Wave: 1, Group: "mod-net"},
			{ID: "r#aws_subnet.c", Kind: "resource", Type: "aws_subnet", NativeAddress: "aws_subnet.c", Risk: 0.225, Wave: 2, Group: "t-subnet"},
			{ID: "r#aws_subnet.a", Kind: "resource", Type: "aws_subnet", NativeAddress: "aws_subnet.a", Risk: 0.3, Wave: 2, Group: "t-subnet"},
			{ID: "r#aws_subnet.b", Kind: "resource", Type: "aws_subnet", NativeAddress: "aws_subnet.b", Risk: 0.225, Wave: 3, Group: "t-subnet"},
			{ID: "r#aws_db.main", Kind: "resource", Type: "aws_db_instance", NativeAddress: `aws_db."main"\x`, Risk: 0.9, Wave: 4, Group: "mod-db"},
			{ID: "r#svc", Kind: "component", NativeAddress: "services/api", Wave: 5},
			{ID: "implied//aws.s3", Kind: "implied_service", Service: "aws.s3", Risk: 0.5, Wave: 1},
		},
		Edges: []result.Edge{
			{From: "r#aws_subnet.a", To: "r#aws_vpc.main", Kind: "reference"},
			{From: "r#aws_subnet.b", To: "r#aws_vpc.main", Kind: "reference"},
			{From: "r#aws_subnet.c", To: "r#aws_vpc.main", Kind: "reference"},
			{From: "r#aws_subnet.c", To: "r#aws_vpc.main", Kind: "containment"},
			{From: "r#aws_db.main", To: "r#aws_subnet.a", Kind: "reference"},
			{From: "r#aws_db.main", To: "r#aws_subnet.b", Kind: "reference"},
			{From: "r#svc", To: "r#aws_db.main", Kind: "data-flow"},
			{From: "r#svc", To: "implied//aws.s3", Kind: "usage"},
			{From: "r#svc", To: "r#missing", Kind: "usage"},
		},
		Groups: []result.Group{
			{ID: "t-subnet", Kind: "type", Label: "aws_subnet x 3", Members: []string{"r#aws_subnet.a", "r#aws_subnet.b", "r#aws_subnet.c"}, Parent: "mod-net"},
			{ID: "mod-net", Kind: "module", Label: "module.network", Members: []string{"r#aws_vpc.main", "t-subnet"}, Parent: "mod-root"},
			{ID: "mod-db", Kind: "module", Label: "module.db", Members: []string{"r#aws_db.main"}, Parent: "mod-root"},
			{ID: "mod-root", Kind: "module", Label: "infra", Members: []string{"mod-net", "mod-db"}},
			{ID: "mod-empty", Kind: "namespace", Label: "unused"},
		},
	}}
}

func TestDOTGrouped(t *testing.T) {
	got := string(renderDOT(t, groupedDoc(), Options{Waves: true}))
	for _, want := range []string{
		`"group:t-subnet" [label="aws_subnet x 3\nrisk: moderate (0.30)\n[waves 2-3]", shape=box3d`,
		`subgraph "cluster_mod-root" {`,
		`    subgraph "cluster_mod-net" {`,
		`label="module: module.network";`,
		`"r#aws_db.main" [label="aws_db.\"main\"\\x\nrisk: critical (0.90)\n[wave 4]"`,
		`"r#aws_db.main" -> "group:t-subnet" [style=solid, tooltip="reference"];`,
		`"group:t-subnet" -> "r#aws_vpc.main" [style=dashed, tooltip="containment"];`,
		`"group:t-subnet" -> "r#aws_vpc.main" [style=solid, tooltip="reference"];`,
		`"r#svc" -> "r#aws_db.main" [style=bold, tooltip="data-flow"];`,
		`"r#svc" -> "implied//aws.s3" [style=dotted, tooltip="usage"];`,
		`label="component services/api\nrisk: none\n[wave 5]", shape=component`,
		`label="aws.s3 (used by code, not declared)\nrisk: elevated (0.50)\n[wave 1]"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s\n%s", want, got)
		}
	}
	if n := strings.Count(got, `"r#aws_db.main" -> "group:t-subnet"`); n != 1 {
		t.Errorf("collapsed edge emitted %d times, want 1", n)
	}
	for _, bad := range []string{"r#aws_subnet.a", "cluster_mod-empty", "r#missing"} {
		if strings.Contains(got, bad) {
			t.Errorf("unexpected %q in output\n%s", bad, got)
		}
	}
}

func TestDOTExpand(t *testing.T) {
	got := string(renderDOT(t, groupedDoc(), Options{Expand: true}))
	if strings.Contains(got, "subgraph") || strings.Contains(got, "group:") || strings.Contains(got, "[wave") {
		t.Errorf("expand output still grouped or labelled with waves\n%s", got)
	}
	if n := strings.Count(got, `-> "r#aws_vpc.main"`); n != 4 {
		t.Errorf("got %d edges to vpc, want 4\n%s", n, got)
	}
}

func TestQuote(t *testing.T) {
	if got, want := quote("a\"b\\c\nd"), `"a\"b\\c\nd"`; got != want {
		t.Errorf("quote = %s, want %s", got, want)
	}
}
