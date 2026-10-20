package graph

import (
	"fmt"
	"sort"
	"strings"

	"github.com/cloudowski/exitctl/internal/catalog"
	"github.com/cloudowski/exitctl/internal/model"
	"github.com/cloudowski/exitctl/internal/rules"
	"github.com/cloudowski/exitctl/internal/sdkname"
)

// ImpliedPrefix prefixes the IDs of implied service nodes: "implied//<service>".
const ImpliedPrefix = "implied//"

// Node note prefixes.
const (
	NoteUndeterminedRule = "undetermined_rule:" // + rule ID
	NoteUnverifiedMatch  = "unverified_match:"  // + draft entry ID
)

// SDKTraitDefault is the trait used for SDK coupling when a pattern names none.
const SDKTraitDefault = "proprietary-sdk"

// Build constructs the dependency graph from facts and a catalog index,
// implementing data-model.md 3.3 steps 1-6. ix may be nil, in which case every
// resource is classified as unknown and no catalog-driven edges or findings
// are produced. The result is fully sorted and deterministic.
func Build(facts model.Facts, ix *catalog.Index) *Graph {
	b := &builder{
		facts:    facts,
		ix:       ix,
		nodes:    map[string]*Node{},
		alias:    map[string]string{},
		decl:     map[string][]model.Evidence{},
		edges:    map[edgeKey]*Edge{},
		gaps:     map[gapKey]int{},
		sdkTrait: map[[2]string]*catalog.TraitRef{},
		bound:    map[string]bool{},
	}
	b.merge()
	b.classify()
	b.rawEdges()
	b.bindings()
	b.codeBridge()
	b.finishImplied()
	g := b.assemble()
	b.findings(g)
	return g
}

type edgeKey struct {
	from, to string
	kind     EdgeKind
}

type gapKey struct {
	kind                model.GapKind
	subject, repo, path string
	// provider and service qualify unknown_sdk gaps; they follow from the
	// subject, so they never split a gap.
	provider, service string
}

type builder struct {
	facts model.Facts
	ix    *catalog.Index

	nodes    map[string]*Node            // by node ID
	alias    map[string]string           // any raw ID -> node ID
	decl     map[string][]model.Evidence // node ID -> declaration evidence (first of every raw node)
	sk       map[string]model.SourceKind // node ID -> primary source kind
	raws     map[string][]model.RawNode  // node ID -> merged raw nodes sorted by ID
	edges    map[edgeKey]*Edge
	gaps     map[gapKey]int
	sdkTrait map[[2]string]*catalog.TraitRef // (component, service) -> SDK trait ref
	bound    map[string]bool                 // implied node IDs reached by binding edges
}

// ---------------------------------------------------------------- step 1

func isK8s(k model.SourceKind) bool {
	return k == model.KindKubernetes || k == model.KindHelm || k == model.KindKustomize
}

func idRepo(id string) string {
	if i := strings.Index(id, "//"); i >= 0 {
		return id[:i]
	}
	return ""
}

func rawRepo(rn model.RawNode) string {
	if len(rn.Evidence) > 0 && rn.Evidence[0].Repo != "" {
		return rn.Evidence[0].Repo
	}
	return idRepo(rn.ID)
}

func rawSource(rn model.RawNode) string {
	if len(rn.Evidence) > 0 {
		return rawRepo(rn) + "//" + rn.Evidence[0].Path
	}
	if i := strings.Index(rn.ID, "#"); i >= 0 {
		return rn.ID[:i]
	}
	return rn.ID
}

