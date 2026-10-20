package catalog

import (
	"path"
	"sort"
	"strings"
)

// typeMatcher resolves type keys of one source format to entries, exact
// names first, then globs (most specific pattern first).
type typeMatcher[T any] struct {
	exact map[string]T
	globs []globEntry[T]
}

type globEntry[T any] struct {
	pattern string
	id      string
	entry   T
}

func newTypeMatcher[T any]() *typeMatcher[T] {
	return &typeMatcher[T]{exact: map[string]T{}}
}

func isGlob(s string) bool { return strings.ContainsAny(s, "*?[") }

func (m *typeMatcher[T]) add(key, id string, e T) {
	if isGlob(key) {
		m.globs = append(m.globs, globEntry[T]{pattern: key, id: id, entry: e})
		return
	}
	if _, ok := m.exact[key]; !ok { // first in ID order wins
		m.exact[key] = e
	}
}

// literalLen counts non-wildcard characters, a measure of glob specificity.
func literalLen(p string) int {
	n := 0
	for _, r := range p {
		if r != '*' && r != '?' && r != '[' && r != ']' {
			n++
		}
	}
	return n
}

func (m *typeMatcher[T]) sortGlobs() {
	sort.SliceStable(m.globs, func(i, j int) bool {
		a, b := m.globs[i], m.globs[j]
		if la, lb := literalLen(a.pattern), literalLen(b.pattern); la != lb {
			return la > lb
		}
		if a.pattern != b.pattern {
			return a.pattern < b.pattern
		}
		return a.id < b.id
	})
}

func (m *typeMatcher[T]) lookupExact(key string) (T, bool) {
	e, ok := m.exact[key]
	return e, ok
}

func (m *typeMatcher[T]) lookupGlob(key string) (T, bool) {
	for _, g := range m.globs {
		if ok, _ := path.Match(g.pattern, key); ok {
			return g.entry, true
		}
	}
	var zero T
	return zero, false
}

type entrySet struct {
	services map[string]*typeMatcher[*Service] // by source format
	neutrals map[string]*typeMatcher[*Neutral]
}

func newEntrySet() *entrySet {
	return &entrySet{services: map[string]*typeMatcher[*Service]{}, neutrals: map[string]*typeMatcher[*Neutral]{}}
}

func (es *entrySet) addService(s *Service) {
	for _, k := range typeKeys(s.ResourceTypes) {
		m := es.services[k[0]]
		if m == nil {
			m = newTypeMatcher[*Service]()
			es.services[k[0]] = m
		}
		m.add(k[1], s.ID, s)
	}
}

func (es *entrySet) addNeutral(n *Neutral) {
	for _, k := range typeKeys(n.ResourceTypes) {
		m := es.neutrals[k[0]]
		if m == nil {
			m = newTypeMatcher[*Neutral]()
			es.neutrals[k[0]] = m
		}
		m.add(k[1], n.ID, n)
	}
}

func (es *entrySet) finish() {
	for _, m := range es.services {
		m.sortGlobs()
	}
	for _, m := range es.neutrals {
		m.sortGlobs()
	}
}

// classify applies the precedence exact service > wildcard service > exact
// neutral > wildcard neutral.
func (es *entrySet) classify(t TypeRef) (*Service, *Neutral) {
	sm, nm := es.services[t.SourceFormat], es.neutrals[t.SourceFormat]
	if sm != nil {
		if s, ok := sm.lookupExact(t.Type); ok {
			return s, nil
		}
		if s, ok := sm.lookupGlob(t.Type); ok {
			return s, nil
		}
	}
	if nm != nil {
		if n, ok := nm.lookupExact(t.Type); ok {
			return nil, n
		}
		if n, ok := nm.lookupGlob(t.Type); ok {
			return nil, n
		}
	}
	return nil, nil
}

type edgeHintEntry struct {
	resourceType string
	attribute    string
	kind         string
}

type indexImpl struct {
	usable *entrySet // verified (+ drafts with IncludeDrafts)
	drafts *entrySet // drafts only, when not IncludeDrafts

	servicesByID map[string]*Service     // all statuses
	aliases      map[string]*Service     // alias -> declaring service
	traits       map[string]*Trait       // usable only (FR-016)
	alts         map[string]*Alternative // usable only (FR-016)

	usableServices []*Service // sorted by ID
	globalRules    []Rule
	edgeHints      []edgeHintEntry // sorted: exact resource types first
}

func (ix *Index) usableStatus(s Status) bool {
	return s == StatusVerified || (ix.opts.IncludeDrafts && s == StatusDraft)
}

