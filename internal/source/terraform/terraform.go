package terraform

import (
	"context"
	"encoding/json"
	"math/big"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/cloudowski/exitctl/internal/model"
	"github.com/cloudowski/exitctl/internal/redact"
	"github.com/cloudowski/exitctl/internal/source"
	"github.com/cloudowski/exitctl/internal/walk"
)

// maxModuleDepth bounds module nesting so cyclic or pathological module
// graphs cannot recurse forever.
const maxModuleDepth = 32

// Neutral source notes.
const (
	noteFailed  = "could not be parsed as HCL"
	notePartial = "parsed with errors as HCL"
	noteRead    = "could not be read"
)

// identityKeys are literal name-like attributes used for provider identity,
// in priority order.
var identityKeys = []string{
	"name", "bucket", "function_name", "table_name", "queue_name", "cluster_name", "repository_name",
}

// resourceMetaArgs are meta-arguments excluded from resource attributes.
var resourceMetaArgs = map[string]bool{
	"count": true, "for_each": true, "depends_on": true, "provider": true,
	"lifecycle": true, "provisioner": true, "connection": true,
}

// moduleMetaArgs are module call arguments that are not inputs.
var moduleMetaArgs = map[string]bool{
	"source": true, "version": true, "count": true, "for_each": true,
	"depends_on": true, "providers": true,
}

// ignoredRoots are reference roots that never name a resource.
var ignoredRoots = map[string]bool{
	"var": true, "each": true, "count": true, "path": true, "self": true, "terraform": true,
}

var topSchema = &hcl.BodySchema{
	Blocks: []hcl.BlockHeaderSchema{
		{Type: "resource", LabelNames: []string{"type", "name"}},
		{Type: "data", LabelNames: []string{"type", "name"}},
		{Type: "module", LabelNames: []string{"name"}},
		{Type: "locals"},
	},
}

type detector struct{}

// New returns the Terraform/OpenTofu detector.
func New() source.Detector { return detector{} }

func (detector) Name() string { return "terraform" }

// parsedFile is one parsed configuration file. Blocks are extracted once and
// reused for every module instance that includes the file.
type parsedFile struct {
	path    string
	lines   []string
	ok      bool
	blocks  []*hcl.Block
	modules []*hcl.Block
}

// scope is one module instance: the files of dir, seen from the root module
// rootDir under the address prefix (e.g. "module.a.module.b.").
type scope struct {
	rootDir string
	dir     string
	prefix  string
	keyPath []string
	unknown bool // inside a module call with count/for_each
	locals  map[string]hcl.Expression
	stack   map[string]bool
}

// refSite is an expression whose traversals become edges, resolved after
// every node is known. From is a node ID, or empty when fromPrefix names a
// module whose managed resources are all sources of the edge.
type refSite struct {
	sc         *scope
	from       string
	fromPrefix string
	attr       string
	hint       string
	expr       hcl.Expression
}

type nodeRef struct {
	rootDir string
	addr    string
	id      string
	managed bool
}

type run struct {
	opts       source.Options
	root       string
	parser     *hclparse.Parser
	dirs       map[string][]string // dir -> files
	files      map[string]*parsedFile
	sources    map[string]model.Source
	nodes      []model.RawNode
	nodeIDs    map[string]bool
	refs       []nodeRef
	sites      []refSite
	gaps       map[[2]string]int
	modulesFor map[string]map[string]string // root dir -> module key -> dir
	edges      map[edgeKey]*model.RawEdge
	edgeOrder  []edgeKey
	parsedDirs map[string]bool
}

type edgeKey struct{ from, to, attr, hint string }