func (b *builder) merge() {
	raws := append([]model.RawNode(nil), b.facts.RawNodes...)
	sort.SliceStable(raws, func(i, j int) bool {
		if raws[i].ID != raws[j].ID {
			return raws[i].ID < raws[j].ID
		}
		// Same canonical ID: order by declaration evidence so the primary
		// declaration does not depend on input order.
		ei, ej := raws[i].Evidence, raws[j].Evidence
		switch {
		case len(ei) == 0 || len(ej) == 0:
			return len(ei) > len(ej)
		case ei[0] != ej[0]:
			return ei[0].Less(ej[0])
		}
		return raws[i].SourceKind < raws[j].SourceKind
	})

	parent := make([]int, len(raws))
	for i := range parent {
		parent[i] = i
	}
	find := func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}
	union := func(a, c int) {
		ra, rc := find(a), find(c)
		if ra == rc {
			return
		}
		if ra < rc {
			parent[rc] = ra
		} else {
			parent[ra] = rc
		}
	}
	byID := map[string]int{}
	byPI := map[string]int{}
	byK8s := map[[2]string]int{}
	for i, rn := range raws {
		if j, ok := byID[rn.ID]; ok {
			union(i, j)
		} else {
			byID[rn.ID] = i
		}
		if rn.ProviderIdentity != "" {
			if j, ok := byPI[rn.ProviderIdentity]; ok {
				union(i, j)
			} else {
				byPI[rn.ProviderIdentity] = i
			}
		}
		if rn.NodeKind != model.NodeComponent && isK8s(rn.SourceKind) && rn.NativeAddress != "" {
			k := [2]string{rawRepo(rn), rn.NativeAddress}
			if j, ok := byK8s[k]; ok {
				union(i, j)
			} else {
				byK8s[k] = i
			}
		}
	}
	groups := map[int][]int{}
	var roots []int
	for i := range raws {
		r := find(i)
		if _, ok := groups[r]; !ok {
			roots = append(roots, r)
		}
		groups[r] = append(groups[r], i)
	}
	b.sk = map[string]model.SourceKind{}
	b.raws = map[string][]model.RawNode{}
	for _, r := range roots {
		members := groups[r] // ascending index = ascending raw ID
		p := raws[members[0]]
		n := &Node{
			ID:            p.ID,
			Kind:          NodeResource,
			Type:          p.Type,
			NativeAddress: p.NativeAddress,
			Repo:          rawRepo(p),
			Multiplicity:  p.Multiplicity,
			Attributes:    map[string]any{},
		}
		if p.NodeKind == model.NodeComponent {
			n.Kind = NodeComponent
			n.Class = ClassComponent
		}
		var rest []model.Evidence
		var decl []model.Evidence
		srcSet := map[string]bool{}
		skSet := map[model.SourceKind]bool{}
		for k, mi := range members {
			rn := raws[mi]
			b.raws[n.ID] = append(b.raws[n.ID], rn)
			b.alias[rn.ID] = n.ID
			if rn.ID != n.ID && (len(n.Aliases) == 0 || n.Aliases[len(n.Aliases)-1] != rn.ID) {
				n.Aliases = append(n.Aliases, rn.ID)
			}
			if n.ProviderIdentity == "" {
				n.ProviderIdentity = rn.ProviderIdentity
			}
			if rn.Multiplicity == model.MultUnknown {
				n.Multiplicity = model.MultUnknown
			}
			for key, v := range rn.Attributes {
				if _, ok := n.Attributes[key]; !ok {
					n.Attributes[key] = v
				}
			}
			srcSet[rawSource(rn)] = true
			skSet[rn.SourceKind] = true
			for e, ev := range rn.Evidence {
				if k == 0 && e == 0 {
					continue
				}
				rest = append(rest, ev)
			}
			if len(rn.Evidence) > 0 {
				decl = append(decl, rn.Evidence[0])
			}
		}
		if len(p.Evidence) > 0 {
			first := p.Evidence[0]
			rest = model.SortEvidence(rest)
			n.Evidence = append([]model.Evidence{first}, without(rest, first)...)
		}
		b.decl[n.ID] = model.SortEvidence(decl)
		for s := range srcSet {
			n.Sources = append(n.Sources, s)
		}
		sort.Strings(n.Sources)
		for s := range skSet {
			n.SourceKinds = append(n.SourceKinds, s)
		}
		sort.Slice(n.SourceKinds, func(i, j int) bool { return n.SourceKinds[i] < n.SourceKinds[j] })
		b.sk[n.ID] = p.SourceKind
		b.nodes[n.ID] = n
	}
}

func without(ev []model.Evidence, x model.Evidence) []model.Evidence {
	out := ev[:0:0]
	for _, e := range ev {
		if e != x {
			out = append(out, e)
		}
	}
	return out
}

// ---------------------------------------------------------------- step 2

// TypeRefOf returns the catalog type reference of a raw node.
func TypeRefOf(rn model.RawNode) catalog.TypeRef {
	switch rn.Type {
	case model.TypeContainerImage:
		return catalog.TypeRef{SourceFormat: "container_registry", Type: attrString(rn.Attributes, "registry")}
	case model.TypeCIAction:
		return catalog.TypeRef{SourceFormat: "ci_action", Type: attrString(rn.Attributes, "action")}
	}
	switch {
	case rn.SourceKind == model.KindTerraform, rn.SourceKind == model.KindPulumi:
		// Pulumi resources are named by their Terraform type: the providers
		// are bridged from the Terraform ones.
		return catalog.TypeRef{SourceFormat: "terraform", Type: rn.Type}
	case rn.SourceKind == model.KindCloudFormation, rn.SourceKind == model.KindCDK:
		return catalog.TypeRef{SourceFormat: "cloudformation", Type: rn.Type}
	case isK8s(rn.SourceKind):
		return catalog.TypeRef{SourceFormat: "kubernetes", Type: rn.Type}
	}
	return catalog.TypeRef{SourceFormat: string(rn.SourceKind), Type: rn.Type}
}

