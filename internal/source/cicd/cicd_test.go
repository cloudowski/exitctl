package cicd

import (
	"context"
	"os"
	"path/filepath"
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
	f, err := New().Detect(context.Background(), idx, source.Options{Repo: "r", Root: root})
	if err != nil {
		t.Fatal(err)
	}
	f.Sort()
	return f
}

type wantNode struct {
	id    string
	typ   string
	attrs map[string]any
	line  int
}

func check(t *testing.T, f model.Facts, kind model.SourceKind, wants []wantNode) {
	t.Helper()
	if len(f.RawNodes) != len(wants) {
		for _, n := range f.RawNodes {
			t.Logf("node %s", n.ID)
		}
		t.Fatalf("got %d nodes, want %d", len(f.RawNodes), len(wants))
	}
	for i, w := range wants {
		n := f.RawNodes[i]
		if n.ID != w.id || n.Type != w.typ || n.SourceKind != kind || n.Multiplicity != model.MultOne {
			t.Errorf("node %d: %s %s %s", i, n.ID, n.Type, n.SourceKind)
		}
		if len(n.Attributes) != len(w.attrs) {
			t.Errorf("node %d attrs %v", i, n.Attributes)
		}
		for k, v := range w.attrs {
			if n.Attributes[k] != v {
				t.Errorf("node %d attr %s = %v, want %v", i, k, n.Attributes[k], v)
			}
		}
		if len(n.Evidence) == 0 || n.Evidence[0].Line != w.line || n.Evidence[0].SourceKind != kind {
			t.Errorf("node %d evidence %+v, want line %d", i, n.Evidence, w.line)
		}
	}
}

const workflow = `name: ci
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    container: node:20
    services:
      db:
        image: postgres:16
      cache:
        image: public.ecr.aws/docker/library/redis:7
    steps:
      - uses: actions/checkout@v4
      - uses: aws-actions/configure-aws-credentials@v4
        with:
          role-to-assume: arn:aws:iam::123456789012:role/ci
      - uses: ./.github/actions/local
      - uses: docker://gcr.io/cloud-builders/gcloud
      - uses: actions/checkout@v3
      - uses: github/codeql-action/init@v3
  deploy:
    container:
      image: 123456789012.dkr.ecr.eu-west-1.amazonaws.com/deployer:1
    steps:
      - uses: actions/checkout@v4
  reuse:
    uses: acme/workflows/.github/workflows/build.yml@main
`

func TestGitHubActions(t *testing.T) {
	f := run(t, map[string]string{".github/workflows/ci.yml": workflow})
	p := "r//.github/workflows/ci.yml#"
	check(t, f, model.KindGitHubActions, []wantNode{
		{p + "action/acme/workflows/.github/workflows/build.yml", model.TypeCIAction, map[string]any{"action": "acme/workflows/.github/workflows/build.yml", "ref": "main"}, 27},
		{p + "action/actions/checkout", model.TypeCIAction, map[string]any{"action": "actions/checkout", "ref": "v4"}, 13},
		{p + "action/aws-actions/configure-aws-credentials", model.TypeCIAction, map[string]any{"action": "aws-actions/configure-aws-credentials", "ref": "v4"}, 14},
		{p + "action/github/codeql-action/init", model.TypeCIAction, map[string]any{"action": "github/codeql-action/init", "ref": "v3"}, 20},
		{p + "image/123456789012.dkr.ecr.eu-west-1.amazonaws.com/deployer:1", model.TypeContainerImage, map[string]any{"image": "123456789012.dkr.ecr.eu-west-1.amazonaws.com/deployer:1", "registry": "123456789012.dkr.ecr.eu-west-1.amazonaws.com"}, 23},
		{p + "image/gcr.io/cloud-builders/gcloud", model.TypeContainerImage, map[string]any{"image": "gcr.io/cloud-builders/gcloud", "registry": "gcr.io"}, 18},
		{p + "image/node:20", model.TypeContainerImage, map[string]any{"image": "node:20", "registry": "docker.io"}, 6},
		{p + "image/postgres:16", model.TypeContainerImage, map[string]any{"image": "postgres:16", "registry": "docker.io"}, 9},
		{p + "image/public.ecr.aws/docker/library/redis:7", model.TypeContainerImage, map[string]any{"image": "public.ecr.aws/docker/library/redis:7", "registry": "public.ecr.aws"}, 11},
	})
	// One node per distinct action: checkout used three times, evidence is the first use.
	if ev := f.RawNodes[1].Evidence; len(ev) != 1 || ev[0].Excerpt != "- uses: actions/checkout@v4" {
		t.Errorf("checkout evidence %+v", ev)
	}
	if len(f.Sources) != 1 || f.Sources[0].Status != model.StatusParsed || f.Sources[0].Path != ".github/workflows/ci.yml" {
		t.Errorf("sources %+v", f.Sources)
	}
}

const gitlab = `image: python:3.12
stages: [test, deploy]
variables:
  IMAGE: foo
include:
  - remote: https://example.com/ci.yml
  - project: acme/ci
services:
  - docker:dind
default:
  image:
    name: gcr.io/acme/base:1
test:
  image: $CI_REGISTRY_IMAGE:latest
  services:
    - name: postgres:16
      alias: db
    - redis:7
  script: [pytest]
deploy:
  image:
    name: 123456789012.dkr.ecr.eu-west-1.amazonaws.com/deploy:2
    entrypoint: [""]
  script: [deploy]
.template:
  image: python:3.12
`

func TestGitLabCI(t *testing.T) {
	f := run(t, map[string]string{".gitlab-ci.yml": gitlab})
	p := "r//.gitlab-ci.yml#image/"
	img := func(ref, reg string, line int) wantNode {
		return wantNode{p + ref, model.TypeContainerImage, map[string]any{"image": ref, "registry": reg}, line}
	}
	check(t, f, model.KindGitLabCI, []wantNode{
		img("123456789012.dkr.ecr.eu-west-1.amazonaws.com/deploy:2", "123456789012.dkr.ecr.eu-west-1.amazonaws.com", 22),
		img("docker:dind", "docker.io", 9),
		img("gcr.io/acme/base:1", "gcr.io", 12),
		img("postgres:16", "docker.io", 16),
		img("python:3.12", "docker.io", 1),
		img("redis:7", "docker.io", 18),
	})
	if ev := f.RawNodes[4].Evidence; len(ev) != 2 || ev[1].Line != 26 {
		t.Errorf("python evidence %+v", ev)
	}
}

func TestBrokenYAML(t *testing.T) {
	f := run(t, map[string]string{".github/workflows/bad.yml": "jobs: [\n"})
	if len(f.RawNodes) != 0 || len(f.Sources) != 1 {
		t.Fatalf("%+v", f)
	}
	s := f.Sources[0]
	if s.Status != model.StatusFailed || s.Note != "could not be parsed as YAML" {
		t.Errorf("source %+v", s)
	}
}