func (detector) Detect(ctx context.Context, idx walk.Index, opts source.Options) (model.Facts, error) {
	r := &run{
		opts:       opts,
		root:       idx.Root,
		parser:     hclparse.NewParser(),
		dirs:       map[string][]string{},
		files:      map[string]*parsedFile{},
		sources:    map[string]model.Source{},
		nodeIDs:    map[string]bool{},
		gaps:       map[[2]string]int{},
		modulesFor: map[string]map[string]string{},
		edges:      map[edgeKey]*model.RawEdge{},
		parsedDirs: map[string]bool{},
	}
	for _, f := range idx.Files[model.KindTerraform] {
		if !isConfigFile(f) {
			continue
		}
		d := path.Dir(f)
		r.dirs[d] = append(r.dirs[d], f)
	}
	dirs := make([]string, 0, len(r.dirs))
	for d := range r.dirs {
		sort.Strings(r.dirs[d])
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)

	// Phase 0: find directories called as local modules; they are not roots.
	called := map[string]bool{}
	for _, d := range dirs {
		for _, f := range r.dirs[d] {
			pf := r.parse(f)
			for _, m := range pf.modules {
				src, ok := literalString(attrExpr(m.Body, "source"))
				if !ok || !isLocalSource(src) {
					continue
				}
				if t, ok := joinWithin(d, src); ok {
					called[t] = true
				}
			}
		}
	}
	isRoot := func(d string) bool {
		return !called[d] && !inDotTerraform(d)
	}
	for _, d := range dirs {
		if err := ctx.Err(); err != nil {
			return model.Facts{}, err
		}
		if isRoot(d) {
			r.walkScope(&scope{rootDir: d, dir: d, stack: map[string]bool{}})
		}
	}
	// Directories only reachable through a module cycle become roots.
	for _, d := range dirs {
		if !r.parsedDirs[d] && !inDotTerraform(d) {
			r.walkScope(&scope{rootDir: d, dir: d, stack: map[string]bool{}})
		}
	}

	// Phase 2: references.
	for _, s := range r.sites {
		r.resolveSite(s)
	}

	var facts model.Facts
	for _, p := range sortedKeys(r.sources) {
		facts.Sources = append(facts.Sources, r.sources[p])
	}
	facts.RawNodes = r.nodes
	for _, k := range r.edgeOrder {
		facts.RawEdges = append(facts.RawEdges, *r.edges[k])
	}
	for k, n := range r.gaps {
		facts.Gaps = append(facts.Gaps, model.Gap{
			Kind: model.GapUnresolvedModule, Subject: k[0], Repo: opts.Repo, Path: k[1], Count: n,
		})
	}
	facts.Sort()
	return facts, nil
}

func isConfigFile(f string) bool {
	l := strings.ToLower(f)
	return strings.HasSuffix(l, ".tf") || strings.HasSuffix(l, ".tofu") ||
		strings.HasSuffix(l, ".tf.json") || strings.HasSuffix(l, ".tofu.json")
}

func inDotTerraform(d string) bool {
	for _, seg := range strings.Split(d, "/") {
		if seg == ".terraform" {
			return true
		}
	}
	return false
}

func isLocalSource(s string) bool {
	return strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../")
}

// joinWithin joins a relative path to a repo-relative dir and reports
// whether the result stays inside the repository.
func joinWithin(dir, rel string) (string, bool) {
	j := path.Join(dir, rel)
	if j == ".." || strings.HasPrefix(j, "../") || path.IsAbs(j) {
		return "", false
	}
	return j, true
}