func attrString(attrs map[string]any, key string) string {
	if s, ok := attrs[key].(string); ok {
		return s
	}
	return ""
}

func classRank(c string) int {
	switch c {
	case ClassService:
		return 3
	case ClassNeutral:
		return 2
	case ClassUnverified:
		return 1
	}
	return 0
}

// gapSubject names an unknown type for coverage gaps.
func gapSubject(tr catalog.TypeRef, rawType string) string {
	switch tr.SourceFormat {
	case "container_registry", "ci_action":
		return rawType + ":" + tr.Type
	}
	return rawType
}

func (b *builder) classify() {
	type unk struct{ repo, subject string }
	unknown := map[unk]int{}
	for _, id := range sortedKeys(b.nodes) {
		n := b.nodes[id]
		if n.Kind != NodeResource {
			continue
		}
		best := catalog.Classification{Class: ClassUnknown}
		for _, rn := range b.raws[id] {
			c := b.classifyRef(TypeRefOf(rn))
			if classRank(c.Class) > classRank(best.Class) {
				best = c
			}
		}
		n.Class = best.Class
		switch best.Class {
		case ClassService:
			if best.Service != nil {
				n.Service = best.Service.ID
			}
		case ClassUnverified:
			if best.Service != nil {
				n.Notes = append(n.Notes, NoteUnverifiedMatch+best.Service.ID)
			} else if best.Neutral != nil {
				n.Notes = append(n.Notes, NoteUnverifiedMatch+best.Neutral.ID)
			}
		case ClassNeutral:
			if best.Neutral != nil && !best.Neutral.Counts() {
				n.Class = ClassContext
			}
		default:
			n.Class = ClassUnknown
			p := b.raws[id][0]
			unknown[unk{n.Repo, gapSubject(TypeRefOf(p), p.Type)}]++
		}
	}
	for k, c := range unknown {
		b.gaps[gapKey{kind: model.GapUnknownType, subject: k.subject, repo: k.repo}] += c
	}
}

func (b *builder) classifyRef(tr catalog.TypeRef) catalog.Classification {
	if b.ix == nil || tr.Type == "" {
		return catalog.Classification{Class: ClassUnknown}
	}
	c := b.ix.Classify(tr)
	if c.Class == "" {
		c.Class = ClassUnknown
	}
	return c
}

// ---------------------------------------------------------------- step 3

func (b *builder) addEdge(from, to string, kind EdgeKind, conf string, ev []model.Evidence) {
	if from == to {
		return
	}
	k := edgeKey{from, to, kind}
	e, ok := b.edges[k]
	if !ok {
		e = &Edge{From: from, To: to, Kind: kind, Confidence: conf}
		b.edges[k] = e
	} else if confRank(conf) > confRank(e.Confidence) {
		e.Confidence = conf
	}
	e.Evidence = append(e.Evidence, ev...)
}

func confRank(c string) int {
	switch c {
	case ConfExact:
		return 3
	case ConfResourceLevel:
		return 2
	case ConfServiceLevel:
		return 1
	}
	return 0
}

func (b *builder) rawEdges() {
	for _, re := range b.facts.RawEdges {
		from, okF := b.alias[re.From]
		to, okT := b.alias[re.To]
		if !okF || !okT {
			subject, repo := re.To, idRepo(re.From)
			if !okF && okT {
				subject = re.From
			}
			if okF {
				repo = b.nodes[from].Repo
			}
			path := ""
			if len(re.Evidence) > 0 {
				path = re.Evidence[0].Path
				if re.Evidence[0].Repo != "" {
					repo = re.Evidence[0].Repo
				}
			}
			b.gaps[gapKey{model.GapUnresolvedReference, subject, repo, path, "", ""}]++
			continue
		}
		kind := EdgeKind(re.KindHint)
		if kind == "" && b.ix != nil && re.Attribute != "" {
			kind = EdgeKind(b.ix.EdgeKindFor(b.nodes[from].Type, re.Attribute))
		}
		if kind == "" {
			kind = EdgeReference
		}
		b.addEdge(from, to, kind, ConfExact, re.Evidence)
	}
}

// ---------------------------------------------------------------- step 4

