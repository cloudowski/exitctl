package terminal

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudowski/exitctl/internal/result"
	"github.com/cloudowski/exitctl/internal/testutil"
)

func loadScanDoc(t *testing.T, name string) *result.Document {
	t.Helper()
	doc, err := result.Load(filepath.Join(testutil.RepoRoot(t), "testdata", "golden", "scan", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func renderTree(t *testing.T, doc *result.Document, to TreeOptions, o Options) []byte {
	t.Helper()
	return testutil.RunTwice(t, func() []byte {
		var buf bytes.Buffer
		if err := Tree(&buf, doc, to, o); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	})
}

func nodeIDByAddress(t *testing.T, doc *result.Document, address string) string {
	t.Helper()
	for _, n := range doc.Graph.Nodes {
		if n.NativeAddress == address {
			return n.ID
		}
	}
	t.Fatalf("node %s not found", address)
	return ""
}

func TestTreeGolden(t *testing.T) {
	utf8 := Options{UTF8: true, Width: 80}
	ascii := Options{Width: 80}
	for _, name := range []string{"shop-aws", "cycles"} {
		doc := loadScanDoc(t, name)
		t.Run(name, func(t *testing.T) {
			testutil.Golden(t, "graph/"+name+".tree.txt", renderTree(t, doc, TreeOptions{}, utf8))
			testutil.Golden(t, "graph/"+name+".tree.ascii.txt", renderTree(t, doc, TreeOptions{}, ascii))
			testutil.Golden(t, "graph/"+name+".tree.expand.txt", renderTree(t, doc, TreeOptions{Expand: true}, utf8))
			testutil.Golden(t, "graph/"+name+".tree.waves.txt", renderTree(t, doc, TreeOptions{Waves: true}, utf8))
		})
	}
}

func TestTreeBlastRadius(t *testing.T) {
	doc := loadScanDoc(t, "shop-aws")
	id := nodeIDByAddress(t, doc, "aws_dynamodb_table.orders")
	got := renderTree(t, doc, TreeOptions{BlastRadius: id}, Options{UTF8: true, Width: 80})
	testutil.Golden(t, "graph/shop-aws.blast-orders.txt", got)
	if !strings.HasPrefix(string(got), "Blast radius of aws_dynamodb_table.orders") {
		t.Errorf("missing header:\n%s", got)
	}
	gotW := renderTree(t, doc, TreeOptions{BlastRadius: id, Waves: true}, Options{Width: 80})
	testutil.Golden(t, "graph/shop-aws.blast-orders.waves.ascii.txt", gotW)

	// The display address resolves to the same node.
	byAddr := renderTree(t, doc, TreeOptions{BlastRadius: "aws_dynamodb_table.orders"}, Options{UTF8: true, Width: 80})
	if !bytes.Equal(got, byAddr) {
		t.Errorf("address and ID render differently:\n%s\n---\n%s", got, byAddr)
	}

	var buf bytes.Buffer
	if err := Tree(&buf, doc, TreeOptions{BlastRadius: "nope"}, Options{}); err == nil {
		t.Error("expected error for unknown resource")
	}
}

func groupedDoc() *result.Document {
	d := &result.Document{}
	d.Graph.Nodes = []result.Node{
		{ID: "n//#aws_instance.app", Kind: "resource", Type: "aws_instance", NativeAddress: "aws_instance.app", Wave: 3},
		{ID: "n//#aws_subnet.a", Kind: "resource", Type: "aws_subnet", NativeAddress: "aws_subnet.a", Wave: 2},
		{ID: "n//#aws_subnet.b", Kind: "resource", Type: "aws_subnet", NativeAddress: "aws_subnet.b", Wave: 2},
		{ID: "n//#aws_subnet.c", Kind: "resource", Type: "aws_subnet", NativeAddress: "aws_subnet.c", Wave: 2},
		{ID: "n//#aws_vpc.main", Kind: "resource", Type: "aws_vpc", NativeAddress: "aws_vpc.main", Wave: 1},
	}
	for _, s := range []string{"a", "b", "c"} {
		d.Graph.Edges = append(d.Graph.Edges,
			result.Edge{From: "n//#aws_instance.app", To: "n//#aws_subnet." + s},
			result.Edge{From: "n//#aws_subnet." + s, To: "n//#aws_vpc.main"},
		)
	}
	d.Graph.Groups = []result.Group{
		{ID: "grp-subnets", Kind: "type", Label: "aws_subnet x 3",
			Members: []string{"n//#aws_subnet.a", "n//#aws_subnet.b", "n//#aws_subnet.c"}},
		{ID: "grp-module", Kind: "module", Label: "module.net",
			Members: []string{"n//#aws_subnet.a", "n//#aws_vpc.main"}},
	}
	return d
}

func TestTreeTypeGroupCollapse(t *testing.T) {
	d := groupedDoc()
	o := Options{Width: 80}
	got := string(renderTree(t, d, TreeOptions{Waves: true}, o))
	want := "Dependency tree (5 nodes, 6 edges)\n" +
		"aws_instance.app [wave 3]\n" +
		"`-- aws_subnet x 3 [wave 2]\n" +
		"    `-- aws_vpc.main [wave 1]\n"
	if got != want {
		t.Errorf("grouped tree:\n%s\nwant:\n%s", got, want)
	}

	got = string(renderTree(t, d, TreeOptions{Expand: true}, o))
	want = "Dependency tree (5 nodes, 6 edges)\n" +
		"aws_instance.app\n" +
		"|-- aws_subnet.a\n" +
		"|   `-- aws_vpc.main\n" +
		"|-- aws_subnet.b\n" +
		"|   `-- aws_vpc.main\n" +
		"`-- aws_subnet.c\n" +
		"    `-- aws_vpc.main\n"
	if got != want {
		t.Errorf("expanded tree:\n%s\nwant:\n%s", got, want)
	}
}

func TestTreeRepeatedSubtreeAndCycle(t *testing.T) {
	d := &result.Document{}
	d.Graph.Nodes = []result.Node{
		{ID: "a", Kind: "resource", NativeAddress: "a"},
		{ID: "b", Kind: "resource", NativeAddress: "b"},
		{ID: "c", Kind: "resource", NativeAddress: "c"},
		{ID: "d", Kind: "resource", NativeAddress: "d"},
		{ID: "x", Kind: "resource", NativeAddress: "x"},
		{ID: "y", Kind: "resource", NativeAddress: "y"},
	}
	d.Graph.Edges = []result.Edge{
		{From: "a", To: "b"}, {From: "a", To: "c"},
		{From: "b", To: "d"}, {From: "c", To: "b"},
		{From: "x", To: "y"}, {From: "y", To: "x"}, // root-less cycle
	}
	got := string(renderTree(t, d, TreeOptions{}, Options{}))
	want := "Dependency tree (6 nodes, 6 edges)\n" +
		"a\n" +
		"|-- b\n" +
		"|   `-- d\n" +
		"`-- c\n" +
		"    `-- b (see above)\n" +
		"x\n" +
		"`-- y\n" +
		"    `-- x (see above)\n"
	if got != want {
		t.Errorf("tree:\n%s\nwant:\n%s", got, want)
	}
}