// parse reads and parses a file once, recording its source status.
func (r *run) parse(rel string) *parsedFile {
	if pf, ok := r.files[rel]; ok {
		return pf
	}
	pf := &parsedFile{path: rel}
	r.files[rel] = pf
	src, err := os.ReadFile(filepath.Join(r.root, filepath.FromSlash(rel)))
	if err != nil {
		r.sources[rel] = source.FailedSource(r.opts.Repo, rel, model.KindTerraform, noteRead)
		return pf
	}
	pf.lines = strings.Split(string(src), "\n")
	var file *hcl.File
	var diags hcl.Diagnostics
	if strings.HasSuffix(strings.ToLower(rel), ".json") {
		file, diags = r.parser.ParseJSON(src, rel)
	} else {
		file, diags = r.parser.ParseHCL(src, rel)
	}
	if file == nil || file.Body == nil {
		r.sources[rel] = source.FailedSource(r.opts.Repo, rel, model.KindTerraform, noteFailed)
		return pf
	}
	content, _, cdiags := file.Body.PartialContent(topSchema)
	status, note := model.StatusParsed, ""
	if diags.HasErrors() {
		if bodyEmpty(file.Body, content) {
			r.sources[rel] = source.FailedSource(r.opts.Repo, rel, model.KindTerraform, noteFailed)
			return pf
		}
		status, note = model.StatusPartial, notePartial
	} else if cdiags.HasErrors() {
		status, note = model.StatusPartial, notePartial
	}
	pf.ok = true
	for _, b := range content.Blocks {
		if b.Type == "module" {
			pf.modules = append(pf.modules, b)
		}
		pf.blocks = append(pf.blocks, b)
	}
	r.sources[rel] = model.Source{Repo: r.opts.Repo, Path: rel, Kind: model.KindTerraform, Status: status, Note: note}
	return pf
}

func bodyEmpty(b hcl.Body, content *hcl.BodyContent) bool {
	if sb, ok := b.(*hclsyntax.Body); ok {
		return len(sb.Blocks) == 0 && len(sb.Attributes) == 0
	}
	return content == nil || len(content.Blocks) == 0
}

// walkScope collects nodes, reference sites and module calls of one module
// instance, recursing into called modules.
func (r *run) walkScope(sc *scope) {
	r.parsedDirs[sc.dir] = true
	sc.stack[sc.dir] = true
	defer delete(sc.stack, sc.dir)
	sc.locals = map[string]hcl.Expression{}
	files := r.dirs[sc.dir]
	var pfs []*parsedFile
	for _, f := range files {
		pf := r.parse(f)
		if pf.ok {
			pfs = append(pfs, pf)
		}
	}
	for _, pf := range pfs {
		for _, b := range pf.blocks {
			if b.Type != "locals" {
				continue
			}
			attrs, _ := b.Body.JustAttributes()
			for n, a := range attrs {
				sc.locals[n] = a.Expr
			}
		}
	}
	for _, pf := range pfs {
		for _, b := range pf.blocks {
			switch b.Type {
			case "resource", "data":
				r.addResource(sc, pf, b)
			}
		}
	}
	for _, pf := range pfs {
		for _, b := range pf.modules {
			r.addModule(sc, pf, b)
		}
	}
}

func (r *run) id(rootDir, addr string) string {
	return model.CanonicalID(r.opts.Repo, rootDir, addr)
}

func (r *run) evidence(pf *parsedFile, line int) model.Evidence {
	text := ""
	if line >= 1 && line <= len(pf.lines) {
		text = strings.TrimRight(pf.lines[line-1], "\r")
	}
	return model.NewEvidence(r.opts.Repo, pf.path, line, redact.Line(text), model.KindTerraform)
}

func (r *run) evidenceAt(rng hcl.Range) model.Evidence {
	pf := r.files[rng.Filename]
	if pf == nil {
		return model.NewEvidence(r.opts.Repo, rng.Filename, rng.Start.Line, "", model.KindTerraform)
	}
	return r.evidence(pf, rng.Start.Line)
}

