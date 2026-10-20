package kubernetes

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/cloudowski/exitctl/internal/model"
	"github.com/cloudowski/exitctl/internal/redact"
	"github.com/cloudowski/exitctl/internal/source"
	"github.com/cloudowski/exitctl/internal/walk"
)

// Neutral source notes.
const (
	noteYAMLFailed  = "could not be parsed as YAML"
	noteYAMLPartial = "some YAML documents could not be parsed"
)

// clusterScoped lists well-known cluster-scoped kinds; their native address
// uses "_" as the namespace.
var clusterScoped = map[string]bool{
	"Namespace": true, "Node": true, "PersistentVolume": true, "StorageClass": true,
	"IngressClass": true, "ClusterRole": true, "ClusterRoleBinding": true,
	"CustomResourceDefinition": true, "CSIDriver": true, "PriorityClass": true,
	"ValidatingWebhookConfiguration": true, "MutatingWebhookConfiguration": true,
	"APIService": true,
}

// object is one Kubernetes object decoded from a YAML document.
type object struct {
	group, kind, ns, name string
	node                  *yaml.Node // the object's mapping node
	data                  map[string]any
	line                  int // line of the `kind:` key in the parsed text
}

func (o *object) typ() string     { return o.group + "/" + o.kind }
func (o *object) address() string { return address(o.group, o.kind, o.ns, o.name) }

func address(group, kind, ns, name string) string {
	return group + "/" + kind + "/" + ns + "/" + name
}

// groupOf returns the API group of an apiVersion ("" for the core group).
func groupOf(apiVersion string) string {
	if i := strings.Index(apiVersion, "/"); i >= 0 {
		return apiVersion[:i]
	}
	return ""
}

func namespaceFor(kind, ns string) string {
	if clusterScoped[kind] {
		return "_"
	}
	if ns == "" {
		return "default"
	}
	return ns
}

// parseObjects decodes multi-document YAML into objects. List kinds are
// expanded into their items; documents that are not objects are skipped. It
// returns the objects decoded before the first error, if any.
func parseObjects(data []byte) ([]*object, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var objs []*object
	for {
		var doc yaml.Node
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			return objs, nil
		}
		if err != nil {
			return objs, err
		}
		n := &doc
		if n.Kind == yaml.DocumentNode {
			if len(n.Content) == 0 {
				continue
			}
			n = n.Content[0]
		}
		objs = collect(n, objs)
	}
}

func collect(n *yaml.Node, objs []*object) []*object {
	n = deref(n)
	if n == nil || n.Kind != yaml.MappingNode {
		return objs
	}
	apiVersion, kind := scalar(get(n, "apiVersion")), scalar(get(n, "kind"))
	if apiVersion == "" || kind == "" {
		return objs
	}
	if items := get(n, "items"); strings.HasSuffix(kind, "List") && items != nil && items.Kind == yaml.SequenceNode {
		for _, it := range items.Content {
			objs = collect(it, objs)
		}
		return objs
	}
	name := scalar(get(n, "metadata", "name"))
	if name == "" {
		name = scalar(get(n, "metadata", "generateName"))
	}
	if name == "" {
		return objs
	}
	m, _ := toValue(n).(map[string]any)
	line := n.Line
	if k := keyNode(n, "kind"); k != nil {
		line = k.Line
	}
	return append(objs, &object{
		group: groupOf(apiVersion),
		kind:  kind,
		ns:    namespaceFor(kind, scalar(get(n, "metadata", "namespace"))),
		name:  name,
		node:  n,
		data:  m,
		line:  line,
	})
}

// --- YAML node helpers ---

func deref(n *yaml.Node) *yaml.Node {
	for i := 0; n != nil && n.Kind == yaml.AliasNode && i < 32; i++ {
		n = n.Alias
	}
	return n
}

func keyNode(m *yaml.Node, key string) *yaml.Node {
	m = deref(m)
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i]
		}
	}
	return nil
}

// get walks mapping keys and returns the value node, or nil.
func get(n *yaml.Node, keys ...string) *yaml.Node {
	for _, k := range keys {
		n = deref(n)
		if n == nil || n.Kind != yaml.MappingNode {
			return nil
		}
		var next *yaml.Node
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == k {
				next = n.Content[i+1]
				break
			}
		}
		if next == nil {
			return nil
		}
		n = next
	}
	return deref(n)
}

func scalar(n *yaml.Node) string {
	n = deref(n)
	if n == nil || n.Kind != yaml.ScalarNode {
		return ""
	}
	return strings.TrimSpace(n.Value)
}

func items(n *yaml.Node) []*yaml.Node {
	n = deref(n)
	if n == nil || n.Kind != yaml.SequenceNode {
		return nil
	}
	return n.Content
}