// serviceTargets returns resource nodes of a service, preferring the given
// repo; when the repo has none, resources of every repo are returned.
func (b *builder) serviceTargets(svc, repo string) []string {
	var same, all []string
	for _, id := range sortedKeys(b.nodes) {
		n := b.nodes[id]
		if n.Kind != NodeResource || n.Class != ClassService || n.Service != svc {
			continue
		}
		all = append(all, id)
		if n.Repo == repo {
			same = append(same, id)
		}
	}
	if len(same) > 0 {
		return same
	}
	return all
}

func (b *builder) implied(svc string) string {
	id := ImpliedPrefix + svc
	if _, ok := b.nodes[id]; !ok {
		b.nodes[id] = &Node{
			ID:         id,
			Kind:       NodeImpliedService,
			Type:       svc,
			Service:    svc,
			Class:      ClassService,
			Attributes: map[string]any{},
		}
		b.alias[id] = id
	}
	return id
}

func (b *builder) bindings() {
	if b.ix == nil {
		return
	}
	binds := b.ix.Bindings()
	if len(binds) == 0 {
		return
	}
	for _, id := range sortedKeys(b.nodes) {
		n := b.nodes[id]
		if n.Kind != NodeResource || !hasK8s(n.SourceKinds) {
			continue
		}
		for _, bd := range binds {
			ok, hint := bindingMatches(bd.Match, n)
			if !ok || bd.Service == "" {
				continue
			}
			ev := []model.Evidence{pickEvidence(n, hint)}
			if len(n.Evidence) == 0 {
				ev = nil
			}
			targets := b.serviceTargets(bd.Service, n.Repo)
			if len(targets) == 0 {
				t := b.implied(bd.Service)
				b.bound[t] = true
				targets = []string{t}
			}
			for _, t := range targets {
				b.addEdge(id, t, EdgeBinding, ConfExact, ev)
			}
		}
	}
}

func hasK8s(kinds []model.SourceKind) bool {
	for _, k := range kinds {
		if isK8s(k) {
			return true
		}
	}
	return false
}

// pickEvidence returns the node evidence whose excerpt contains hint, else
// the primary declaration.
func pickEvidence(n *Node, hint string) model.Evidence {
	if len(n.Evidence) == 0 {
		return model.Evidence{}
	}
	if hint != "" {
		for _, e := range n.Evidence {
			if strings.Contains(e.Excerpt, hint) {
				return e
			}
		}
	}
	return n.Evidence[0]
}

func splitK8sType(t string) (group, kind string) {
	i := strings.LastIndex(t, "/")
	if i < 0 {
		return "", t
	}
	return t[:i], t[i+1:]
}

// bindingMatches tests a binding against a Kubernetes node. It returns a hint
// (the matched annotation key or field name) used to pick evidence.
func bindingMatches(m catalog.BindingMatch, n *Node) (bool, string) {
	group, kind := splitK8sType(n.Type)
	if m.Kind != "" && m.Kind != "*" && m.Kind != kind {
		return false, ""
	}
	if m.Group != "" && m.Group != group {
		return false, ""
	}
	switch {
	case m.Annotation != "":
		anns, _ := lookupPath(n.Attributes, "metadata.annotations")
		var vals []any
		hint := m.Annotation
		for _, a := range anns {
			am, ok := a.(map[string]any)
			if !ok {
				continue
			}
			for _, k := range sortedKeys(am) {
				if keyMatches(m.Annotation, k) {
					vals = append(vals, am[k])
					hint = k
				}
			}
		}
		return valuesMatch(m, vals), strings.TrimSuffix(hint, "*")
	case m.Field != "":
		vals, _ := lookupPath(n.Attributes, m.Field)
		hint := m.Field
		if i := strings.LastIndex(hint, "."); i >= 0 {
			hint = hint[i+1:]
		}
		return valuesMatch(m, vals), hint
	}
	return true, ""
}

func keyMatches(pat, key string) bool {
	if p, ok := strings.CutSuffix(pat, "*"); ok {
		return strings.HasPrefix(key, p)
	}
	return pat == key
}

func valuesMatch(m catalog.BindingMatch, vals []any) bool {
	exists := len(vals) > 0
	if m.Exists != nil && *m.Exists != exists {
		return false
	}
	if m.Equals == "" && len(m.In) == 0 {
		return m.Exists != nil || exists
	}
	for _, v := range vals {
		if model.IsUnknown(v) || model.IsRedacted(v) {
			continue
		}
		s := fmt.Sprint(v)
		if m.Equals != "" && s == m.Equals {
			return true
		}
		for _, x := range m.In {
			if s == x {
				return true
			}
		}
	}
	return false
}