func (r *run) addResource(sc *scope, pf *parsedFile, b *hcl.Block) {
	typ, name := b.Labels[0], b.Labels[1]
	addr := sc.prefix + typ + "." + name
	if b.Type == "data" {
		addr = sc.prefix + "data." + typ + "." + name
	}
	id := r.id(sc.rootDir, addr)
	if r.nodeIDs[id] {
		return
	}
	r.nodeIDs[id] = true

	mult := model.MultOne
	if sc.unknown {
		mult = model.MultUnknown
	}
	attrs := map[string]any{}
	items := bodyItems(b.Body)
	for _, it := range items {
		if it.name == "count" || it.name == "for_each" {
			mult = model.MultUnknown
		}
		if it.name == "depends_on" && it.expr != nil {
			r.sites = append(r.sites, refSite{sc: sc, from: id, attr: "depends_on", hint: "ordering", expr: it.expr})
			continue
		}
		if resourceMetaArgs[it.name] {
			continue
		}
		r.collectSites(sc, id, it, it.name)
		attrs[it.name] = redact.ResourceValue(typ, it.name, it.value())
	}
	n := model.RawNode{
		ID:            id,
		NodeKind:      model.NodeResource,
		SourceKind:    model.KindTerraform,
		Type:          typ,
		NativeAddress: addr,
		Attributes:    attrs,
		Multiplicity:  mult,
		Evidence:      []model.Evidence{r.evidence(pf, b.DefRange.Start.Line)},
	}
	for _, k := range identityKeys {
		if s, ok := attrs[k].(string); ok && s != "" {
			n.ProviderIdentity = providerOf(typ) + ":" + typ + "/" + s
			break
		}
	}
	r.nodes = append(r.nodes, n)
	r.refs = append(r.refs, nodeRef{rootDir: sc.rootDir, addr: addr, id: id, managed: b.Type == "resource"})
}

func (r *run) collectSites(sc *scope, from string, it item, attrPath string) {
	if it.expr != nil {
		r.sites = append(r.sites, refSite{sc: sc, from: from, attr: attrPath, expr: it.expr})
		return
	}
	for _, blk := range it.blocks {
		for _, sub := range blk {
			r.collectSites(sc, from, sub, attrPath+"."+sub.name)
		}
	}
}

func providerOf(typ string) string {
	if i := strings.Index(typ, "_"); i > 0 {
		return typ[:i]
	}
	return typ
}

func (r *run) addModule(sc *scope, pf *parsedFile, b *hcl.Block) {
	name := b.Labels[0]
	attrs, _ := b.Body.JustAttributes()
	srcExpr := attrExpr(b.Body, "source")
	src, ok := literalString(srcExpr)
	if !ok {
		subject := "<non-literal source>"
		r.gaps[[2]string{subject, pf.path}]++
		return
	}
	keyPath := append(append([]string(nil), sc.keyPath...), name)
	var dir string
	resolved := false
	if isLocalSource(src) {
		if d, ok := joinWithin(sc.dir, src); ok && len(r.dirs[d]) > 0 {
			dir, resolved = d, true
		}
	} else if d, ok := r.vendoredDir(sc.rootDir, strings.Join(keyPath, ".")); ok && len(r.dirs[d]) > 0 {
		dir, resolved = d, true
	}
	if !resolved || sc.stack[dir] || len(keyPath) > maxModuleDepth {
		r.gaps[[2]string{src, pf.path}]++
		return
	}
	child := &scope{
		rootDir: sc.rootDir,
		dir:     dir,
		prefix:  sc.prefix + "module." + name + ".",
		keyPath: keyPath,
		unknown: sc.unknown,
		stack:   sc.stack,
	}
	if attrs["count"] != nil || attrs["for_each"] != nil {
		child.unknown = true
	}
	r.walkScope(child)
	for _, an := range sortedKeys(attrs) {
		a := attrs[an]
		if an == "depends_on" {
			r.sites = append(r.sites, refSite{sc: sc, fromPrefix: child.prefix, attr: "depends_on", hint: "ordering", expr: a.Expr})
			continue
		}
		if moduleMetaArgs[an] {
			continue
		}
		r.sites = append(r.sites, refSite{sc: sc, fromPrefix: child.prefix, attr: "module." + name + "." + an, expr: a.Expr})
	}
}

