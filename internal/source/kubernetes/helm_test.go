package kubernetes

import (
	"testing"

	"github.com/cloudowski/exitctl/internal/model"
	"github.com/cloudowski/exitctl/internal/source"
)

var chartFiles = map[string]string{
	"charts/app/Chart.yaml": `apiVersion: v2
name: app
version: 0.1.0
`,
	"charts/app/values.yaml": `storageClass: gp3
provisioner: ebs.csi.aws.com
replicas: 1
`,
	"charts/app/templates/_helpers.tpl": `{{- define "app.name" -}}{{ .Release.Name }}-app{{- end -}}
`,
	"charts/app/templates/NOTES.txt": `Installed {{ .Release.Name }}.
`,
	"charts/app/templates/storage.yaml": `# storage
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: {{ .Values.storageClass }}
provisioner: {{ .Values.provisioner }}
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: {{ include "app.name" . }}
spec:
  storageClassName: {{ .Values.storageClass }}
`,
	"charts/app/templates/deploy.yaml": `apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ include "app.name" . }}
  namespace: {{ .Release.Namespace }}
spec:
  replicas: {{ .Values.replicas }}
  template:
    metadata:
      labels:
        app: web
    spec:
      containers:
        - name: web
          image: nginx
---
apiVersion: v1
kind: Service
metadata:
  name: web
spec:
  selector:
    app: web
`,
	"envs/prod.yaml": `storageClass: premium
provisioner: disk.csi.azure.com
`,
}

func TestHelmRender(t *testing.T) {
	tests := []struct {
		name        string
		values      map[string][]string
		sc, prov    string
		wantSources model.SourceStatus
	}{
		{"defaults", nil, "gp3", "ebs.csi.aws.com", model.StatusParsed},
		{"values override", map[string][]string{"charts/app": {"envs/prod.yaml"}}, "premium", "disk.csi.azure.com", model.StatusParsed},
		{"missing values file", map[string][]string{"charts/app": {"envs/nope.yaml"}}, "gp3", "ebs.csi.aws.com", model.StatusPartial},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := writeFiles(t, chartFiles)
			f := detect(t, NewHelm(), root, source.Options{HelmValues: tt.values})
			if len(f.Sources) != 1 || f.Sources[0].Path != "charts/app/Chart.yaml" || f.Sources[0].Kind != model.KindHelm || f.Sources[0].Status != tt.wantSources {
				t.Fatalf("sources = %+v", f.Sources)
			}
			if len(f.Gaps) != 0 {
				t.Errorf("gaps = %+v", f.Gaps)
			}
			sc := nodeByID(f, id("storage.k8s.io/StorageClass/_/"+tt.sc))
			if sc == nil {
				t.Fatalf("missing storage class; nodes = %+v", f.RawNodes)
			}
			if sc.SourceKind != model.KindHelm || sc.Attributes["provisioner"] != tt.prov {
				t.Errorf("sc = %+v", sc)
			}
			if ev := sc.Evidence[0]; ev.Path != "charts/app/templates/storage.yaml" || ev.Line != 3 || ev.Excerpt != "kind: StorageClass" {
				t.Errorf("sc evidence = %+v", ev)
			}
			pvc := id("/PersistentVolumeClaim/default/exitctl-app")
			if n := nodeByID(f, pvc); n == nil || n.Evidence[0].Line != 9 {
				t.Fatalf("pvc = %+v", n)
			}
			if hasEdge(f, pvc, sc.ID, "spec.storageClassName") == nil {
				t.Errorf("missing pvc -> sc edge: %+v", f.RawEdges)
			}
			dep := id("apps/Deployment/default/exitctl-app")
			d := nodeByID(f, dep)
			if d == nil || d.Evidence[0].Path != "charts/app/templates/deploy.yaml" || d.Evidence[0].Line != 2 {
				t.Fatalf("deployment = %+v", d)
			}
			if lookup(d.Attributes, "spec", "replicas") != int64(1) {
				t.Errorf("replicas = %#v", lookup(d.Attributes, "spec", "replicas"))
			}
			if hasEdge(f, id("/Service/default/web"), dep, "spec.selector") == nil {
				t.Errorf("missing service -> deployment edge: %+v", f.RawEdges)
			}
			if len(f.RawNodes) != 4 {
				t.Errorf("nodes = %d", len(f.RawNodes))
			}
		})
	}
}

func TestHelmMissingDependency(t *testing.T) {
	root := writeFiles(t, map[string]string{
		"Chart.yaml": `apiVersion: v2
name: umbrella
version: 1.0.0
dependencies:
  - name: redis
    version: 18.x
    repository: https://charts.bitnami.com/bitnami
  - name: local
    version: 0.1.0
`,
		"charts/local/Chart.yaml":        "apiVersion: v2\nname: local\nversion: 0.1.0\n",
		"charts/local/templates/cm.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: from-sub\n",
		"templates/cm.yaml":              "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: top\n",
	})
	f := detect(t, NewHelm(), root, source.Options{})
	if len(f.Gaps) != 1 {
		t.Fatalf("gaps = %+v", f.Gaps)
	}
	g := f.Gaps[0]
	if g.Kind != model.GapUnresolvedChart || g.Subject != "redis (https://charts.bitnami.com/bitnami)" || g.Path != "Chart.yaml" || g.Repo != repo {
		t.Errorf("gap = %+v", g)
	}
	if len(f.Sources) != 1 || f.Sources[0].Status != model.StatusPartial || f.Sources[0].Path != "Chart.yaml" {
		t.Errorf("sources = %+v", f.Sources)
	}
	top := nodeByID(f, id("/ConfigMap/default/top"))
	sub := nodeByID(f, id("/ConfigMap/default/from-sub"))
	if top == nil || sub == nil {
		t.Fatalf("nodes = %+v", f.RawNodes)
	}
	if sub.Evidence[0].Path != "charts/local/templates/cm.yaml" || sub.Evidence[0].Line != 2 {
		t.Errorf("sub evidence = %+v", sub.Evidence)
	}
}

func TestHelmBrokenChart(t *testing.T) {
	root := writeFiles(t, map[string]string{
		"c/Chart.yaml":          "apiVersion: v2\nname: c\nversion: 0.1.0\n",
		"c/templates/bad.yaml":  "kind: {{ .Values.x | nosuchfunc }}\n",
		"d/Chart.yaml":          "name: [broken\n",
		"d/templates/cm.yaml":   "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: d\n",
		"e/Chart.yaml":          "apiVersion: v2\nname: e\nversion: 0.1.0\n",
		"e/templates/need.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: {{ required \"name is required\" .Values.name | default \"fallback\" }}\n",
	})
	f := detect(t, NewHelm(), root, source.Options{})
	got := map[string]model.Source{}
	for _, s := range f.Sources {
		got[s.Path] = s
	}
	if s := got["c/Chart.yaml"]; s.Status != model.StatusFailed || s.Note != noteChartRender {
		t.Errorf("c = %+v", s)
	}
	if s := got["d/Chart.yaml"]; s.Status != model.StatusFailed || s.Note != noteChartLoad {
		t.Errorf("d = %+v", s)
	}
	if s := got["e/Chart.yaml"]; s.Status != model.StatusPartial {
		t.Errorf("e = %+v", s)
	}
	if nodeByID(f, id("/ConfigMap/default/fallback")) == nil {
		t.Errorf("nodes = %+v", f.RawNodes)
	}
}
