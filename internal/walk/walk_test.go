package walk

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cloudowski/exitctl/internal/model"
)

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const k8sDeploy = "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\n"

func buildTree(t *testing.T) string {
	root := t.TempDir()
	files := map[string]string{
		"infra/main.tf":                         `resource "aws_sqs_queue" "q" {}`,
		"infra/other.tofu":                      `resource "aws_sqs_queue" "r" {}`,
		"infra/.terraform/modules/vpc/main.tf":  `resource "aws_vpc" "v" {}`,
		"infra/.terraform/providers/x/README":   "x",
		"infra/.terraform/terraform.tfstate.tf": "ignored",
		"cfn/stack.yaml":                        "AWSTemplateFormatVersion: '2010-09-09'\nResources:\n  Q:\n    Type: AWS::SQS::Queue\n",
		"cfn/no-version.json":                   `{"Resources": {"T": {"Type": "AWS::DynamoDB::Table", "Properties": {}}}}`,
		"cfn/tags.yaml":                         "Resources:\n  B:\n    Type: AWS::S3::Bucket\n    Properties:\n      BucketName: !Ref Name\n",
		"k8s/deploy.yaml":                       k8sDeploy,
		"k8s/multi.yml":                         "a: 1\n---\n" + k8sDeploy,
		"k8s/kustomization.yaml":                "apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources: [deploy.yaml]\n",
		"k8s/values.yaml":                       "replicas: 2\n",
		"k8s/templated.yaml":                    "apiVersion: v1\nkind: ConfigMap\ndata: {{ .Values.x }}: [\n",
		"chart/Chart.yaml":                      "apiVersion: v2\nname: web\nversion: 0.1.0\n",
		"chart/values.yaml":                     "image: nginx\n",
		"chart/templates/deploy.yaml":           k8sDeploy,
		"chart/charts/sub/Chart.yaml":           "apiVersion: v2\nname: sub\n",
		"chart/charts/sub/templates/svc.yaml":   "apiVersion: v1\nkind: Service\n",
		"overlays/prod/Kustomization":           "resources: []\n",
		"Dockerfile":                            "FROM alpine\n",
		"svc/Dockerfile.prod":                   "FROM alpine\n",
		"svc/app.dockerfile":                    "FROM alpine\n",
		".github/workflows/ci.yml":              "on: push\n",
		".github/workflows/sub/nested.yml":      "on: push\n",
		".gitlab-ci.yml":                        "stages: [build]\n",
		"svc/main.go":                           "package main\n",
		"svc/go.mod":                            "module x\n",
		"web/package.json":                      `{"name":"web"}`,
		"web/src/index.ts":                      "export {}\n",
		"web/node_modules/aws-sdk/package.json": `{}`,
		"py/requirements-dev.txt":               "boto3\n",
		"py/pyproject.toml":                     "[project]\n",
		"py/app.py":                             "import boto3\n",
		"net/app.csproj":                        "<Project/>\n",
		"vendor/x/x.go":                         "package x\n",
		"build/out.js":                          "x\n",
		".git/config":                           "[core]\n",
		"README.md":                             "# hi\n",
		"ignored/thing.tf":                      "x",
		"deep/a/b/skip.tf":                      "x",
		"latin1.py":                             "x = '\xff\xfe'\n",
	}
	for rel, c := range files {
		write(t, root, rel, c)
	}
	big := strings.Repeat("# padding\n", (MaxFileSize/10)+10)
	write(t, root, "big/huge.tf", big)
	write(t, root, "big/huge.yaml", k8sDeploy+big)
	write(t, root, "big/huge.json", "{\"x\": 1}\n"+big)
	return root
}