// NewIndex builds an index over a catalog that passed Validate.
// Only verified entries are used unless opts.IncludeDrafts is set
// (then draft entries count as verified too; deprecated entries never
// classify but still resolve through ServiceByID).
func NewIndex(cat *Catalog, opts IndexOptions) *Index {
	ix := &Index{cat: cat, opts: opts}
	im := &indexImpl{
		usable:       newEntrySet(),
		drafts:       newEntrySet(),
		servicesByID: map[string]*Service{},
		aliases:      map[string]*Service{},
		traits:       map[string]*Trait{},
		alts:         map[string]*Alternative{},
	}
	ix.impl = im

	svcs := make([]*Service, 0, len(cat.Services))
	for i := range cat.Services {
		svcs = append(svcs, &cat.Services[i])
	}
	sort.SliceStable(svcs, func(i, j int) bool { return svcs[i].ID < svcs[j].ID })
	for _, s := range svcs {
		if _, ok := im.servicesByID[s.ID]; !ok {
			im.servicesByID[s.ID] = s
		}
		for _, a := range s.Aliases {
			if _, ok := im.aliases[a]; !ok {
				im.aliases[a] = s
			}
		}
		switch {
		case ix.usableStatus(s.Provenance.Status):
			im.usable.addService(s)
			im.usableServices = append(im.usableServices, s)
		case s.Provenance.Status == StatusDraft:
			im.drafts.addService(s)
		}
	}

	ns := make([]*Neutral, 0, len(cat.Neutrals))
	for i := range cat.Neutrals {
		ns = append(ns, &cat.Neutrals[i])
	}
	sort.SliceStable(ns, func(i, j int) bool { return ns[i].ID < ns[j].ID })
	for _, n := range ns {
		switch {
		case ix.usableStatus(n.Provenance.Status):
			im.usable.addNeutral(n)
		case n.Provenance.Status == StatusDraft:
			im.drafts.addNeutral(n)
		}
	}
	im.usable.finish()
	im.drafts.finish()

	traits := make([]*Trait, 0, len(cat.Traits))
	for i := range cat.Traits {
		traits = append(traits, &cat.Traits[i])
	}
	sort.SliceStable(traits, func(i, j int) bool { return traits[i].ID < traits[j].ID })
	for _, t := range traits {
		if !ix.usableStatus(t.Provenance.Status) {
			continue
		}
		if _, ok := im.traits[t.ID]; !ok {
			im.traits[t.ID] = t
		}
		im.globalRules = append(im.globalRules, t.Rules...)
	}
	sortRules(im.globalRules)

	for i := range cat.Alternatives {
		a := &cat.Alternatives[i]
		if !ix.usableStatus(a.Provenance.Status) {
			continue
		}
		if prev, ok := im.alts[a.ID]; !ok || a.File < prev.File {
			im.alts[a.ID] = a
		}
	}

	for _, s := range im.usableServices {
		for _, h := range s.EdgeHints {
			im.edgeHints = append(im.edgeHints, edgeHintEntry{h.ResourceType, h.Attribute, h.EdgeKind})
		}
	}
	sort.SliceStable(im.edgeHints, func(i, j int) bool {
		a, b := im.edgeHints[i], im.edgeHints[j]
		if ga, gb := isGlob(a.resourceType), isGlob(b.resourceType); ga != gb {
			return !ga
		}
		if ga := isGlob(a.resourceType); ga {
			if la, lb := literalLen(a.resourceType), literalLen(b.resourceType); la != lb {
				return la > lb
			}
		}
		if a.resourceType != b.resourceType {
			return a.resourceType < b.resourceType
		}
		return a.attribute < b.attribute
	})
	return ix
}

func sortRules(rs []Rule) {
	sort.SliceStable(rs, func(i, j int) bool { return rs[i].ID < rs[j].ID })
}

// Classify resolves a type: exact service type > wildcard neutral > unknown.
// A type matched by both a service and a neutral entry resolves to the
// service. When nothing usable matches but a draft entry would match, Class
// is "unverified" and Service/Neutral point to that draft entry.
func (ix *Index) Classify(t TypeRef) Classification {
	// Full precedence: exact service > wildcard service > exact neutral >
	// wildcard neutral > draft match ("unverified") > unknown.
	if s, n := ix.impl.usable.classify(t); s != nil {
		return Classification{Class: "service", Service: s}
	} else if n != nil {
		return Classification{Class: "neutral", Neutral: n}
	}
	if s, n := ix.impl.drafts.classify(t); s != nil || n != nil {
		return Classification{Class: "unverified", Service: s, Neutral: n}
	}
	return Classification{Class: "unknown"}
}