// vendoredDir looks a module key up in <rootDir>/.terraform/modules/modules.json.
func (r *run) vendoredDir(rootDir, key string) (string, bool) {
	m, ok := r.modulesFor[rootDir]
	if !ok {
		m = map[string]string{}
		r.modulesFor[rootDir] = m
		p := filepath.Join(r.root, filepath.FromSlash(path.Join(rootDir, ".terraform/modules/modules.json")))
		if b, err := os.ReadFile(p); err == nil {
			var doc struct {
				Modules []struct {
					Key string `json:"Key"`
					Dir string `json:"Dir"`
				} `json:"Modules"`
			}
			if json.Unmarshal(b, &doc) == nil {
				for _, e := range doc.Modules {
					if e.Key == "" || e.Dir == "" {
						continue
					}
					d := strings.ReplaceAll(e.Dir, "\\", "/")
					if path.IsAbs(d) {
						continue
					}
					if j, ok := joinWithin(rootDir, d); ok {
						m[e.Key] = j
					}
				}
			}
		}
	}
	d, ok := m[key]
	return d, ok
}

// resolveSite turns every traversal of a site into edges.
func (r *run) resolveSite(s refSite) {
	var froms []string
	if s.from != "" {
		froms = []string{s.from}
	} else {
		froms = r.managedUnder(s.sc.rootDir, s.fromPrefix)
	}
	if len(froms) == 0 {
		return
	}
	for _, tr := range s.expr.Variables() {
		targets := r.resolveTraversal(s.sc, tr, 0)
		if len(targets) == 0 {
			continue
		}
		ev := r.evidenceAt(tr.SourceRange())
		for _, from := range froms {
			for _, to := range targets {
				if from == to {
					continue
				}
				r.addEdge(from, to, s.attr, s.hint, ev)
			}
		}
	}
}

func (r *run) addEdge(from, to, attr, hint string, ev model.Evidence) {
	k := edgeKey{from, to, attr, hint}
	if e, ok := r.edges[k]; ok {
		e.Evidence = append(e.Evidence, ev)
		return
	}
	r.edges[k] = &model.RawEdge{From: from, To: to, Attribute: attr, KindHint: hint, Evidence: []model.Evidence{ev}}
	r.edgeOrder = append(r.edgeOrder, k)
}

// resolveTraversal returns the node IDs a traversal refers to in a scope.
func (r *run) resolveTraversal(sc *scope, tr hcl.Traversal, depth int) []string {
	if len(tr) == 0 {
		return nil
	}
	root := tr.RootName()
	attrAt := func(i int) (string, bool) {
		if i >= len(tr) {
			return "", false
		}
		a, ok := tr[i].(hcl.TraverseAttr)
		return a.Name, ok
	}
	switch {
	case ignoredRoots[root]:
		return nil
	case root == "local":
		name, ok := attrAt(1)
		if !ok || depth > 0 {
			return nil
		}
		expr := sc.locals[name]
		if expr == nil {
			return nil
		}
		var out []string
		for _, t := range expr.Variables() {
			if t.RootName() == "local" {
				continue
			}
			out = append(out, r.resolveTraversal(sc, t, depth+1)...)
		}
		return uniqueSorted(out)
	case root == "data":
		typ, ok1 := attrAt(1)
		name, ok2 := attrAt(2)
		if !ok1 || !ok2 {
			return nil
		}
		return []string{r.id(sc.rootDir, sc.prefix+"data."+typ+"."+name)}
	case root == "module":
		name, ok := attrAt(1)
		if !ok {
			return nil
		}
		return r.managedUnder(sc.rootDir, sc.prefix+"module."+name+".")
	case strings.Contains(root, "_"):
		name, ok := attrAt(1)
		if !ok {
			return nil
		}
		return []string{r.id(sc.rootDir, sc.prefix+root+"."+name)}
	}
	return nil
}

// managedUnder returns the IDs of managed resources whose address starts with
// prefix in the given root module, sorted.
func (r *run) managedUnder(rootDir, prefix string) []string {
	var out []string
	for _, n := range r.refs {
		if n.managed && n.rootDir == rootDir && strings.HasPrefix(n.addr, prefix) {
			out = append(out, n.id)
		}
	}
	sort.Strings(out)
	return out
}

// item is an attribute (expr set) or a group of nested blocks of one type.
type item struct {
	name    string
	expr    hcl.Expression
	blocks  [][]item // one entry per repeated block
	dynamic bool
}

