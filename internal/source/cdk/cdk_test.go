package cdk

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/cloudowski/exitctl/internal/model"
	"github.com/cloudowski/exitctl/internal/source"
	"github.com/cloudowski/exitctl/internal/source/packages"
	"github.com/cloudowski/exitctl/internal/walk"
)

const repo = "github.com/acme/infra"

func detect(t *testing.T, files map[string]string) []model.RawNode {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	idx, err := walk.Walk(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	opts := source.Options{Repo: repo, Root: root}
	f := source.RunAll(context.Background(), []source.Detector{packages.NewWith(nil)}, []source.Detector{New()}, idx, opts)
	var out []model.RawNode
	for _, n := range f.RawNodes {
		if n.SourceKind == model.KindCDK {
			out = append(out, n)
		}
	}
	return out
}

func summary(ns []model.RawNode) []string {
	var out []string
	for _, n := range ns {
		out = append(out, n.Type+" "+n.NativeAddress+" "+n.Evidence[0].Path)
	}
	sort.Strings(out)
	return out
}

func same(t *testing.T, got []model.RawNode, want ...string) {
	t.Helper()
	g := summary(got)
	sort.Strings(want)
	if len(g) != len(want) {
		t.Fatalf("got %q\nwant %q", g, want)
	}
	for i := range g {
		if g[i] != want[i] {
			t.Errorf("got %q, want %q", g[i], want[i])
		}
	}
}

func TestTypeScript(t *testing.T) {
	ns := detect(t, map[string]string{
		"package.json": `{"dependencies": {"aws-cdk-lib": "2.100.0"}}`,
		"lib/stack.ts": `import * as cdk from 'aws-cdk-lib';
import * as s3 from 'aws-cdk-lib/aws-s3';
import { Queue } from 'aws-cdk-lib/aws-sqs';
import { aws_dynamodb as ddb, aws_lambda } from 'aws-cdk-lib';
import * as deploy from 'aws-cdk-lib/aws-s3-deployment';

export class S extends cdk.Stack {
  constructor(scope: Construct, id: string) {
    super(scope, id);
    const b = new s3.Bucket(this, 'Assets', { versioned: true });
    // const x = new s3.Bucket(this, 'Commented');
    const q = new Queue(this, "Jobs");
    new ddb.Table(this, 'Orders', {});
    new aws_lambda.Function(this, 'Handler', {});
    new cdk.aws_sns.Topic(this, 'Alerts');
    new deploy.BucketDeployment(this, 'Deploy', {});
    new s3.CfnBucket(this, 'Raw');
    new Date(this, 'NotCdk');
  }
}
`,
	})
	same(t, ns,
		"AWS::S3::Bucket s3.Bucket.Assets lib/stack.ts",
		"AWS::SQS::Queue sqs.Queue.Jobs lib/stack.ts",
		"AWS::DynamoDB::Table dynamodb.Table.Orders lib/stack.ts",
		"AWS::Lambda::Function lambda.Function.Handler lib/stack.ts",
		"AWS::SNS::Topic sns.Topic.Alerts lib/stack.ts",
		"AWS::S3::Bucket s3.CfnBucket.Raw lib/stack.ts",
	)
	for _, n := range ns {
		if n.NativeAddress == "s3.Bucket.Assets" && (n.Evidence[0].Line != 10 || n.NodeKind != model.NodeResource ||
			n.ID != repo+"//lib/stack.ts#s3.Bucket.Assets") {
			t.Errorf("node = %+v", n)
		}
	}
}

func TestPython(t *testing.T) {
	ns := detect(t, map[string]string{
		"requirements.txt": "aws-cdk-lib==2.100.0\nconstructs\n",
		"app/stack.py": `from aws_cdk import (
    Stack,
    aws_s3 as s3,
    aws_rds as rds,
)
from aws_cdk.aws_sqs import Queue
import aws_cdk.aws_ec2 as ec2


class S(Stack):
    def __init__(self, scope, id):
        super().__init__(scope, id)
        s3.Bucket(self, "Assets")
        # s3.Bucket(self, "Commented")
        Queue(self, "Jobs")
        self.vpc = ec2.Vpc(self, "Net", max_azs=2)
        rds.DatabaseInstance(self, "Db", engine=None)
        print("hello", "world")
`,
	})
	same(t, ns,
		"AWS::S3::Bucket s3.Bucket.Assets app/stack.py",
		"AWS::SQS::Queue sqs.Queue.Jobs app/stack.py",
		"AWS::EC2::VPC ec2.Vpc.Net app/stack.py",
		"AWS::RDS::DBInstance rds.DatabaseInstance.Db app/stack.py",
	)
}

func TestJavaAndGo(t *testing.T) {
	ns := detect(t, map[string]string{
		"pom.xml": `<project><dependencies><dependency><groupId>software.amazon.awscdk</groupId><artifactId>aws-cdk-lib</artifactId><version>2.1</version></dependency></dependencies></project>`,
		"src/main/java/App.java": `import software.amazon.awscdk.services.s3.Bucket;
import software.amazon.awscdk.services.dynamodb.*;

class App {
  void f() {
    Bucket.Builder.create(this, "Assets").versioned(true).build();
    new Table(this, "Orders");
  }
}
`,
		"go/go.mod": "module x\n\ngo 1.22\n\nrequire github.com/aws/aws-cdk-go/awscdk/v2 v2.100.0\n",
		"go/main.go": `package main

import (
	"github.com/aws/aws-cdk-go/awscdk/v2/awssqs"
)

func f() {
	awssqs.NewQueue(stack, jsii.String("Jobs"), nil)
}
`,
	})
	same(t, ns,
		"AWS::S3::Bucket s3.Bucket.Assets src/main/java/App.java",
		"AWS::DynamoDB::Table dynamodb.Table.Orders src/main/java/App.java",
		"AWS::SQS::Queue sqs.Queue.Jobs go/main.go",
	)
}

func TestNotCDK(t *testing.T) {
	ns := detect(t, map[string]string{
		"package.json": `{"dependencies": {"express": "4"}}`,
		"a.ts":         "import * as s3 from 'aws-cdk-lib/aws-s3';\nnew s3.Bucket(this, 'X');\n",
	})
	if len(ns) != 0 {
		t.Fatalf("nodes without a CDK dependency: %v", summary(ns))
	}
}

func TestDuplicateIDs(t *testing.T) {
	ns := detect(t, map[string]string{
		"package.json": `{"dependencies": {"aws-cdk-lib": "2"}}`,
		"a.ts": `import * as s3 from 'aws-cdk-lib/aws-s3';
new s3.Bucket(this, 'X');
new s3.Bucket(this, 'X');
`,
	})
	ids := map[string]bool{}
	for _, n := range ns {
		ids[n.ID] = true
	}
	if len(ns) != 2 || len(ids) != 2 {
		t.Fatalf("want two distinct nodes, got %v", summary(ns))
	}
}

func TestPythonMultilineCall(t *testing.T) {
	ns := detect(t, map[string]string{
		"requirements.txt": "aws-cdk-lib==2.100.0\n",
		"stack.py": `from aws_cdk import aws_s3 as s3

s3.Bucket(
    self,
    "Assets",
    versioned=True,
)
`,
	})
	same(t, ns, "AWS::S3::Bucket s3.Bucket.Assets stack.py")
	if ns[0].Evidence[0].Line != 3 {
		t.Errorf("evidence line = %d, want 3", ns[0].Evidence[0].Line)
	}
}