// ServiceByID returns a service by ID, following aliases and successors.
func (ix *Index) ServiceByID(id string) *Service {
	im := ix.impl
	s := im.servicesByID[id]
	if s == nil {
		s = im.aliases[id]
	}
	seen := map[string]bool{}
	for s != nil && s.Successor != "" && !seen[s.ID] {
		seen[s.ID] = true
		next := im.servicesByID[s.Successor]
		if next == nil {
			next = im.aliases[s.Successor]
		}
		if next == nil {
			break
		}
		s = next
	}
	return s
}

// Trait returns a usable trait by ID or nil. Draft and deprecated traits
// never affect results (FR-016).
func (ix *Index) Trait(id string) *Trait { return ix.impl.traits[id] }

// Alternative returns a usable alternative by ID or nil (FR-016).
func (ix *Index) Alternative(id string) *Alternative { return ix.impl.alts[id] }

// Alternatives returns the mappings of a service ordered: recommended first,
// then difficulty ascending, then alternative name ascending (FR-015).
// Mappings to alternatives that are not usable are left out (FR-016).
func (ix *Index) Alternatives(serviceID string) []AlternativeMapping {
	s := ix.ServiceByID(serviceID)
	if s == nil || len(s.Alternatives) == 0 {
		return nil
	}
	var out []AlternativeMapping
	for _, m := range s.Alternatives {
		if ix.Alternative(m.Ref) != nil {
			out = append(out, m)
		}
	}
	if len(out) == 0 {
		return nil
	}
	name := func(m AlternativeMapping) string {
		if a := ix.Alternative(m.Ref); a != nil && a.Name != "" {
			return a.Name
		}
		return m.Ref
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Recommended != b.Recommended {
			return a.Recommended
		}
		if da, db := DifficultyRank(a.Difficulty), DifficultyRank(b.Difficulty); da != db {
			return da < db
		}
		if na, nb := name(a), name(b); na != nb {
			return na < nb
		}
		return a.Ref < b.Ref
	})
	return out
}

// SDKPatterns returns all usable services' SDK patterns for an ecosystem,
// as (service ID, pattern) pairs sorted by service ID.
func (ix *Index) SDKPatterns(ecosystem string) []ServicePattern {
	var out []ServicePattern
	for _, s := range ix.impl.usableServices {
		for _, p := range s.SDK {
			if p.Ecosystem == ecosystem {
				out = append(out, ServicePattern{ServiceID: s.ID, Pattern: p})
			}
		}
	}
	return out
}

// Bindings returns all usable Kubernetes bindings with the effective service
// ID filled in (binding.Service or the declaring service), sorted by service ID.
func (ix *Index) Bindings() []Binding {
	var out []Binding
	for _, s := range ix.impl.usableServices {
		for _, b := range s.KubernetesBindings {
			if b.Service == "" {
				b.Service = s.ID
			}
			out = append(out, b)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Service < out[j].Service })
	return out
}

// EdgeKindFor returns the edge kind hinted for (resource type, attribute), or "".
func (ix *Index) EdgeKindFor(resourceType, attribute string) string {
	// Hint resource types may be globs ("*" matches every type); exact types
	// win over globs. A hint attribute matches the attribute itself first,
	// then its last dotted segment (hint "subnet_ids" matches
	// "vpc_config.subnet_ids").
	last := attribute
	if i := strings.LastIndex(attribute, "."); i >= 0 {
		last = attribute[i+1:]
	}
	for _, pass := range []string{attribute, last} {
		for _, h := range ix.impl.edgeHints {
			if h.attribute != pass {
				continue
			}
			if h.resourceType == resourceType {
				return h.kind
			}
			if isGlob(h.resourceType) {
				if ok, _ := path.Match(h.resourceType, resourceType); ok {
					return h.kind
				}
			}
		}
	}
	return ""
}

// ServiceRules returns rules declared by a service, sorted by rule ID.
func (ix *Index) ServiceRules(serviceID string) []Rule {
	s := ix.ServiceByID(serviceID)
	if s == nil || len(s.Rules) == 0 {
		return nil
	}
	out := append([]Rule(nil), s.Rules...)
	sortRules(out)
	return out
}

// GlobalRules returns rules declared in trait files, sorted by rule ID.
func (ix *Index) GlobalRules() []Rule {
	return append([]Rule(nil), ix.impl.globalRules...)
}

// CategoryOf returns the category ID of a service ("" if unknown).
func (ix *Index) CategoryOf(serviceID string) string {
	if s := ix.ServiceByID(serviceID); s != nil {
		return s.Category
	}
	return ""
}

// ServicePattern pairs a service ID with one of its SDK patterns.
type ServicePattern struct {
	ServiceID string
	Pattern   SdkPattern
}