// lookupPath resolves a dotted attribute path. A literal key equal to the
// full path wins; otherwise maps are traversed segment by segment and lists
// match if any element matches. It returns every value found.
func lookupPath(attrs map[string]any, path string) ([]any, bool) {
	if v, ok := attrs[path]; ok {
		return []any{v}, true
	}
	var walk func(v any, segs []string) []any
	walk = func(v any, segs []string) []any {
		if len(segs) == 0 {
			return []any{v}
		}
		switch t := v.(type) {
		case map[string]any:
			// Prefer the longest literal dotted key (annotation keys contain dots).
			for k := len(segs); k >= 1; k-- {
				key := strings.Join(segs[:k], ".")
				if c, ok := t[key]; ok {
					if r := walk(c, segs[k:]); len(r) > 0 {
						return r
					}
				}
			}
		case []any:
			var out []any
			for _, e := range t {
				out = append(out, walk(e, segs)...)
			}
			return out
		}
		return nil
	}
	r := walk(attrs, strings.Split(path, "."))
	return r, len(r) > 0
}

// ---------------------------------------------------------------- step 5

// unknownSDKPackage records an unknown_sdk gap for a cloud SDK package that
// no catalog service claims. Packages whose names follow no SDK convention
// are not cloud SDKs as far as exitctl can tell and are left alone.
func (b *builder) unknownSDKPackage(pu model.PackageUse) {
	ref, ok := sdkname.Derive(pu.Ecosystem, pu.Name)
	if !ok {
		return
	}
	k := gapKey{kind: model.GapUnknownSDK, subject: string(pu.Ecosystem) + ":" + pu.Name,
		repo: repoOfID(pu.Component), path: pu.Manifest.Path, provider: ref.Provider, service: ref.Service}
	b.gaps[k]++
}

// unknownSDKClients records an unknown_sdk gap for every generic-client call
// (boto3.client("x")) that no catalog usage pattern turned into a service.
func (b *builder) unknownSDKClients() {
	type site struct {
		path string
		line int
	}
	mapped := map[site]bool{}
	for _, cu := range b.facts.CodeUses {
		if cu.Service != "" {
			mapped[site{cu.Evidence.Path, cu.Evidence.Line}] = true
		}
	}
	for _, cu := range b.facts.CodeUses {
		if cu.Service != "" || cu.PatternKind != model.CodeKindSDKName || mapped[site{cu.Evidence.Path, cu.Evidence.Line}] {
			continue
		}
		name := sdkname.Normalize(cu.Match)
		if name == "" {
			continue
		}
		k := gapKey{kind: model.GapUnknownSDK, subject: "boto3:" + name, repo: repoOfID(cu.Component),
			path: cu.Evidence.Path, provider: sdkname.AWS, service: name}
		b.gaps[k]++
	}
}

// repoOfID returns the repository part of a node ID "<repo>//<dir>#<address>".
func repoOfID(id string) string {
	if i := strings.Index(id, "//"); i >= 0 {
		return id[:i]
	}
	return ""
}

type usageKey struct{ component, service string }

func (b *builder) codeBridge() {
	type usage struct {
		ev       []model.Evidence
		literals []string // text of code matches and excerpts
		trait    *catalog.TraitRef
	}
	uses := map[usageKey]*usage{}
	get := func(k usageKey) *usage {
		u, ok := uses[k]
		if !ok {
			u = &usage{}
			uses[k] = u
		}
		return u
	}
	patterns := map[string][]catalog.ServicePattern{}
	pats := func(eco string) []catalog.ServicePattern {
		if b.ix == nil {
			return nil
		}
		p, ok := patterns[eco]
		if !ok {
			p = b.ix.SDKPatterns(eco)
			patterns[eco] = p
		}
		return p
	}
	for _, cu := range b.facts.CodeUses {
		if cu.Service == "" {
			continue
		}
		u := get(usageKey{cu.Component, cu.Service})
		u.ev = append(u.ev, cu.Evidence)
		u.literals = append(u.literals, cu.Match, cu.Evidence.Excerpt)
		if u.trait == nil {
			for _, sp := range pats(string(cu.Ecosystem)) {
				if sp.ServiceID == cu.Service && sp.Pattern.Trait != nil {
					u.trait = sp.Pattern.Trait
					break
				}
			}
		}
	}
	for _, pu := range b.facts.PackageUses {
		mapped := false
		for _, sp := range pats(string(pu.Ecosystem)) {
			matched := false
			for _, g := range sp.Pattern.Packages {
				if globMatch(g, pu.Name) {
					matched = true
					break
				}
			}
			for _, g := range sp.Pattern.Gate {
				if globMatch(g, pu.Name) {
					mapped = true // a generic client the catalog already knows
				}
			}
			if !matched {
				continue
			}
			mapped = true
			u := get(usageKey{pu.Component, sp.ServiceID})
			u.ev = append(u.ev, pu.Manifest)
			if u.trait == nil && sp.Pattern.Trait != nil {
				u.trait = sp.Pattern.Trait
			}
		}
		if !mapped && b.ix != nil {
			b.unknownSDKPackage(pu)
		}
	}
	b.unknownSDKClients()
	keys := make([]usageKey, 0, len(uses))
	for k := range uses {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].component != keys[j].component {
			return keys[i].component < keys[j].component
		}
		return keys[i].service < keys[j].service
	})
	for _, k := range keys {
		u := uses[k]
		comp := b.component(k.component, u.ev)
		b.sdkTrait[[2]string{comp, k.service}] = u.trait
		targets := b.serviceTargets(k.service, b.nodes[comp].Repo)
		if len(targets) == 0 {
			targets = []string{b.implied(k.service)}
		}
		for _, t := range targets {
			conf := ConfServiceLevel
			if name := identityName(b.nodes[t].ProviderIdentity); name != "" && containsQuoted(u.literals, name) {
				conf = ConfResourceLevel
			}
			b.addEdge(comp, t, EdgeUsage, conf, u.ev)
		}
	}
}

