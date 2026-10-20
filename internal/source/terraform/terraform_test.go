package terraform

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudowski/exitctl/internal/model"
	"github.com/cloudowski/exitctl/internal/source"
	"github.com/cloudowski/exitctl/internal/walk"
)

const repo = "example.com/acme/infra"

func scan(t *testing.T, files map[string]string) model.Facts {
	t.Helper()
	root := t.TempDir()
	for p, c := range files {
		abs := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	idx, err := walk.Walk(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	f, err := New().Detect(context.Background(), idx, source.Options{Repo: repo, Root: root})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func id(dir, addr string) string { return model.CanonicalID(repo, dir, addr) }

func node(t *testing.T, f model.Facts, nid string) model.RawNode {
	t.Helper()
	for _, n := range f.RawNodes {
		if n.ID == nid {
			return n
		}
	}
	var ids []string
	for _, n := range f.RawNodes {
		ids = append(ids, n.ID)
	}
	t.Fatalf("node %s not found; have %v", nid, ids)
	return model.RawNode{}
}

func hasNode(f model.Facts, nid string) bool {
	for _, n := range f.RawNodes {
		if n.ID == nid {
			return true
		}
	}
	return false
}

func edge(f model.Facts, from, to, attr string) *model.RawEdge {
	for i, e := range f.RawEdges {
		if e.From == from && e.To == to && e.Attribute == attr {
			return &f.RawEdges[i]
		}
	}
	return nil
}

func source1(t *testing.T, f model.Facts, p string) model.Source {
	t.Helper()
	for _, s := range f.Sources {
		if s.Path == p {
			return s
		}
	}
	t.Fatalf("source %s not found in %v", p, f.Sources)
	return model.Source{}
}

const mainTF = `resource "aws_dynamodb_table" "orders" {
  name         = "orders"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "id"
  read_capacity = 5

  attribute {
    name = "id"
    type = "S"
  }
  attribute {
    name = "sk"
    type = "S"
  }
  point_in_time_recovery {
    enabled = true
  }
  dynamic "ttl" {
    for_each = var.ttl
    content {
      enabled = true
    }
  }
  tags = var.tags
}

data "aws_iam_policy_document" "read" {
  statement {
    actions   = ["dynamodb:GetItem"]
    resources = [aws_dynamodb_table.orders.arn]
  }
}

resource "aws_lambda_function" "api" {
  function_name = "api"
  role          = aws_iam_role.lambda.arn
  environment {
    variables = {
      TABLE = aws_dynamodb_table.orders.name
    }
  }
  policy     = data.aws_iam_policy_document.read.json
  password   = "hunter2xx"
  depends_on = [aws_dynamodb_table.orders]
}

resource "aws_s3_bucket" "logs" {
  count  = 2
  bucket = "logs-bucket"
}

resource "aws_sqs_queue" "jobs" {
  for_each = toset(["a", "b"])
  name     = "jobs-${each.key}"
}
`

func TestResourcesAndAttributes(t *testing.T) {
	f := scan(t, map[string]string{"infra/main.tf": mainTF})

	tbl := node(t, f, id("infra", "aws_dynamodb_table.orders"))
	if tbl.Type != "aws_dynamodb_table" || tbl.NativeAddress != "aws_dynamodb_table.orders" ||
		tbl.SourceKind != model.KindTerraform || tbl.NodeKind != model.NodeResource {
		t.Fatalf("bad node %+v", tbl)
	}
	if tbl.Multiplicity != model.MultOne {
		t.Errorf("multiplicity = %s", tbl.Multiplicity)
	}
	if tbl.ProviderIdentity != "aws:aws_dynamodb_table/orders" {
		t.Errorf("identity = %q", tbl.ProviderIdentity)
	}
	if len(tbl.Evidence) != 1 || tbl.Evidence[0].Line != 1 || tbl.Evidence[0].Path != "infra/main.tf" ||
		tbl.Evidence[0].Excerpt != `resource "aws_dynamodb_table" "orders" {` || tbl.Evidence[0].Repo != repo {
		t.Errorf("evidence = %+v", tbl.Evidence)
	}
	a := tbl.Attributes
	if a["billing_mode"] != "PAY_PER_REQUEST" || a["read_capacity"] != int64(5) {
		t.Errorf("literals: %v", a)
	}
	if !model.IsUnknown(a["tags"]) {
		t.Errorf("tags should be unknown: %v", a["tags"])
	}
	if !model.IsUnknown(a["ttl"]) {
		t.Errorf("dynamic ttl should be unknown: %v", a["ttl"])
	}
	attrsList, ok := a["attribute"].([]any)
	if !ok || len(attrsList) != 2 || attrsList[1].(map[string]any)["name"] != "sk" {
		t.Errorf("repeated block: %#v", a["attribute"])
	}
	pitr, ok := a["point_in_time_recovery"].(map[string]any)
	if !ok || pitr["enabled"] != true {
		t.Errorf("single block: %#v", a["point_in_time_recovery"])
	}

	data := node(t, f, id("infra", "data.aws_iam_policy_document.read"))
	if data.Type != "aws_iam_policy_document" || data.NativeAddress != "data.aws_iam_policy_document.read" {
		t.Errorf("data node %+v", data)
	}

	fn := node(t, f, id("infra", "aws_lambda_function.api"))
	if !model.IsRedacted(fn.Attributes["password"]) {
		t.Errorf("password not redacted: %v", fn.Attributes["password"])
	}
	if _, ok := fn.Attributes["depends_on"]; ok {
		t.Error("depends_on must not be an attribute")
	}
	if fn.ProviderIdentity != "aws:aws_lambda_function/api" {
		t.Errorf("identity = %q", fn.ProviderIdentity)
	}
	if !model.IsUnknown(fn.Attributes["role"]) {
		t.Errorf("role should be unknown")
	}

	logs := node(t, f, id("infra", "aws_s3_bucket.logs"))
	if logs.Multiplicity != model.MultUnknown || logs.Attributes["bucket"] != "logs-bucket" {
		t.Errorf("count node %+v", logs)
	}
	if _, ok := logs.Attributes["count"]; ok {
		t.Error("count must not be an attribute")
	}
	if logs.ProviderIdentity != "aws:aws_s3_bucket/logs-bucket" {
		t.Errorf("identity = %q", logs.ProviderIdentity)
	}
	jobs := node(t, f, id("infra", "aws_sqs_queue.jobs"))
	if jobs.Multiplicity != model.MultUnknown || !model.IsUnknown(jobs.Attributes["name"]) || jobs.ProviderIdentity != "" {
		t.Errorf("for_each node %+v", jobs)
	}

	s := source1(t, f, "infra/main.tf")
	if s.Status != model.StatusParsed || s.Kind != model.KindTerraform {
		t.Errorf("source %+v", s)
	}
}

func TestReferences(t *testing.T) {
	f := scan(t, map[string]string{"infra/main.tf": mainTF})
	tbl := id("infra", "aws_dynamodb_table.orders")
	fn := id("infra", "aws_lambda_function.api")
	data := id("infra", "data.aws_iam_policy_document.read")

	cases := []struct {
		from, to, attr, hint string
		line                 int
	}{
		{data, tbl, "statement.resources", "", 30},
		{fn, tbl, "environment.variables", "", 39},
		{fn, data, "policy", "", 42},
		{fn, tbl, "depends_on", "ordering", 44},
		{fn, id("infra", "aws_iam_role.lambda"), "role", "", 36}, // unresolved target still emitted
	}
	for _, c := range cases {
		e := edge(f, c.from, c.to, c.attr)
		if e == nil {
			t.Errorf("missing edge %s -> %s (%s); have %+v", c.from, c.to, c.attr, f.RawEdges)
			continue
		}
		if e.KindHint != c.hint || e.Evidence[0].Line != c.line || e.Evidence[0].Path != "infra/main.tf" {
			t.Errorf("edge %s -> %s: hint=%q evidence=%+v", c.from, c.to, e.KindHint, e.Evidence)
		}
	}
	for _, e := range f.RawEdges {
		if e.Attribute == "tags" || e.Attribute == "ttl" {
			t.Errorf("unexpected edge from var reference: %+v", e)
		}
	}
	// The redacted line must not leak the password in evidence.
	for _, n := range f.RawNodes {
		for _, ev := range n.Evidence {
			if ev.Excerpt == `password   = "hunter2xx"` {
				t.Error("password leaked")
			}
		}
	}
}

func TestLocalModules(t *testing.T) {
	f := scan(t, map[string]string{
		"envs/prod/main.tf": `locals {
  bucket_arn = aws_s3_bucket.assets.arn
}
resource "aws_s3_bucket" "assets" {
  bucket = "assets"
}
module "db" {
  source = "../../modules/db"
  bucket = local.bucket_arn
}
module "db2" {
  source = "../../modules/db"
}
resource "aws_lambda_function" "fn" {
  function_name = "fn"
  environment {
    variables = { T = module.db.table_name }
  }
}
`,
		"modules/db/main.tf": `resource "aws_dynamodb_table" "orders" {
  name = "orders"
}
module "inner" {
  source = "./inner"
}
`,
		"modules/db/inner/main.tf": `resource "aws_kms_key" "k" {
  description = "k"
}
`,
	})
	if hasNode(f, id("modules/db", "aws_dynamodb_table.orders")) || hasNode(f, id("modules/db/inner", "aws_kms_key.k")) {
		t.Error("module directories must not be roots")
	}
	n := node(t, f, id("envs/prod", "module.db.aws_dynamodb_table.orders"))
	if n.NativeAddress != "module.db.aws_dynamodb_table.orders" || n.Evidence[0].Path != "modules/db/main.tf" {
		t.Errorf("module node %+v", n)
	}
	node(t, f, id("envs/prod", "module.db2.aws_dynamodb_table.orders"))
	node(t, f, id("envs/prod", "module.db.module.inner.aws_kms_key.k"))
	node(t, f, id("envs/prod", "module.db2.module.inner.aws_kms_key.k"))

	fn := id("envs/prod", "aws_lambda_function.fn")
	for _, to := range []string{
		id("envs/prod", "module.db.aws_dynamodb_table.orders"),
		id("envs/prod", "module.db.module.inner.aws_kms_key.k"),
	} {
		e := edge(f, fn, to, "environment.variables")
		if e == nil || e.Evidence[0].Line != 17 {
			t.Errorf("missing module output edge to %s: %+v", to, e)
		}
	}
	if edge(f, fn, id("envs/prod", "module.db2.aws_dynamodb_table.orders"), "environment.variables") != nil {
		t.Error("edge to wrong module")
	}
	// Input wiring through a local: every resource in module.db -> assets.
	assets := id("envs/prod", "aws_s3_bucket.assets")
	e := edge(f, id("envs/prod", "module.db.aws_dynamodb_table.orders"), assets, "module.db.bucket")
	if e == nil || e.Evidence[0].Line != 9 || e.Evidence[0].Path != "envs/prod/main.tf" {
		t.Errorf("module input edge: %+v (all: %+v)", e, f.RawEdges)
	}
	if edge(f, id("envs/prod", "module.db.module.inner.aws_kms_key.k"), assets, "module.db.bucket") == nil {
		t.Error("module input edge for nested resource missing")
	}
	for _, p := range []string{"envs/prod/main.tf", "modules/db/main.tf", "modules/db/inner/main.tf"} {
		if s := source1(t, f, p); s.Status != model.StatusParsed {
			t.Errorf("source %s: %+v", p, s)
		}
	}
	if len(f.Sources) != 3 {
		t.Errorf("sources should be unique per file: %+v", f.Sources)
	}
	if len(f.Gaps) != 0 {
		t.Errorf("gaps: %+v", f.Gaps)
	}
}

func TestRemoteModules(t *testing.T) {
	f := scan(t, map[string]string{
		"main.tf": `module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "5.0.0"
}
module "rds" {
  source = "git::https://example.com/rds.git"
}
`,
		".terraform/modules/modules.json": `{"Modules":[{"Key":"","Source":"","Dir":"."},
{"Key":"vpc","Source":"registry.terraform.io/terraform-aws-modules/vpc/aws","Dir":".terraform/modules/vpc"}]}`,
		".terraform/modules/vpc/main.tf": `resource "aws_vpc" "this" {
  cidr_block = "10.0.0.0/16"
}
`,
	})
	n := node(t, f, id("", "module.vpc.aws_vpc.this"))
	if n.Evidence[0].Path != ".terraform/modules/vpc/main.tf" || n.Attributes["cidr_block"] != "10.0.0.0/16" {
		t.Errorf("vendored node %+v", n)
	}
	if hasNode(f, id(".terraform/modules/vpc", "aws_vpc.this")) {
		t.Error(".terraform directories must not be roots")
	}
	if len(f.Gaps) != 1 {
		t.Fatalf("gaps: %+v", f.Gaps)
	}
	g := f.Gaps[0]
	if g.Kind != model.GapUnresolvedModule || g.Subject != "git::https://example.com/rds.git" ||
		g.Path != "main.tf" || g.Count != 1 || g.Repo != repo {
		t.Errorf("gap %+v", g)
	}
}

func TestBrokenFile(t *testing.T) {
	f := scan(t, map[string]string{
		"a/broken.tf":  "}}} this is not ] HCL [[[",
		"a/partial.tf": `resource "aws_s3_bucket" "x" {{{ = = ]`,
		"a/good.tf": `resource "aws_s3_bucket" "y" {
  bucket = "y"
}
`,
		"b/main.tofu": `resource "aws_sns_topic" "t" {
  name = "t"
}
`,
	})
	s := source1(t, f, "a/broken.tf")
	if s.Status != model.StatusFailed || s.Note != noteFailed {
		t.Errorf("broken source %+v", s)
	}
	if s := source1(t, f, "a/partial.tf"); s.Status != model.StatusPartial || s.Note != notePartial {
		t.Errorf("partial source %+v", s)
	}
	if source1(t, f, "a/good.tf").Status != model.StatusParsed {
		t.Error("good file should parse")
	}
	node(t, f, id("a", "aws_s3_bucket.y"))
	n := node(t, f, id("b", "aws_sns_topic.t"))
	if n.ProviderIdentity != "aws:aws_sns_topic/t" {
		t.Errorf("tofu node %+v", n)
	}
}

func TestDeterministic(t *testing.T) {
	files := map[string]string{"infra/main.tf": mainTF}
	a := scan(t, files)
	b := scan(t, files)
	if len(a.RawEdges) != len(b.RawEdges) || len(a.RawNodes) != len(b.RawNodes) {
		t.Fatal("non-deterministic counts")
	}
	for i := range a.RawEdges {
		if a.RawEdges[i].From != b.RawEdges[i].From || a.RawEdges[i].To != b.RawEdges[i].To ||
			a.RawEdges[i].Attribute != b.RawEdges[i].Attribute {
			t.Fatal("non-deterministic edges")
		}
	}
}
