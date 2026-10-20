package pulumi

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

func detect(t *testing.T, files map[string]string) model.Facts {
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
	return source.RunAll(context.Background(),
		[]source.Detector{packages.NewWith(nil), NewYAML()}, []source.Detector{New()}, idx, opts)
}

func same(t *testing.T, f model.Facts, want ...string) {
	t.Helper()
	var got []string
	for _, n := range f.RawNodes {
		if n.SourceKind == model.KindPulumi {
			got = append(got, n.Type+" "+n.NativeAddress+" "+n.Evidence[0].Path)
		}
	}
	sort.Strings(got)
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("got %q, want %q", got[i], want[i])
		}
	}
}

func TestTypeScript(t *testing.T) {
	f := detect(t, map[string]string{
		"package.json": `{"dependencies": {"@pulumi/pulumi": "3", "@pulumi/aws": "6"}}`,
		"index.ts": `import * as pulumi from "@pulumi/pulumi";
import * as aws from "@pulumi/aws";
import * as gcp from "@pulumi/gcp";
import * as ec2 from "@pulumi/aws/ec2";
import { rds } from "@pulumi/aws";

const bucket = new aws.s3.BucketV2("assets", { bucket: "x" });
// new aws.s3.Bucket("commented");
const fn = new aws.lambda.Function("handler", {});
const vpc = new ec2.Vpc("net", {});
const db = new rds.Instance("db", {});
const lb = new aws.lb.LoadBalancer("front", {});
const api = new aws.apigateway.RestApi("api", {});
const gb = new gcp.storage.Bucket("gb", {});
const cfg = new pulumi.Config("x");
const p = new aws.Provider("p", {});
`,
	})
	same(t, f,
		"aws_s3_bucket aws.s3.BucketV2.assets index.ts",
		"aws_lambda_function aws.lambda.Function.handler index.ts",
		"aws_vpc aws.ec2.Vpc.net index.ts",
		"aws_db_instance aws.rds.Instance.db index.ts",
		"aws_lb aws.lb.LoadBalancer.front index.ts",
		"aws_api_gateway_rest_api aws.apigateway.RestApi.api index.ts",
		"google_storage_bucket gcp.storage.Bucket.gb index.ts",
	)
}

func TestPython(t *testing.T) {
	f := detect(t, map[string]string{
		"requirements.txt": "pulumi>=3\npulumi-aws\n",
		"__main__.py": `import pulumi
import pulumi_aws as aws
from pulumi_aws import (
    sqs,
    iam as identity,
)

bucket = aws.s3.Bucket(
    "my-bucket",
    acl="private",
)
q = sqs.Queue("jobs")
role = identity.Role("r", assume_role_policy="{}")
fn = aws.lambda_.Function("fn", runtime="python3.12")
pulumi.export("x", bucket.id)
`,
	})
	same(t, f,
		"aws_s3_bucket aws.s3.Bucket.my-bucket __main__.py",
		"aws_sqs_queue aws.sqs.Queue.jobs __main__.py",
		"aws_iam_role aws.iam.Role.r __main__.py",
		"aws_lambda_function aws.lambda.Function.fn __main__.py",
	)
	for _, n := range f.RawNodes {
		if n.NativeAddress == "aws.s3.Bucket.my-bucket" && n.Evidence[0].Line != 8 {
			t.Errorf("multi-line call evidence line = %d, want 8", n.Evidence[0].Line)
		}
	}
}

func TestGo(t *testing.T) {
	f := detect(t, map[string]string{
		"go.mod": "module x\n\ngo 1.22\n\nrequire github.com/pulumi/pulumi/sdk/v3 v3.100.0\n",
		"main.go": `package main

import (
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/s3"
	ec "github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ec2"
)

func f(ctx *pulumi.Context) {
	s3.NewBucketV2(ctx, "assets", nil)
	ec.NewSubnet(ctx, "sn", nil)
}
`,
	})
	same(t, f,
		"aws_s3_bucket aws.s3.BucketV2.assets main.go",
		"aws_subnet aws.ec2.Subnet.sn main.go",
	)
}

func TestYAML(t *testing.T) {
	f := detect(t, map[string]string{
		"Pulumi.yaml": `name: demo
runtime: yaml
resources:
  assets:
    type: aws:s3/bucketV2:BucketV2
    properties:
      bucket: x
  jobs:
    type: aws:sqs:Queue
  net:
    type: azure-native:network:VirtualNetwork
  gb:
    type: gcp:storage/bucket:Bucket
`,
		"Pulumi.dev.yaml": "config:\n  aws:region: eu-west-1\n",
	})
	same(t, f,
		"aws_s3_bucket aws.s3.BucketV2.assets Pulumi.yaml",
		"aws_sqs_queue aws.sqs.Queue.jobs Pulumi.yaml",
		"google_storage_bucket gcp.storage.Bucket.gb Pulumi.yaml",
	)
}

func TestNotPulumi(t *testing.T) {
	f := detect(t, map[string]string{
		"package.json": `{"dependencies": {"express": "4"}}`,
		"a.ts":         "import * as aws from '@pulumi/aws';\nnew aws.s3.Bucket('x');\n",
	})
	same(t, f)
}

func TestTFType(t *testing.T) {
	for _, tc := range []struct{ provider, module, class, want string }{
		{"aws", "ec2", "Instance", "aws_instance"},
		{"aws", "ec2", "TransitGateway", "aws_ec2_transit_gateway"},
		{"aws", "s3", "BucketObjectv2", "aws_s3_object"},
		{"aws", "iam", "RolePolicyAttachment", "aws_iam_role_policy_attachment"},
		{"aws", "cloudwatch", "LogGroup", "aws_cloudwatch_log_group"},
		{"aws", "lambda_", "Function", "aws_lambda_function"},
		{"gcp", "compute", "Instance", "google_compute_instance"},
		{"gcp", "serviceaccount", "Account", "google_service_account"},
		{"azure", "core", "ResourceGroup", "azurerm_resource_group"},
		{"azure", "storage", "Account", "azurerm_storage_account"},
	} {
		got, ok := tfType(tc.provider, tc.module, tc.class)
		if !ok || got != tc.want {
			t.Errorf("tfType(%s, %s, %s) = %q, %v; want %q", tc.provider, tc.module, tc.class, got, ok, tc.want)
		}
	}
	if _, ok := tfType("kubernetes", "apps", "Deployment"); ok {
		t.Error("kubernetes must not be mapped")
	}
}