// component resolves a component ID to a node, creating a minimal component
// node when no detector emitted one.
func (b *builder) component(id string, ev []model.Evidence) string {
	if nid, ok := b.alias[id]; ok {
		return nid
	}
	n := &Node{
		ID:         id,
		Kind:       NodeComponent,
		Type:       model.TypeComponent,
		Class:      ClassComponent,
		Repo:       idRepo(id),
		Attributes: map[string]any{},
	}
	if len(ev) > 0 {
		e := model.SortEvidence(append([]model.Evidence(nil), ev...))[0]
		n.Evidence = []model.Evidence{e}
		if e.Repo != "" {
			n.Repo = e.Repo
		}
		n.Sources = []string{n.Repo + "//" + e.Path}
		b.decl[id] = []model.Evidence{e}
	}
	b.nodes[id] = n
	b.alias[id] = id
	return id
}

// identityName is the resource name part of a provider identity: the text
// after the last "/" (or after the last ":" when there is no "/").
func identityName(pi string) string {
	if pi == "" {
		return ""
	}
	if i := strings.LastIndex(pi, "/"); i >= 0 {
		return pi[i+1:]
	}
	if i := strings.LastIndex(pi, ":"); i >= 0 {
		return pi[i+1:]
	}
	return pi
}

func containsQuoted(texts []string, name string) bool {
	for _, t := range texts {
		for _, q := range []string{`"`, `'`, "`"} {
			if strings.Contains(t, q+name+q) {
				return true
			}
		}
	}
	return false
}

// globMatch matches s against a glob where "*" matches any sequence
// (including "/") and "?" matches one character.
func globMatch(pat, s string) bool {
	p, i := 0, 0
	star, mark := -1, 0
	for i < len(s) {
		switch {
		case p < len(pat) && (pat[p] == '?' || pat[p] == s[i]):
			p++
			i++
		case p < len(pat) && pat[p] == '*':
			star, mark = p, i
			p++
		case star >= 0:
			p = star + 1
			mark++
			i = mark
		default:
			return false
		}
	}
	for p < len(pat) && pat[p] == '*' {
		p++
	}
	return p == len(pat)
}

// finishImplied gives implied service nodes the evidence of their incoming
// edges, so findings always have a fallback.
func (b *builder) finishImplied() {
	for _, k := range sortedEdgeKeys(b.edges) {
		e := b.edges[k]
		n := b.nodes[e.To]
		if n.Kind != NodeImpliedService {
			continue
		}
		n.Evidence = append(n.Evidence, e.Evidence...)
		srcs := map[string]bool{}
		for _, s := range n.Sources {
			srcs[s] = true
		}
		for _, ev := range e.Evidence {
			srcs[ev.Repo+"//"+ev.Path] = true
		}
		n.Sources = n.Sources[:0]
		for s := range srcs {
			n.Sources = append(n.Sources, s)
		}
		sort.Strings(n.Sources)
	}
	for _, n := range b.nodes {
		if n.Kind == NodeImpliedService {
			n.Evidence = model.SortEvidence(n.Evidence)
			b.decl[n.ID] = n.Evidence
		}
	}
}

// ---------------------------------------------------------------- assembly

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedEdgeKeys(m map[edgeKey]*Edge) []edgeKey {
	out := make([]edgeKey, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		return edgeLess(out[i].from, out[i].to, out[i].kind, out[j].from, out[j].to, out[j].kind)
	})
	return out
}

