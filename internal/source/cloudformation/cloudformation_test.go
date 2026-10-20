package cloudformation

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/cloudowski/exitctl/internal/model"
	"github.com/cloudowski/exitctl/internal/source"
	"github.com/cloudowski/exitctl/internal/walk"
)

func run(t *testing.T, files map[string]string) model.Facts {
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
	if len(idx.Files[model.KindCloudFormation]) != len(files) {
		t.Fatalf("walk classified %v", idx.Files)
	}
	f, err := New().Detect(context.Background(), idx, source.Options{Repo: "r", Root: root})
	if err != nil {
		t.Fatal(err)
	}
	f.Sort()
	return f
}

const stackYAML = `AWSTemplateFormatVersion: "2010-09-09"
Parameters:
  Env:
    Type: String
Resources:
  Table:
    Type: AWS::DynamoDB::Table
    Properties:
      TableName: orders
      BillingMode: PAY_PER_REQUEST
      StreamSpecification:
        StreamViewType: NEW_IMAGE
  Queue:
    Type: AWS::SQS::Queue
    Properties:
      QueueName: !Sub "orders-${Env}"
      DelaySeconds: 5
  Fn:
    Type: AWS::Lambda::Function
    DependsOn: [Table, Queue]
    Properties:
      Role: !GetAtt Role.Arn
      Environment:
        Variables:
          TABLE: !Ref Table
          QUEUE_URL: !Sub "https://${Queue}/${AWS::Region}/${Local}"
          STAGE: !Ref Env
          DB_PASSWORD: hunter2
          MISSING: !Ref Nowhere
      Code:
        ZipFile: !Join ["", ["a", !GetAtt [Queue, Arn]]]
  Role:
    Type: AWS::IAM::Role
    DependsOn: Table
    Properties:
      RoleName: !If [IsProd, prod, dev]
      Tags:
        - Key: env
          Value: !Ref Env
  Custom:
    Type: Custom::Thing
    Properties:
      ServiceToken: !Sub
        - "${Arn}"
        - Arn: !GetAtt Fn.Arn
`

const stackJSON = `{
	"Resources": {
		"Table": {
			"Type": "AWS::DynamoDB::Table",
			"Properties": {"TableName": "orders", "ReadCapacity": 5}
		},
		"Api": {
			"Type": "AWS::Serverless::Function",
			"DependsOn": "Table",
			"Properties": {
				"Env": {"Ref": "Table"},
				"Arn": {"Fn::GetAtt": "Table.Arn"}
			}
		}
	}
}
`

func TestYAMLTemplate(t *testing.T) {
	f := run(t, map[string]string{"infra/stack.yaml": stackYAML})
	p := "r//infra/stack.yaml#"
	byID := map[string]model.RawNode{}
	for _, n := range f.RawNodes {
		byID[n.ID] = n
	}
	if len(f.RawNodes) != 5 {
		t.Fatalf("nodes: %+v", f.RawNodes)
	}
	tbl := byID[p+"Table"]
	if tbl.Type != "AWS::DynamoDB::Table" || tbl.NativeAddress != "Table" || tbl.SourceKind != model.KindCloudFormation ||
		tbl.ProviderIdentity != "aws:AWS::DynamoDB::Table/orders" || tbl.Multiplicity != model.MultOne {
		t.Errorf("table %+v", tbl)
	}
	if tbl.Evidence[0].Line != 6 || tbl.Evidence[0].Excerpt != "Table:" || tbl.Evidence[0].Path != "infra/stack.yaml" {
		t.Errorf("table evidence %+v", tbl.Evidence)
	}
	if !reflect.DeepEqual(tbl.Attributes["StreamSpecification"], map[string]any{"StreamViewType": "NEW_IMAGE"}) {
		t.Errorf("nested attr %v", tbl.Attributes)
	}
	q := byID[p+"Queue"]
	if !model.IsUnknown(q.Attributes["QueueName"]) || q.Attributes["DelaySeconds"] != 5 || q.ProviderIdentity != "" {
		t.Errorf("queue %+v", q)
	}
	fn := byID[p+"Fn"]
	vars := fn.Attributes["Environment"].(map[string]any)["Variables"].(map[string]any)
	if !model.IsUnknown(vars["TABLE"]) || !model.IsRedacted(vars["DB_PASSWORD"]) || !model.IsUnknown(fn.Attributes["Role"]) {
		t.Errorf("fn attrs %v", fn.Attributes)
	}
	if !model.IsUnknown(byID[p+"Role"].Attributes["RoleName"]) {
		t.Errorf("role %v", byID[p+"Role"].Attributes)
	}
	if byID[p+"Custom"].Type != "Custom::Thing" {
		t.Errorf("custom %+v", byID[p+"Custom"])
	}

	type we struct {
		from, to, attr, hint string
		lines                []int
	}
	wants := []we{
		{"Custom", "Fn", "ServiceToken", "", []int{45}},
		{"Fn", "Queue", "Code", "", []int{31}},
		{"Fn", "Queue", "DependsOn", "ordering", []int{20}},
		{"Fn", "Queue", "Environment", "", []int{26}},
		{"Fn", "Role", "Role", "", []int{22}},
		{"Fn", "Table", "DependsOn", "ordering", []int{20}},
		{"Fn", "Table", "Environment", "", []int{25}},
		{"Role", "Table", "DependsOn", "ordering", []int{34}},
	}
	if len(f.RawEdges) != len(wants) {
		for _, e := range f.RawEdges {
			t.Logf("edge %s -> %s %s %s", e.From, e.To, e.Attribute, e.KindHint)
		}
		t.Fatalf("got %d edges", len(f.RawEdges))
	}
	for i, w := range wants {
		e := f.RawEdges[i]
		if e.From != p+w.from || e.To != p+w.to || e.Attribute != w.attr || e.KindHint != w.hint {
			t.Errorf("edge %d: %+v want %+v", i, e, w)
			continue
		}
		var lines []int
		for _, ev := range e.Evidence {
			lines = append(lines, ev.Line)
		}
		if !reflect.DeepEqual(lines, w.lines) {
			t.Errorf("edge %d lines %v want %v", i, lines, w.lines)
		}
	}
	// Parameters, pseudo parameters, and Sub-local variables are not linked;
	// unknown names are reported as unresolved references.
	// (${Local} has no Sub variable map here, so it is unresolved too.)
	if len(f.Gaps) != 2 || f.Gaps[0].Kind != model.GapUnresolvedReference || f.Gaps[0].Subject != "Local" ||
		f.Gaps[1].Subject != "Nowhere" || f.Gaps[1].Count != 1 || f.Gaps[1].Path != "infra/stack.yaml" {
		t.Errorf("gaps %+v", f.Gaps)
	}
	if len(f.Sources) != 1 || f.Sources[0].Status != model.StatusParsed {
		t.Errorf("sources %+v", f.Sources)
	}
	// Redaction in evidence excerpts never leaks the literal.
	for _, e := range f.RawEdges {
		for _, ev := range e.Evidence {
			if ev.Excerpt == "DB_PASSWORD: hunter2" {
				t.Errorf("unredacted excerpt")
			}
		}
	}
}