// toValue converts a YAML node into plain Go values (map[string]any, []any,
// string, int64, float64, bool, nil). Timestamps and unusual scalars stay
// strings.
func toValue(n *yaml.Node) any {
	return toValueDepth(n, 0)
}

func toValueDepth(n *yaml.Node, depth int) any {
	n = deref(n)
	if n == nil || depth > 100 {
		return nil
	}
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return nil
		}
		return toValueDepth(n.Content[0], depth+1)
	case yaml.MappingNode:
		out := make(map[string]any, len(n.Content)/2)
		var merges []*yaml.Node
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i].Value
			if k == "<<" && n.Content[i].ShortTag() == "!!merge" {
				merges = append(merges, n.Content[i+1])
				continue
			}
			out[k] = toValueDepth(n.Content[i+1], depth+1)
		}
		for _, mn := range merges {
			mn = deref(mn)
			srcs := []*yaml.Node{mn}
			if mn != nil && mn.Kind == yaml.SequenceNode {
				srcs = mn.Content
			}
			for _, s := range srcs {
				if mm, ok := toValueDepth(s, depth+1).(map[string]any); ok {
					for k, v := range mm {
						if _, exists := out[k]; !exists {
							out[k] = v
						}
					}
				}
			}
		}
		return out
	case yaml.SequenceNode:
		out := make([]any, len(n.Content))
		for i, c := range n.Content {
			out[i] = toValueDepth(c, depth+1)
		}
		return out
	case yaml.ScalarNode:
		switch n.ShortTag() {
		case "!!null":
			return nil
		case "!!bool":
			if b, err := strconv.ParseBool(strings.ToLower(n.Value)); err == nil {
				return b
			}
		case "!!int":
			if i, err := strconv.ParseInt(strings.ReplaceAll(n.Value, "_", ""), 0, 64); err == nil {
				return i
			}
		case "!!float":
			if f, err := strconv.ParseFloat(n.Value, 64); err == nil {
				return f
			}
		}
		return n.Value
	}
	return nil
}

// --- facts ---

// located pairs an object with a function returning evidence for a line of
// the text it was parsed from.
type located struct {
	obj *object
	ev  func(line int) model.Evidence
}

// attributes returns the object map without apiVersion/kind, with values
// redacted. Secret data values are always redacted.
func attributes(o *object) map[string]any {
	out := make(map[string]any, len(o.data))
	secret := o.group == "" && o.kind == "Secret"
	for k, v := range o.data {
		if k == "apiVersion" || k == "kind" {
			continue
		}
		if secret && (k == "data" || k == "stringData") {
			if m, ok := v.(map[string]any); ok {
				r := make(map[string]any, len(m))
				for kk := range m {
					r[kk] = model.Redacted
				}
				out[k] = r
			} else if v != nil {
				out[k] = model.Redacted
			}
			continue
		}
		out[k] = redact.Value(k, v)
	}
	return out
}

// buildFacts turns objects into raw nodes and reference edges. Nodes with the
// same ID are merged (evidence union). Service selectors are matched against
// all workloads in objs.
func buildFacts(repo string, kind model.SourceKind, objs []located) ([]model.RawNode, []model.RawEdge) {
	var nodes []model.RawNode
	nodeIdx := map[string]int{}
	for _, l := range objs {
		id := model.CanonicalID(repo, "", l.obj.address())
		ev := l.ev(l.obj.line)
		if i, ok := nodeIdx[id]; ok {
			nodes[i].Evidence = append(nodes[i].Evidence, ev)
			continue
		}
		nodeIdx[id] = len(nodes)
		nodes = append(nodes, model.RawNode{
			ID:            id,
			NodeKind:      model.NodeResource,
			SourceKind:    kind,
			Type:          l.obj.typ(),
			NativeAddress: l.obj.address(),
			Attributes:    attributes(l.obj),
			Multiplicity:  model.MultOne,
			Evidence:      []model.Evidence{ev},
		})
	}

	var edges []model.RawEdge
	edgeIdx := map[string]int{}
	addEdge := func(from string, r ref, ev model.Evidence) {
		to := model.CanonicalID(repo, "", r.to)
		if to == from {
			return
		}
		key := from + "\x00" + to + "\x00" + r.attr
		if i, ok := edgeIdx[key]; ok {
			edges[i].Evidence = append(edges[i].Evidence, ev)
			return
		}
		edgeIdx[key] = len(edges)
		edges = append(edges, model.RawEdge{From: from, To: to, Attribute: r.attr, Evidence: []model.Evidence{ev}})
	}

	// Workloads by namespace, for selector matching.
	type workload struct {
		addr   string
		labels map[string]string
	}
	byNS := map[string][]workload{}
	for _, l := range objs {
		if p := podTemplatePath(l.obj); p != nil {
			lbl := stringMap(get(l.obj.node, append(p, "metadata", "labels")...))
			byNS[l.obj.ns] = append(byNS[l.obj.ns], workload{addr: l.obj.address(), labels: lbl})
		}
	}

	for _, l := range objs {
		from := model.CanonicalID(repo, "", l.obj.address())
		for _, r := range refs(l.obj) {
			addEdge(from, r, l.ev(r.line))
		}
		if l.obj.group == "" && l.obj.kind == "Service" {
			selNode := get(l.obj.node, "spec", "selector")
			sel := stringMap(selNode)
			if len(sel) == 0 {
				continue
			}
			line := l.obj.line
			if k := keyNode(get(l.obj.node, "spec"), "selector"); k != nil {
				line = k.Line
			}
			for _, w := range byNS[l.obj.ns] {
				if matches(sel, w.labels) {
					addEdge(from, ref{to: w.addr, attr: "spec.selector", line: line}, l.ev(line))
				}
			}
		}
	}
	return nodes, edges
}