func edgeLess(f1, t1 string, k1 EdgeKind, f2, t2 string, k2 EdgeKind) bool {
	if f1 != f2 {
		return f1 < f2
	}
	if t1 != t2 {
		return t1 < t2
	}
	return k1 < k2
}

func (b *builder) assemble() *Graph {
	g := &Graph{}
	for _, id := range sortedKeys(b.nodes) {
		g.Nodes = append(g.Nodes, b.nodes[id])
	}
	for _, k := range sortedEdgeKeys(b.edges) {
		e := b.edges[k]
		e.Evidence = model.SortEvidence(e.Evidence)
		g.Edges = append(g.Edges, e)
	}
	for k, c := range b.gaps {
		g.Gaps = append(g.Gaps, model.Gap{Kind: k.kind, Subject: k.subject, Repo: k.repo, Path: k.path, Count: c, Provider: k.provider, Service: k.service})
	}
	sort.Slice(g.Gaps, func(i, j int) bool {
		a, c := g.Gaps[i], g.Gaps[j]
		if a.Kind != c.Kind {
			return a.Kind < c.Kind
		}
		if a.Subject != c.Subject {
			return a.Subject < c.Subject
		}
		if a.Repo != c.Repo {
			return a.Repo < c.Repo
		}
		return a.Path < c.Path
	})
	g.link()
	return g
}

// link sorts nodes and edges, rebuilds Index, Out, and In, and resets the
// memoized algorithm state. Out and In are deduplicated and sorted by node ID.
func (g *Graph) link() {
	sort.SliceStable(g.Nodes, func(i, j int) bool { return g.Nodes[i].ID < g.Nodes[j].ID })
	sort.SliceStable(g.Edges, func(i, j int) bool {
		a, c := g.Edges[i], g.Edges[j]
		return edgeLess(a.From, a.To, a.Kind, c.From, c.To, c.Kind)
	})
	g.Index = make(map[string]int, len(g.Nodes))
	for i, n := range g.Nodes {
		for _, a := range n.Aliases {
			if _, ok := g.Index[a]; !ok {
				g.Index[a] = i
			}
		}
	}
	for i, n := range g.Nodes {
		g.Index[n.ID] = i
	}
	g.Out = make([][]int, len(g.Nodes))
	g.In = make([][]int, len(g.Nodes))
	for _, e := range g.Edges {
		f, okF := g.Index[e.From]
		t, okT := g.Index[e.To]
		if !okF || !okT || f == t {
			continue
		}
		g.Out[f] = append(g.Out[f], t)
		g.In[t] = append(g.In[t], f)
	}
	for i := range g.Nodes {
		g.Out[i] = sortDedup(g.Out[i])
		g.In[i] = sortDedup(g.In[i])
	}
	g.algo = nil
}

func sortDedup(s []int) []int {
	sort.Ints(s)
	out := s[:0]
	for i, v := range s {
		if i > 0 && v == s[i-1] {
			continue
		}
		out = append(out, v)
	}
	return out
}

// ---------------------------------------------------------------- step 6