func TestJSONTemplate(t *testing.T) {
	f := run(t, map[string]string{"infra/stack.json": stackJSON, "other/stack.yaml": "Resources:\n  Table:\n    Type: AWS::DynamoDB::Table\n"})
	ids := []string{}
	for _, n := range f.RawNodes {
		ids = append(ids, n.ID)
	}
	want := []string{"r//infra/stack.json#Api", "r//infra/stack.json#Table", "r//other/stack.yaml#Table"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("ids %v", ids)
	}
	api, tbl := f.RawNodes[0], f.RawNodes[1]
	if api.Type != "AWS::Serverless::Function" || api.Evidence[0].Line != 7 || api.Evidence[0].Excerpt != `"Api": {` {
		t.Errorf("api %+v", api)
	}
	if tbl.Attributes["TableName"] != "orders" || tbl.Attributes["ReadCapacity"] != 5 || tbl.Evidence[0].Line != 3 {
		t.Errorf("table %+v", tbl)
	}
	if len(f.RawEdges) != 3 {
		t.Fatalf("edges %+v", f.RawEdges)
	}
	got := map[string]int{}
	for _, e := range f.RawEdges {
		if e.To != "r//infra/stack.json#Table" {
			t.Errorf("edge to %s", e.To)
		}
		got[e.Attribute+"/"+e.KindHint] = e.Evidence[0].Line
	}
	if !reflect.DeepEqual(got, map[string]int{"DependsOn/ordering": 9, "Env/": 11, "Arn/": 12}) {
		t.Errorf("edges %v", got)
	}
}

func TestJSONFallback(t *testing.T) {
	// Tab-indented JSON with a duplicate key is rejected by the YAML parser.
	src := "{\n\t\"Resources\": {\n\t\t\"Q\": {\"Type\": \"AWS::SQS::Queue\"},\n\t\t\"Q\": {\"Type\": \"AWS::SQS::Queue\"}\n\t}\n}\n"
	n, err := parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	res := get(n, "Resources")
	if res == nil || len(res.Content) != 4 || res.Content[0].Line != 3 || res.Content[2].Line != 4 {
		t.Fatalf("resources %+v", res)
	}
}

func TestBrokenTemplate(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "bad.yaml"), []byte("Resources: [\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	idx := walk.Index{Root: root, Files: map[model.SourceKind][]string{model.KindCloudFormation: {"bad.yaml"}}}
	f, err := New().Detect(context.Background(), idx, source.Options{Repo: "r", Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Sources) != 1 || f.Sources[0].Status != model.StatusFailed || f.Sources[0].Note == "" || len(f.RawNodes) != 0 {
		t.Errorf("%+v", f)
	}
}
