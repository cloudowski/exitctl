package kubernetes

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudowski/exitctl/internal/model"
	"github.com/cloudowski/exitctl/internal/source"
	"github.com/cloudowski/exitctl/internal/walk"
)

const repo = "github.com/acme/shop"

func writeFiles(t *testing.T, files map[string]string) string {
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
	return root
}

func detect(t *testing.T, d source.Detector, root string, opts source.Options) model.Facts {
	t.Helper()
	idx, err := walk.Walk(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	opts.Repo = repo
	opts.Root = idx.Root
	f, err := d.Detect(context.Background(), idx, opts)
	if err != nil {
		t.Fatal(err)
	}
	f.Sort()
	return f
}

func id(addr string) string { return model.CanonicalID(repo, "", addr) }

func nodeByID(f model.Facts, id string) *model.RawNode {
	for i := range f.RawNodes {
		if f.RawNodes[i].ID == id {
			return &f.RawNodes[i]
		}
	}
	return nil
}

func hasEdge(f model.Facts, from, to, attr string) *model.RawEdge {
	for i, e := range f.RawEdges {
		if e.From == from && e.To == to && e.Attribute == attr {
			return &f.RawEdges[i]
		}
	}
	return nil
}

func lookup(m map[string]any, keys ...string) any {
	var cur any = m
	for _, k := range keys {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = mm[k]
	}
	return cur
}

const appYAML = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
  namespace: shop
  annotations:
    team: payments
spec:
  template:
    metadata:
      labels:
        app: web
        tier: front
    spec:
      serviceAccountName: web-sa
      volumes:
        - name: cfg
          configMap:
            name: web-config
        - name: data
          persistentVolumeClaim:
            claimName: web-data
      containers:
        - name: web
          image: nginx
          envFrom:
            - secretRef:
                name: web-secret
          env:
            - name: X
              valueFrom:
                configMapKeyRef:
                  name: other-config
                  key: x
---
apiVersion: v1
kind: Service
metadata:
  name: web
  namespace: shop
spec:
  selector:
    app: web
---
apiVersion: v1
kind: Service
metadata:
  name: nomatch
  namespace: other
spec:
  selector:
    app: web
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: web-data
  namespace: shop
spec:
  storageClassName: gp3
---
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: gp3
provisioner: ebs.csi.aws.com
---
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: web
  namespace: shop
  annotations:
    alb.ingress.kubernetes.io/scheme: internet-facing
spec:
  ingressClassName: alb
  defaultBackend:
    service:
      name: fallback
  rules:
    - http:
        paths:
          - path: /
            backend:
              service:
                name: web
---
apiVersion: v1
kind: Secret
metadata:
  name: web-secret
  namespace: shop
data:
  token: aGVsbG8=
stringData:
  plain: hello
---
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: web
  namespace: shop
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: web
---
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: db
spec:
  volumeClaimTemplates:
    - metadata:
        name: d
      spec:
        storageClassName: fast
  template:
    spec:
      serviceAccountName: default
      containers:
        - name: db
          image: postgres
---
apiVersion: dynamodb.services.k8s.aws/v1alpha1
kind: Table
metadata:
  name: orders
spec:
  tableName: orders
`

func TestManifestsNodes(t *testing.T) {
	root := writeFiles(t, map[string]string{"k8s/app.yaml": appYAML})
	f := detect(t, New(), root, source.Options{})

	if len(f.Sources) != 1 || f.Sources[0].Status != model.StatusParsed || f.Sources[0].Path != "k8s/app.yaml" || f.Sources[0].Kind != model.KindKubernetes {
		t.Fatalf("sources = %+v", f.Sources)
	}
	tests := []struct {
		addr, typ string
		line      int
	}{
		{"apps/Deployment/shop/web", "apps/Deployment", 2},
		{"/Service/shop/web", "/Service", 37},
		{"/PersistentVolumeClaim/shop/web-data", "/PersistentVolumeClaim", 55},
		{"storage.k8s.io/StorageClass/_/gp3", "storage.k8s.io/StorageClass", 63},
		{"networking.k8s.io/Ingress/shop/web", "networking.k8s.io/Ingress", 69},
		{"/Secret/shop/web-secret", "/Secret", 89},
		{"apps/StatefulSet/default/db", "apps/StatefulSet", 110},
		{"dynamodb.services.k8s.aws/Table/default/orders", "dynamodb.services.k8s.aws/Table", 127},
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			n := nodeByID(f, id(tt.addr))
			if n == nil {
				t.Fatalf("missing node %s", tt.addr)
			}
			if n.Type != tt.typ || n.NativeAddress != tt.addr || n.SourceKind != model.KindKubernetes || n.Multiplicity != model.MultOne || n.NodeKind != model.NodeResource {
				t.Errorf("node = %+v", n)
			}
			if len(n.Evidence) != 1 || n.Evidence[0].Line != tt.line || n.Evidence[0].Path != "k8s/app.yaml" || n.Evidence[0].Repo != repo {
				t.Errorf("evidence = %+v", n.Evidence)
			}
			if _, ok := n.Attributes["kind"]; ok {
				t.Errorf("kind must not be an attribute")
			}
		})
	}
	if len(f.RawNodes) != 10 {
		t.Errorf("got %d nodes", len(f.RawNodes))
	}

	sc := nodeByID(f, id("storage.k8s.io/StorageClass/_/gp3"))
	if sc.Attributes["provisioner"] != "ebs.csi.aws.com" || sc.Evidence[0].Excerpt != "kind: StorageClass" {
		t.Errorf("storageclass = %+v", sc)
	}
	ing := nodeByID(f, id("networking.k8s.io/Ingress/shop/web"))
	if lookup(ing.Attributes, "spec", "ingressClassName") != "alb" ||
		lookup(ing.Attributes, "metadata", "annotations", "alb.ingress.kubernetes.io/scheme") != "internet-facing" ||
		lookup(ing.Attributes, "metadata", "namespace") != "shop" {
		t.Errorf("ingress attrs = %+v", ing.Attributes)
	}
	sec := nodeByID(f, id("/Secret/shop/web-secret"))
	if !model.IsRedacted(lookup(sec.Attributes, "data", "token")) || !model.IsRedacted(lookup(sec.Attributes, "stringData", "plain")) {
		t.Errorf("secret not redacted: %+v", sec.Attributes)
	}
}

func TestManifestsEdges(t *testing.T) {
	root := writeFiles(t, map[string]string{"k8s/app.yaml": appYAML})
	f := detect(t, New(), root, source.Options{})
	dep := id("apps/Deployment/shop/web")
	tests := []struct {
		from, to, attr string
		line           int
	}{
		{dep, id("/ServiceAccount/shop/web-sa"), "spec.template.spec.serviceAccountName", 15},
		{dep, id("/ConfigMap/shop/web-config"), "spec.template.spec.volumes.configMap.name", 19},
		{dep, id("/PersistentVolumeClaim/shop/web-data"), "spec.template.spec.volumes.persistentVolumeClaim.claimName", 22},
		{dep, id("/Secret/shop/web-secret"), "spec.template.spec.containers.envFrom.secretRef.name", 28},
		{dep, id("/ConfigMap/shop/other-config"), "spec.template.spec.containers.env.valueFrom.configMapKeyRef.name", 33},
		{id("/Service/shop/web"), dep, "spec.selector", 42},
		{id("/PersistentVolumeClaim/shop/web-data"), id("storage.k8s.io/StorageClass/_/gp3"), "spec.storageClassName", 60},
		{id("networking.k8s.io/Ingress/shop/web"), id("networking.k8s.io/IngressClass/_/alb"), "spec.ingressClassName", 76},
		{id("networking.k8s.io/Ingress/shop/web"), id("/Service/shop/fallback"), "spec.defaultBackend.service.name", 79},
		{id("networking.k8s.io/Ingress/shop/web"), id("/Service/shop/web"), "spec.rules.http.paths.backend.service.name", 86},
		{id("autoscaling/HorizontalPodAutoscaler/shop/web"), dep, "spec.scaleTargetRef", 107},
		{id("apps/StatefulSet/default/db"), id("storage.k8s.io/StorageClass/_/fast"), "spec.volumeClaimTemplates.spec.storageClassName", 118},
	}
	for _, tt := range tests {
		t.Run(tt.attr, func(t *testing.T) {
			e := hasEdge(f, tt.from, tt.to, tt.attr)
			if e == nil {
				t.Fatalf("missing edge %s -> %s (%s); edges=%+v", tt.from, tt.to, tt.attr, f.RawEdges)
			}
			if len(e.Evidence) != 1 || e.Evidence[0].Line != tt.line {
				t.Errorf("evidence = %+v, want line %d", e.Evidence, tt.line)
			}
		})
	}
	if len(f.RawEdges) != len(tests) {
		t.Errorf("got %d edges, want %d: %+v", len(f.RawEdges), len(tests), f.RawEdges)
	}
}

func TestManifestsListAndFailures(t *testing.T) {
	root := writeFiles(t, map[string]string{
		"list.yaml": `apiVersion: v1
kind: List
items:
  - apiVersion: v1
    kind: ConfigMap
    metadata:
      name: a
  - apiVersion: v1
    kind: Namespace
    metadata:
      name: prod
`,
		"broken.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata: [unclosed\n",
		"partial.yaml": `apiVersion: v1
kind: ConfigMap
metadata:
  name: ok
---
apiVersion: v1
kind: ConfigMap
metadata: {bad
`,
		"empty-docs.yaml": "---\n---\napiVersion: v1\nkind: ServiceAccount\nmetadata:\n  name: sa\n---\n",
	})
	f := detect(t, New(), root, source.Options{})
	status := map[string]model.Source{}
	for _, s := range f.Sources {
		status[s.Path] = s
	}
	if s := status["broken.yaml"]; s.Status != model.StatusFailed || s.Note != noteYAMLFailed {
		t.Errorf("broken = %+v", s)
	}
	if s := status["partial.yaml"]; s.Status != model.StatusPartial {
		t.Errorf("partial = %+v", s)
	}
	if s := status["list.yaml"]; s.Status != model.StatusParsed {
		t.Errorf("list = %+v", s)
	}
	for addr, line := range map[string]int{
		"/ConfigMap/default/a":       5,
		"/Namespace/_/prod":          9,
		"/ConfigMap/default/ok":      2,
		"/ServiceAccount/default/sa": 4,
	} {
		n := nodeByID(f, id(addr))
		if n == nil {
			t.Errorf("missing %s", addr)
			continue
		}
		if n.Evidence[0].Line != line {
			t.Errorf("%s line = %d, want %d", addr, n.Evidence[0].Line, line)
		}
	}
	if len(f.RawNodes) != 4 {
		t.Errorf("nodes = %+v", f.RawNodes)
	}
}

func TestManifestsMergeDuplicates(t *testing.T) {
	cm := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: x\ndata:\n  password: hunter2\n"
	root := writeFiles(t, map[string]string{"a.yaml": cm, "b.yaml": cm})
	f := detect(t, New(), root, source.Options{})
	if len(f.RawNodes) != 1 || len(f.RawNodes[0].Evidence) != 2 {
		t.Fatalf("nodes = %+v", f.RawNodes)
	}
	if !model.IsRedacted(lookup(f.RawNodes[0].Attributes, "data", "password")) {
		t.Errorf("password not redacted: %+v", f.RawNodes[0].Attributes)
	}
}