func (b *builder) findings(g *Graph) {
	in := map[string][]*Edge{}
	out := map[string][]*Edge{}
	for _, e := range g.Edges {
		out[e.From] = append(out[e.From], e)
		in[e.To] = append(in[e.To], e)
	}
	for _, n := range g.Nodes {
		if n.Kind == NodeComponent {
			continue
		}
		fs := map[string]*Finding{}
		decl := b.decl[n.ID]
		if len(decl) == 0 && len(n.Evidence) > 0 {
			decl = n.Evidence[:1]
		}
		var svc *catalog.Service
		if b.ix != nil && n.Service != "" && n.Class == ClassService {
			svc = b.ix.ServiceByID(n.Service)
		}
		// Service traits on recognized resources; binding traits on implied
		// services reached by bindings.
		if svc != nil {
			origin := ""
			ev := decl
			switch {
			case n.Kind == NodeResource:
				origin = "service"
			case b.bound[n.ID]:
				origin = "binding"
				ev = nil
				for _, e := range in[n.ID] {
					if e.Kind == EdgeBinding {
						ev = append(ev, e.Evidence...)
					}
				}
			}
			if origin != "" {
				for _, tr := range svc.Traits {
					b.put(fs, n, tr.ID, tr.Level, origin, ev, decl)
				}
			}
		}
		// SDK traits on nodes reached by usage edges.
		for _, e := range in[n.ID] {
			if e.Kind != EdgeUsage {
				continue
			}
			tr := b.sdkTrait[[2]string{e.From, n.Service}]
			id, lvl := SDKTraitDefault, catalog.Level("")
			if tr != nil {
				id, lvl = tr.ID, tr.Level
			}
			b.put(fs, n, id, lvl, "sdk", e.Evidence, decl)
		}
		// Rules.
		if b.ix != nil {
			ctx := &rules.NodeContext{
				Type:        n.Type,
				Service:     n.Service,
				SourceKind:  string(b.sk[n.ID]),
				Traits:      sortedKeys(fs),
				Attributes:  n.Attributes,
				HasSDKUsage: false,
			}
			if n.Service != "" {
				ctx.Category = b.ix.CategoryOf(n.Service)
			}
			for _, e := range out[n.ID] {
				ctx.Out = append(ctx.Out, b.edgeRef(g, e.To, e.Kind))
			}
			for _, e := range in[n.ID] {
				ctx.In = append(ctx.In, b.edgeRef(g, e.From, e.Kind))
				if e.Kind == EdgeUsage {
					ctx.HasSDKUsage = true
				}
			}
			var svcRules []catalog.Rule
			if n.Service != "" && n.Class == ClassService {
				svcRules = b.ix.ServiceRules(n.Service)
			}
			fired, undetermined := rules.Apply(svcRules, b.ix.GlobalRules(), ctx)
			for _, o := range fired {
				switch {
				case o.AddTrait != "":
					ev := []model.Evidence{}
					if o.EvidenceAttr != "" {
						ev = append(ev, attrEvidence(n, o.EvidenceAttr))
					}
					b.put(fs, n, o.AddTrait, o.Level, "rule:"+o.RuleID, ev, decl)
				case o.SetLevel != nil:
					if f, ok := fs[o.SetLevel.Trait]; ok && catalog.LevelRank(o.SetLevel.Level) >= 0 {
						f.Level = o.SetLevel.Level
					}
				}
			}
			for _, r := range undetermined {
				n.Notes = append(n.Notes, NoteUndeterminedRule+r)
			}
		}
		for _, id := range sortedKeys(fs) {
			f := fs[id]
			f.Evidence = model.SortEvidence(f.Evidence)
			n.Findings = append(n.Findings, *f)
		}
		n.Notes = sortDedupStrings(n.Notes)
	}
}

// put adds or upgrades a finding. An existing finding keeps its origin unless
// the new level is strictly higher; evidence is merged either way.
func (b *builder) put(fs map[string]*Finding, n *Node, traitID string, lvl catalog.Level, origin string, ev, decl []model.Evidence) {
	if b.ix == nil {
		return
	}
	t := b.ix.Trait(traitID)
	if t == nil {
		return
	}
	if lvl == "" {
		lvl = t.Level
	}
	ev = nonEmpty(ev)
	if len(ev) == 0 {
		ev = nonEmpty(decl)
	}
	if len(ev) == 0 && len(n.Evidence) > 0 {
		ev = n.Evidence[:1]
	}
	if f, ok := fs[traitID]; ok {
		if catalog.LevelRank(lvl) > catalog.LevelRank(f.Level) {
			f.Level = lvl
			f.Origin = origin
		}
		f.Evidence = append(f.Evidence, ev...)
		return
	}
	fs[traitID] = &Finding{
		ID:         n.ID + "::" + traitID,
		Trait:      traitID,
		Level:      lvl,
		Dimensions: append([]catalog.Dimension(nil), t.Dimensions...),
		Origin:     origin,
		Evidence:   append([]model.Evidence(nil), ev...),
		Status:     FindingActive,
	}
}

func nonEmpty(ev []model.Evidence) []model.Evidence {
	var out []model.Evidence
	for _, e := range ev {
		if e != (model.Evidence{}) {
			out = append(out, e)
		}
	}
	return out
}

// attrEvidence picks the node evidence line mentioning the last segment of an
// attribute path, falling back to the primary declaration.
func attrEvidence(n *Node, path string) model.Evidence {
	seg := path
	if i := strings.LastIndex(seg, "."); i >= 0 {
		seg = seg[i+1:]
	}
	return pickEvidence(n, seg)
}

func (b *builder) edgeRef(g *Graph, other string, kind EdgeKind) rules.EdgeRef {
	r := rules.EdgeRef{Kind: string(kind)}
	if o := g.Node(other); o != nil && o.Class == ClassService {
		r.Service = o.Service
		if r.Service != "" {
			r.Category = b.ix.CategoryOf(r.Service)
		}
	}
	return r
}

func sortDedupStrings(s []string) []string {
	sort.Strings(s)
	out := s[:0]
	for i, v := range s {
		if i > 0 && v == s[i-1] {
			continue
		}
		out = append(out, v)
	}
	return out
}
