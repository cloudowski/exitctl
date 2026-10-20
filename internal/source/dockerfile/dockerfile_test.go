package dockerfile

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

const multiStage = `ARG BASE=node:20
FROM ${BASE} AS deps
FROM node:20 AS build
RUN npm ci
FROM build AS test
FROM public.ecr.aws/lambda/nodejs:20
COPY --from=build /app /app
COPY --from=0 /x /x
COPY --from=gcr.io/distroless/base:latest /etc/ssl /etc/ssl
FROM 123456789012.dkr.ecr.eu-west-1.amazonaws.com/app:1.2
FROM scratch
FROM localhost:5000/tools
FROM node:20
`

func TestMultiStage(t *testing.T) {
	f := run(t, map[string]string{"svc/Dockerfile": multiStage})
	type want struct {
		ref, registry string
		lines         []int
	}
	wants := []want{
		{"123456789012.dkr.ecr.eu-west-1.amazonaws.com/app:1.2", "123456789012.dkr.ecr.eu-west-1.amazonaws.com", []int{10}},
		{"gcr.io/distroless/base:latest", "gcr.io", []int{9}},
		{"localhost:5000/tools", "localhost:5000", []int{12}},
		{"node:20", "docker.io", []int{3, 13}},
		{"public.ecr.aws/lambda/nodejs:20", "public.ecr.aws", []int{6}},
	}
	if len(f.RawNodes) != len(wants) {
		t.Fatalf("got %d nodes: %+v", len(f.RawNodes), f.RawNodes)
	}
	for i, w := range wants {
		n := f.RawNodes[i]
		if n.ID != "r//svc/Dockerfile#image/"+w.ref {
			t.Errorf("node %d id %q", i, n.ID)
		}
		if n.Type != model.TypeContainerImage || n.SourceKind != model.KindDockerfile || n.Multiplicity != model.MultOne {
			t.Errorf("node %d: %+v", i, n)
		}
		if n.Attributes["image"] != w.ref || n.Attributes["registry"] != w.registry {
			t.Errorf("node %d attrs %v", i, n.Attributes)
		}
		if len(n.Evidence) != len(w.lines) {
			t.Fatalf("node %d evidence %+v", i, n.Evidence)
		}
		for j, l := range w.lines {
			if n.Evidence[j].Line != l || n.Evidence[j].Path != "svc/Dockerfile" || n.Evidence[j].Repo != "r" {
				t.Errorf("node %d evidence %+v", i, n.Evidence[j])
			}
		}
	}
	if got := f.RawNodes[3].Evidence[0].Excerpt; got != "FROM node:20 AS build" {
		t.Errorf("excerpt %q", got)
	}
	if len(f.Sources) != 1 || f.Sources[0].Status != model.StatusParsed || f.Sources[0].Kind != model.KindDockerfile {
		t.Errorf("sources %+v", f.Sources)
	}
}

func TestRegistry(t *testing.T) {
	cases := map[string]string{
		"node:20":                          "docker.io",
		"library/node":                     "docker.io",
		"bitnami/redis:7":                  "docker.io",
		"gcr.io/proj/img":                  "gcr.io",
		"public.ecr.aws/x/y":               "public.ecr.aws",
		"localhost/img":                    "localhost",
		"myreg:5000/img":                   "myreg:5000",
		"mcr.microsoft.com/dotnet/sdk:8.0": "mcr.microsoft.com",
	}
	for ref, want := range cases {
		if got := Registry(ref); got != want {
			t.Errorf("Registry(%q) = %q, want %q", ref, got, want)
		}
	}
}

func TestEmptyFile(t *testing.T) {
	f := run(t, map[string]string{"Dockerfile": ""})
	if len(f.RawNodes) != 0 || len(f.Sources) != 1 {
		t.Fatalf("%+v", f)
	}
	if f.Sources[0].Status == model.StatusFailed && f.Sources[0].Note == "" {
		t.Errorf("failed source without note")
	}
}