func TestWalk(t *testing.T) {
	root := buildTree(t)
	idx, err := Walk(root, []string{"ignored/", "**/skip.tf"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[model.SourceKind][]string{
		model.KindTerraform:       {"infra/.terraform/modules/vpc/main.tf", "infra/main.tf", "infra/other.tofu"},
		model.KindCloudFormation:  {"cfn/no-version.json", "cfn/stack.yaml", "cfn/tags.yaml"},
		model.KindKubernetes:      {"k8s/deploy.yaml", "k8s/multi.yml", "k8s/templated.yaml"},
		model.KindDockerfile:      {"Dockerfile", "svc/Dockerfile.prod", "svc/app.dockerfile"},
		model.KindGitHubActions:   {".github/workflows/ci.yml"},
		model.KindGitLabCI:        {".gitlab-ci.yml"},
		model.KindCode:            {"py/app.py", "svc/main.go", "web/src/index.ts"},
		model.KindPackageManifest: {"net/app.csproj", "py/pyproject.toml", "py/requirements-dev.txt", "svc/go.mod", "web/package.json"},
	}
	if !reflect.DeepEqual(idx.Files, want) {
		for k := range want {
			if !reflect.DeepEqual(idx.Files[k], want[k]) {
				t.Errorf("kind %s:\n got %v\nwant %v", k, idx.Files[k], want[k])
			}
		}
		for k := range idx.Files {
			if _, ok := want[k]; !ok {
				t.Errorf("unexpected kind %s: %v", k, idx.Files[k])
			}
		}
	}
	if got, w := idx.HelmCharts, []string{"chart"}; !reflect.DeepEqual(got, w) {
		t.Errorf("HelmCharts = %v, want %v", got, w)
	}
	if got, w := idx.Kustomizations, []string{"k8s", "overlays/prod"}; !reflect.DeepEqual(got, w) {
		t.Errorf("Kustomizations = %v, want %v", got, w)
	}
	wantSkipped := []model.Source{
		{Path: "big/huge.tf", Kind: model.KindTerraform, Status: model.StatusSkipped, Note: NoteTooLarge},
		{Path: "big/huge.yaml", Kind: model.KindKubernetes, Status: model.StatusSkipped, Note: NoteTooLarge},
		{Path: "latin1.py", Kind: model.KindCode, Status: model.StatusSkipped, Note: NoteNotUTF8},
	}
	if !reflect.DeepEqual(idx.Skipped, wantSkipped) {
		t.Errorf("Skipped:\n got %+v\nwant %+v", idx.Skipped, wantSkipped)
	}
	if !filepath.IsAbs(idx.Root) {
		t.Errorf("Root not absolute: %s", idx.Root)
	}

	// Deterministic: a second walk is identical.
	idx2, err := Walk(root, []string{"ignored/", "**/skip.tf"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(idx, idx2) {
		t.Error("two walks differ")
	}
}

func TestWalkSymlinks(t *testing.T) {
	outside := t.TempDir()
	write(t, outside, "secret.tf", "x")
	write(t, outside, "dir/x.tf", "x")
	root := t.TempDir()
	write(t, root, "real/a.tf", "x")
	links := map[string]string{
		"real/loop":   filepath.Join(root, "real"),
		"linked":      filepath.Join(root, "real"),
		"out.tf":      filepath.Join(outside, "secret.tf"),
		"outdir":      filepath.Join(outside, "dir"),
		"dangling.tf": filepath.Join(root, "missing.tf"),
	}
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(root, filepath.FromSlash(name))); err != nil {
			t.Skipf("symlinks unsupported: %v", err)
		}
	}
	idx, err := Walk(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"linked/a.tf", "real/a.tf"}
	if got := idx.Files[model.KindTerraform]; !reflect.DeepEqual(got, want) {
		t.Errorf("terraform files = %v, want %v", got, want)
	}
	wantSkipped := []model.Source{
		{Path: "dangling.tf", Kind: model.KindTerraform, Status: model.StatusSkipped, Note: NoteSymlinkError},
		{Path: "out.tf", Kind: model.KindTerraform, Status: model.StatusSkipped, Note: NoteOutsideRoot},
	}
	if !reflect.DeepEqual(idx.Skipped, wantSkipped) {
		t.Errorf("Skipped = %+v, want %+v", idx.Skipped, wantSkipped)
	}
}

func TestWalkErrors(t *testing.T) {
	if _, err := Walk(filepath.Join(t.TempDir(), "nope"), nil); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("missing root: err = %v", err)
	}
	f := filepath.Join(t.TempDir(), "file")
	write(t, filepath.Dir(f), "file", "x")
	if _, err := Walk(f, nil); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("file root: err = %v", err)
	}
}

func TestMatch(t *testing.T) {
	cases := []struct {
		pat, rel string
		want     bool
	}{
		{"*.tf", "a/b/c.tf", true},
		{"*.tf", "a/b/c.yaml", false},
		{"ignored", "ignored", true},
		{"ignored/", "ignored", true},
		{"a/*/c.tf", "a/b/c.tf", true},
		{"a/*/c.tf", "a/b/x/c.tf", false},
		{"a/**/c.tf", "a/c.tf", true},
		{"a/**/c.tf", "a/b/x/c.tf", true},
		{"**/test", "x/y/test", true},
		{"**", "anything/here", true},
		{"/infra/legacy", "infra/legacy", true},
		{"infra/legacy", "x/infra/legacy", false},
	}
	for _, c := range cases {
		if got := Match(c.pat, c.rel); got != c.want {
			t.Errorf("Match(%q,%q) = %v, want %v", c.pat, c.rel, got, c.want)
		}
	}
}