func (it item) value() any {
	switch {
	case it.dynamic:
		return model.Unknown
	case it.expr != nil:
		return literal(it.expr)
	}
	vals := make([]any, 0, len(it.blocks))
	for _, blk := range it.blocks {
		m := map[string]any{}
		for _, sub := range blk {
			m[sub.name] = sub.value()
		}
		vals = append(vals, m)
	}
	if len(vals) == 1 {
		return vals[0]
	}
	return vals
}

// bodyItems lists a block body's attributes and nested blocks in a stable
// order. Native syntax bodies keep nested blocks; JSON bodies expose them as
// object-valued attributes.
func bodyItems(b hcl.Body) []item {
	var out []item
	if sb, ok := b.(*hclsyntax.Body); ok {
		for _, n := range sortedKeys(sb.Attributes) {
			out = append(out, item{name: n, expr: sb.Attributes[n].Expr})
		}
		groups := map[string]*item{}
		var order []string
		for _, blk := range sb.Blocks {
			name := blk.Type
			if blk.Type == "dynamic" && len(blk.Labels) > 0 {
				name = blk.Labels[0]
			}
			g, ok := groups[name]
			if !ok {
				g = &item{name: name}
				groups[name] = g
				order = append(order, name)
			}
			if blk.Type == "dynamic" {
				g.dynamic = true
				continue
			}
			g.blocks = append(g.blocks, bodyItems(blk.Body))
		}
		sort.Strings(order)
		for _, n := range order {
			if _, clash := sb.Attributes[n]; clash {
				continue
			}
			out = append(out, *groups[n])
		}
		return out
	}
	attrs, _ := b.JustAttributes()
	for _, n := range sortedKeys(attrs) {
		out = append(out, item{name: n, expr: attrs[n].Expr})
	}
	return out
}

func attrExpr(b hcl.Body, name string) hcl.Expression {
	if sb, ok := b.(*hclsyntax.Body); ok {
		if a := sb.Attributes[name]; a != nil {
			return a.Expr
		}
		return nil
	}
	attrs, _ := b.JustAttributes()
	if a := attrs[name]; a != nil {
		return a.Expr
	}
	return nil
}

func literalString(e hcl.Expression) (string, bool) {
	if e == nil {
		return "", false
	}
	s, ok := literal(e).(string)
	return s, ok
}

// literal evaluates an expression without any variables or functions;
// anything else is model.Unknown.
func literal(e hcl.Expression) any {
	if len(e.Variables()) > 0 {
		return model.Unknown
	}
	v, diags := e.Value(nil)
	if diags.HasErrors() {
		return model.Unknown
	}
	return ctyToGo(v)
}

func ctyToGo(v cty.Value) any {
	if v.IsMarked() {
		v, _ = v.Unmark()
	}
	if !v.IsWhollyKnown() {
		return model.Unknown
	}
	if v.IsNull() {
		return nil
	}
	t := v.Type()
	switch {
	case t == cty.String:
		return v.AsString()
	case t == cty.Bool:
		return v.True()
	case t == cty.Number:
		bf := v.AsBigFloat()
		if bf.IsInt() {
			if i, acc := bf.Int64(); acc == big.Exact {
				return i
			}
		}
		f, _ := bf.Float64()
		return f
	case t.IsListType() || t.IsTupleType() || t.IsSetType():
		out := []any{}
		for it := v.ElementIterator(); it.Next(); {
			_, e := it.Element()
			out = append(out, ctyToGo(e))
		}
		return out
	case t.IsMapType() || t.IsObjectType():
		out := map[string]any{}
		for it := v.ElementIterator(); it.Next(); {
			k, e := it.Element()
			out[k.AsString()] = ctyToGo(e)
		}
		return out
	}
	return model.Unknown
}

func uniqueSorted(s []string) []string {
	sort.Strings(s)
	out := s[:0]
	for i, x := range s {
		if i > 0 && x == s[i-1] {
			continue
		}
		out = append(out, x)
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
