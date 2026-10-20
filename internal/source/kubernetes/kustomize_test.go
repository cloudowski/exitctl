package kubernetes

import (
	"path/filepath"
	"testing"

	"github.com/cloudowski/exitctl/internal/model"
	"github.com/cloudowski/exitctl/internal/source"
)

func TestKustomizeLocal(t *testing.T) {
	root := writeFiles(t, map[string]string{
		"k/base/kustomization.yaml": "resources:\n  - deploy.yaml\n  - pvc.yaml\n",
		"k/base/deploy.yaml": `apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
spec:
  template:
    metadata:
      labels:
        app: web
    spec:
      volumes:
        - name: d
          persistentVolumeClaim:
            claimName: data
      containers:
        - name: web
          image: nginx
`,
		"k/base/pvc.yaml": `apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: data
spec:
  storageClassName: gp3
`,
		"k/overlays/prod/kustomization.yaml": "namespace: prod\nresources:\n  - ../../base\n",
	})
	f := detect(t, NewKustomize(), root, source.Options{})
	srcs := map[string]model.Source{}
	for _, s := range f.Sources {
		srcs[s.Path] = s
	}
	for _, p := range []string{"k/base/kustomization.yaml", "k/overlays/prod/kustomization.yaml"} {
		if s := srcs[p]; s.Status != model.StatusParsed || s.Kind != model.KindKustomize {
			t.Errorf("%s = %+v", p, s)
		}
	}
	tests := []struct{ addr, evPath string }{
		{"apps/Deployment/default/web", "k/base/kustomization.yaml"},
		{"/PersistentVolumeClaim/default/data", "k/base/kustomization.yaml"},
		{"apps/Deployment/prod/web", "k/overlays/prod/kustomization.yaml"},
		{"/PersistentVolumeClaim/prod/data", "k/overlays/prod/kustomization.yaml"},
	}
	for _, tt := range tests {
		n := nodeByID(f, id(tt.addr))
		if n == nil {
			t.Errorf("missing %s; nodes=%+v", tt.addr, f.RawNodes)
			continue
		}
		if n.SourceKind != model.KindKustomize || len(n.Evidence) != 1 || n.Evidence[0].Path != tt.evPath || n.Evidence[0].Line != 1 {
			t.Errorf("%s = %+v", tt.addr, n)
		}
	}
	if hasEdge(f, id("apps/Deployment/prod/web"), id("/PersistentVolumeClaim/prod/data"), "spec.template.spec.volumes.persistentVolumeClaim.claimName") == nil {
		t.Errorf("missing workload -> pvc edge: %+v", f.RawEdges)
	}
	if hasEdge(f, id("/PersistentVolumeClaim/prod/data"), id("storage.k8s.io/StorageClass/_/gp3"), "spec.storageClassName") == nil {
		t.Errorf("missing pvc -> sc edge: %+v", f.RawEdges)
	}
	if len(f.Gaps) != 0 {
		t.Errorf("gaps = %+v", f.Gaps)
	}
}

func TestKustomizeRemoteSkipped(t *testing.T) {
	calls := 0
	orig := runKrusty
	runKrusty = func(absDir string) ([]byte, error) {
		if filepath.Base(absDir) != "local" {
			t.Errorf("krusty must not run for %s", absDir)
		}
		calls++
		return orig(absDir)
	}
	defer func() { runKrusty = orig }()

	root := writeFiles(t, map[string]string{
		"remote/kustomization.yaml":  "resources:\n  - https://github.com/acme/infra//base?ref=v1\n  - cm.yaml\n",
		"remote/cm.yaml":             "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: r\n",
		"gh/kustomization.yaml":      "bases:\n  - github.com/acme/infra/base\n",
		"hosted/kustomization.yaml":  "components:\n  - gitlab.example.com/acme/comp\n",
		"nested/kustomization.yaml":  "resources:\n  - ../inner\n",
		"inner/kustomization.yaml":   "resources:\n  - git@github.com:acme/infra.git\n",
		"helm/kustomization.yaml":    "helmCharts:\n  - name: redis\n    repo: https://charts.bitnami.com/bitnami\n",
		"local/kustomization.yaml":   "resources:\n  - cm.yaml\n",
		"local/cm.yaml":              "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: l\n",
		"missing/kustomization.yaml": "resources:\n  - nope.yaml\n",
		"outside/kustomization.yaml": "resources:\n  - ../../elsewhere\n",
	})
	f := detect(t, NewKustomize(), root, source.Options{})
	if calls != 1 {
		t.Errorf("krusty calls = %d, want 1", calls)
	}
	wantGaps := []model.Gap{
		{Kind: model.GapRemoteKustomize, Subject: "git@github.com:acme/infra.git", Repo: repo, Path: "inner/kustomization.yaml", Count: 1},
		{Kind: model.GapRemoteKustomize, Subject: "github.com/acme/infra/base", Repo: repo, Path: "gh/kustomization.yaml", Count: 1},
		{Kind: model.GapRemoteKustomize, Subject: "gitlab.example.com/acme/comp", Repo: repo, Path: "hosted/kustomization.yaml", Count: 1},
		{Kind: model.GapRemoteKustomize, Subject: "helmCharts", Repo: repo, Path: "helm/kustomization.yaml", Count: 1},
		{Kind: model.GapRemoteKustomize, Subject: "https://github.com/acme/infra//base?ref=v1", Repo: repo, Path: "remote/kustomization.yaml", Count: 1},
	}
	if len(f.Gaps) != len(wantGaps) {
		t.Fatalf("gaps = %+v", f.Gaps)
	}
	for i, g := range wantGaps {
		if f.Gaps[i] != g {
			t.Errorf("gap[%d] = %+v, want %+v", i, f.Gaps[i], g)
		}
	}
	wantStatus := map[string]model.SourceStatus{
		"remote/kustomization.yaml":  model.StatusUnresolved,
		"gh/kustomization.yaml":      model.StatusUnresolved,
		"hosted/kustomization.yaml":  model.StatusUnresolved,
		"nested/kustomization.yaml":  model.StatusUnresolved,
		"inner/kustomization.yaml":   model.StatusUnresolved,
		"helm/kustomization.yaml":    model.StatusUnresolved,
		"local/kustomization.yaml":   model.StatusParsed,
		"missing/kustomization.yaml": model.StatusUnresolved,
		"outside/kustomization.yaml": model.StatusUnresolved,
	}
	for _, s := range f.Sources {
		if want, ok := wantStatus[s.Path]; !ok || s.Status != want {
			t.Errorf("source %+v, want %s", s, want)
		}
	}
	if len(f.Sources) != len(wantStatus) {
		t.Errorf("sources = %+v", f.Sources)
	}
	if len(f.RawNodes) != 1 || f.RawNodes[0].ID != id("/ConfigMap/default/l") {
		t.Errorf("nodes = %+v", f.RawNodes)
	}
}