func stringMap(n *yaml.Node) map[string]string {
	n = deref(n)
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	out := map[string]string{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if v := deref(n.Content[i+1]); v != nil && v.Kind == yaml.ScalarNode {
			out[n.Content[i].Value] = v.Value
		}
	}
	return out
}

func matches(sel, labels map[string]string) bool {
	if len(sel) == 0 || len(labels) == 0 {
		return false
	}
	for k, v := range sel {
		if lv, ok := labels[k]; !ok || lv != v {
			return false
		}
	}
	return true
}

// ref is a reference from an object to another object's native address.
type ref struct {
	to   string
	attr string
	line int
}

var workloadGroups = map[string]map[string]bool{
	"Pod":         {"": true},
	"Deployment":  {"apps": true, "extensions": true},
	"StatefulSet": {"apps": true},
	"DaemonSet":   {"apps": true, "extensions": true},
	"ReplicaSet":  {"apps": true, "extensions": true},
	"Job":         {"batch": true},
	"CronJob":     {"batch": true},
}

// podTemplatePath returns the path to the object holding the pod's metadata
// and spec ("" path for a Pod itself), or nil for non-workloads.
func podTemplatePath(o *object) []string {
	if !workloadGroups[o.kind][o.group] {
		return nil
	}
	switch o.kind {
	case "Pod":
		return []string{}
	case "CronJob":
		return []string{"spec", "jobTemplate", "spec", "template"}
	default:
		return []string{"spec", "template"}
	}
}

func refs(o *object) []ref {
	var out []ref
	add := func(group, kind, ns string, nameNode *yaml.Node, attr string) {
		name := scalar(nameNode)
		if name == "" {
			return
		}
		if clusterScoped[kind] {
			ns = "_"
		}
		out = append(out, ref{to: address(group, kind, ns, name), attr: attr, line: nameNode.Line})
	}
	n := o.node
	if p := podTemplatePath(o); p != nil {
		specPath := append(append([]string{}, p...), "spec")
		prefix := strings.Join(specPath, ".")
		spec := get(n, specPath...)
		if sa := get(spec, "serviceAccountName"); scalar(sa) != "default" {
			add("", "ServiceAccount", o.ns, sa, prefix+".serviceAccountName")
		}
		if sa := get(spec, "serviceAccount"); scalar(sa) != "default" && get(spec, "serviceAccountName") == nil {
			add("", "ServiceAccount", o.ns, sa, prefix+".serviceAccount")
		}
		for _, v := range items(get(spec, "volumes")) {
			add("", "ConfigMap", o.ns, get(v, "configMap", "name"), prefix+".volumes.configMap.name")
			add("", "Secret", o.ns, get(v, "secret", "secretName"), prefix+".volumes.secret.secretName")
			add("", "PersistentVolumeClaim", o.ns, get(v, "persistentVolumeClaim", "claimName"), prefix+".volumes.persistentVolumeClaim.claimName")
			for _, s := range items(get(v, "projected", "sources")) {
				add("", "ConfigMap", o.ns, get(s, "configMap", "name"), prefix+".volumes.projected.sources.configMap.name")
				add("", "Secret", o.ns, get(s, "secret", "name"), prefix+".volumes.projected.sources.secret.name")
			}
		}
		for _, cs := range []string{"initContainers", "containers"} {
			cp := prefix + "." + cs
			for _, c := range items(get(spec, cs)) {
				for _, e := range items(get(c, "envFrom")) {
					add("", "ConfigMap", o.ns, get(e, "configMapRef", "name"), cp+".envFrom.configMapRef.name")
					add("", "Secret", o.ns, get(e, "secretRef", "name"), cp+".envFrom.secretRef.name")
				}
				for _, e := range items(get(c, "env")) {
					add("", "ConfigMap", o.ns, get(e, "valueFrom", "configMapKeyRef", "name"), cp+".env.valueFrom.configMapKeyRef.name")
					add("", "Secret", o.ns, get(e, "valueFrom", "secretKeyRef", "name"), cp+".env.valueFrom.secretKeyRef.name")
				}
			}
		}
		for _, s := range items(get(spec, "imagePullSecrets")) {
			add("", "Secret", o.ns, get(s, "name"), prefix+".imagePullSecrets.name")
		}
		if o.kind == "StatefulSet" {
			for _, t := range items(get(n, "spec", "volumeClaimTemplates")) {
				add("storage.k8s.io", "StorageClass", "_", get(t, "spec", "storageClassName"), "spec.volumeClaimTemplates.spec.storageClassName")
			}
		}
	}
	switch {
	case o.group == "" && o.kind == "PersistentVolumeClaim":
		add("storage.k8s.io", "StorageClass", "_", get(n, "spec", "storageClassName"), "spec.storageClassName")
	case o.kind == "Ingress" && (o.group == "networking.k8s.io" || o.group == "extensions"):
		add("networking.k8s.io", "IngressClass", "_", get(n, "spec", "ingressClassName"), "spec.ingressClassName")
		for _, b := range []string{"defaultBackend", "backend"} {
			add("", "Service", o.ns, get(n, "spec", b, "service", "name"), "spec."+b+".service.name")
			add("", "Service", o.ns, get(n, "spec", b, "serviceName"), "spec."+b+".serviceName")
		}
		for _, r := range items(get(n, "spec", "rules")) {
			for _, p := range items(get(r, "http", "paths")) {
				add("", "Service", o.ns, get(p, "backend", "service", "name"), "spec.rules.http.paths.backend.service.name")
				add("", "Service", o.ns, get(p, "backend", "serviceName"), "spec.rules.http.paths.backend.serviceName")
			}
		}
	case o.group == "autoscaling" && o.kind == "HorizontalPodAutoscaler":
		t := get(n, "spec", "scaleTargetRef")
		kind := scalar(get(t, "kind"))
		if kind == "" {
			break
		}
		group := groupOf(scalar(get(t, "apiVersion")))
		if scalar(get(t, "apiVersion")) == "" {
			switch kind {
			case "Deployment", "StatefulSet", "ReplicaSet", "DaemonSet":
				group = "apps"
			}
		}
		add(group, kind, o.ns, get(t, "name"), "spec.scaleTargetRef")
	}
	return out
}

// lineEvidence returns an evidence function for a file's lines.
func lineEvidence(repo, path string, kind model.SourceKind, data []byte) func(int) model.Evidence {
	lines := strings.Split(string(data), "\n")
	return func(line int) model.Evidence {
		if line < 1 || line > len(lines) {
			line = 1
		}
		text := ""
		if line <= len(lines) {
			text = lines[line-1]
		}
		return model.NewEvidence(repo, path, line, redact.Line(text), kind)
	}
}

// --- raw manifests detector ---

type manifests struct{}

// New returns the detector for raw Kubernetes manifests.
func New() source.Detector { return manifests{} }

func (manifests) Name() string { return "kubernetes" }

func (manifests) Detect(ctx context.Context, idx walk.Index, opts source.Options) (model.Facts, error) {
	var f model.Facts
	var all []located
	files := append([]string(nil), idx.Files[model.KindKubernetes]...)
	sort.Strings(files)
	for _, rel := range files {
		if err := ctx.Err(); err != nil {
			return f, err
		}
		data, err := os.ReadFile(filepath.Join(idx.Root, filepath.FromSlash(rel)))
		if err != nil {
			f.Sources = append(f.Sources, source.FailedSource(opts.Repo, rel, model.KindKubernetes, "file could not be read"))
			continue
		}
		objs, perr := parseObjects(data)
		src := model.Source{Repo: opts.Repo, Path: rel, Kind: model.KindKubernetes, Status: model.StatusParsed}
		if perr != nil {
			if len(objs) == 0 {
				src = source.FailedSource(opts.Repo, rel, model.KindKubernetes, noteYAMLFailed)
			} else {
				src.Status, src.Note = model.StatusPartial, noteYAMLPartial
			}
		}
		f.Sources = append(f.Sources, src)
		ev := lineEvidence(opts.Repo, rel, model.KindKubernetes, data)
		for _, o := range objs {
			all = append(all, located{obj: o, ev: ev})
		}
	}
	f.RawNodes, f.RawEdges = buildFacts(opts.Repo, model.KindKubernetes, all)
	return f, nil
}
