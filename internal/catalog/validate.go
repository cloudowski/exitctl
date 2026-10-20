package catalog

import (
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// LoadAndValidate loads a catalog and runs Validate on it. Problems from both
// steps are merged and sorted. Any problem makes the catalog unusable.
func LoadAndValidate(fsys fs.FS) (*Catalog, []Problem, error) {
	cat, probs, err := Load(fsys)
	if err != nil {
		return nil, nil, err
	}
	probs = append(probs, Validate(cat)...)
	sortProblems(probs)
	return cat, probs, nil
}

// Validate runs the referential and semantic checks of data-model.md 1.11
// (schema conformance is checked by Load): unique IDs per kind, references
// resolve, no alias/successor cycles, each resource type maps to at most one
// service, RE2 patterns and globs compile, `recommended` only with maintainer
// approval, verified services have >= 1 alternative, and a level override
// always carries a reason. Problems are sorted by file, then line.
func Validate(cat *Catalog) []Problem {
	v := &validator{cat: cat}
	v.uniqueIDs()
	v.references()
	v.aliasCycles()
	v.resourceTypes()
	v.patterns()
	v.recommended()
	v.alternativesPresent()
	v.levelReasons()
	v.lifecycle()
	sortProblems(v.probs)
	return v.probs
}

type validator struct {
	cat   *Catalog
	probs []Problem
}

func (v *validator) add(p Pos, id, format string, args ...any) {
	v.probs = append(v.probs, Problem{File: p.File, Line: p.Line, EntryID: id, Message: fmt.Sprintf(format, args...)})
}

// locAt returns the position of the occ-th declaration of key.
func (v *validator) locAt(key string, occ int) Pos {
	ps := v.cat.locs[key]
	switch {
	case len(ps) == 0:
		return Pos{}
	case occ < len(ps):
		return ps[occ]
	}
	return ps[len(ps)-1]
}

// (2) unique IDs per kind (and unique rule IDs across the catalog).
func (v *validator) uniqueIDs() {
	dup := func(kind, id string, p Pos, seen map[string]Pos) {
		if id == "" {
			v.add(p, id, "%s without id", kind)
			return
		}
		if first, ok := seen[id]; ok {
			v.add(p, id, "duplicate %s id %q (first defined at %s:%d)", kind, id, first.File, first.Line)
			return
		}
		seen[id] = p
	}
	seen := map[string]Pos{}
	occ := map[string]int{}
	for _, pr := range v.cat.Providers {
		k := "provider:" + pr.ID
		dup("provider", pr.ID, v.locAt(k, occ[k]), seen)
		occ[k]++
	}
	seen = map[string]Pos{}
	for _, c := range v.cat.Categories {
		k := "category:" + c.ID
		dup("category", c.ID, v.locAt(k, occ[k]), seen)
		occ[k]++
	}
	seen = map[string]Pos{}
	for _, t := range v.cat.Traits {
		dup("trait", t.ID, t.Pos, seen)
	}
	seen = map[string]Pos{}
	for _, s := range v.cat.Services {
		dup("service", s.ID, s.Pos, seen)
	}
	seen = map[string]Pos{}
	for _, a := range v.cat.Alternatives {
		dup("alternative", a.ID, a.Pos, seen)
	}
	seen = map[string]Pos{}
	for _, n := range v.cat.Neutrals {
		dup("neutral", n.ID, n.Pos, seen)
	}
	seen = map[string]Pos{}
	for _, t := range v.cat.Traits {
		for _, r := range t.Rules {
			dup("rule", r.ID, t.Pos, seen)
		}
	}
	for _, s := range v.cat.Services {
		for _, r := range s.Rules {
			dup("rule", r.ID, s.Pos, seen)
		}
	}
}

// (3) all references resolve.
func (v *validator) references() {
	providers := map[string]bool{}
	for _, p := range v.cat.Providers {
		providers[p.ID] = true
	}
	categories := map[string]bool{}
	for _, c := range v.cat.Categories {
		categories[c.ID] = true
	}
	traits := map[string]bool{}
	for _, t := range v.cat.Traits {
		traits[t.ID] = true
	}
	alts := map[string]bool{}
	for _, a := range v.cat.Alternatives {
		alts[a.ID] = true
	}
	services := map[string]bool{}
	aliases := map[string]string{}
	for _, s := range v.cat.Services {
		services[s.ID] = true
	}
	for _, s := range v.cat.Services {
		for _, a := range s.Aliases {
			if owner, ok := aliases[a]; ok && owner != s.ID {
				v.add(s.Pos, s.ID, "alias %q is also claimed by service %q", a, owner)
				continue
			}
			aliases[a] = s.ID
		}
	}
	serviceResolves := func(id string) bool { return services[id] || aliases[id] != "" }

	traitRef := func(p Pos, owner, where string, r TraitRef) {
		if !traits[r.ID] {
			v.add(p, owner, "%s references unknown trait %q", where, r.ID)
		}
	}
	ruleRefs := func(p Pos, owner string, r Rule) {
		if r.Then.AddTrait != "" && !traits[r.Then.AddTrait] {
			v.add(p, owner, "rule %q adds unknown trait %q", r.ID, r.Then.AddTrait)
		}
		if r.Then.SetLevel != nil && !traits[r.Then.SetLevel.Trait] {
			v.add(p, owner, "rule %q sets level of unknown trait %q", r.ID, r.Then.SetLevel.Trait)
		}
		for _, t := range r.Match.Trait {
			if !traits[t] {
				v.add(p, owner, "rule %q matches unknown trait %q", r.ID, t)
			}
		}
		for _, s := range r.Match.Service {
			if !serviceResolves(s) {
				v.add(p, owner, "rule %q matches unknown service %q", r.ID, s)
			}
		}
		for _, c := range r.Match.Category {
			if !categories[c] {
				v.add(p, owner, "rule %q matches unknown category %q", r.ID, c)
			}
		}
		walkConditions(r.When, func(c *Condition) {
			if c.EdgeTo == nil {
				return
			}
			if c.EdgeTo.Service != "" && !serviceResolves(c.EdgeTo.Service) {
				v.add(p, owner, "rule %q edge_to references unknown service %q", r.ID, c.EdgeTo.Service)
			}
			if c.EdgeTo.Category != "" && !categories[c.EdgeTo.Category] {
				v.add(p, owner, "rule %q edge_to references unknown category %q", r.ID, c.EdgeTo.Category)
			}
		})
	}

	for _, t := range v.cat.Traits {
		for _, r := range t.Rules {
			ruleRefs(t.Pos, t.ID, r)
		}
	}
	for _, s := range v.cat.Services {
		if !providers[s.Provider] {
			v.add(s.Pos, s.ID, "unknown provider %q", s.Provider)
		} else if p, _, _ := strings.Cut(s.ID, "."); p != s.Provider {
			v.add(s.Pos, s.ID, "service id must start with its provider %q", s.Provider)
		}
		if !categories[s.Category] {
			v.add(s.Pos, s.ID, "unknown category %q", s.Category)
		}
		for _, r := range s.Traits {
			traitRef(s.Pos, s.ID, "traits", r)
		}
		for _, r := range s.Rules {
			ruleRefs(s.Pos, s.ID, r)
		}
		for _, sp := range s.SDK {
			if sp.Trait != nil {
				traitRef(s.Pos, s.ID, "sdk "+sp.Ecosystem+" trait", *sp.Trait)
			}
		}
		for _, b := range s.KubernetesBindings {
			if b.Service != "" && !serviceResolves(b.Service) {
				v.add(s.Pos, s.ID, "kubernetes binding references unknown service %q", b.Service)
			}
		}
		for _, m := range s.Alternatives {
			if !alts[m.Ref] {
				v.add(s.Pos, s.ID, "unknown alternative %q", m.Ref)
			}
			for _, r := range m.ResidualTraits {
				traitRef(s.Pos, s.ID, "alternative "+m.Ref+" residual_traits", r)
			}
		}
		if s.Successor != "" && !serviceResolves(s.Successor) {
			v.add(s.Pos, s.ID, "unknown successor %q", s.Successor)
		}
	}
	for _, n := range v.cat.Neutrals {
		if !providers[n.Provider] {
			v.add(n.Pos, n.ID, "unknown provider %q", n.Provider)
		}
	}
}

// (4) no alias cycles: following aliases and successors must terminate.
func (v *validator) aliasCycles() {
	// redirect: an ID that is an alias of service S redirects to S; a
	// service with a successor redirects to it.
	next := map[string][]string{}
	pos := map[string]Pos{}
	for _, s := range v.cat.Services {
		pos[s.ID] = s.Pos
		for _, a := range s.Aliases {
			if a != s.ID {
				next[a] = append(next[a], s.ID)
			} else {
				v.add(s.Pos, s.ID, "service lists its own id as an alias")
			}
		}
		if s.Successor != "" {
			if s.Successor == s.ID {
				v.add(s.Pos, s.ID, "service is its own successor")
				continue
			}
			next[s.ID] = append(next[s.ID], s.Successor)
		}
	}
	keys := make([]string, 0, len(next))
	for k := range next {
		keys = append(keys, k)
		sort.Strings(next[k])
	}
	sort.Strings(keys)
	const (
		white = iota
		grey
		black
	)
	color := map[string]int{}
	reported := map[string]bool{}
	var stack []string
	var visit func(string)
	visit = func(n string) {
		color[n] = grey
		stack = append(stack, n)
		for _, m := range next[n] {
			switch color[m] {
			case white:
				visit(m)
			case grey:
				i := slices.Index(stack, m)
				cyc := append([]string{}, stack[i:]...)
				minID := slices.Min(cyc)
				if !reported[minID] {
					reported[minID] = true
					// rotate so the cycle starts at its smallest ID
					j := slices.Index(cyc, minID)
					cyc = append(cyc[j:], cyc[:j]...)
					v.add(pos[minID], minID, "alias/successor cycle: %s -> %s", strings.Join(cyc, " -> "), minID)
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[n] = black
	}
	for _, k := range keys {
		if color[k] == white {
			visit(k)
		}
	}
}

// typeKeys returns the (format, type key) pairs of a resource type set.
func typeKeys(rt ResourceTypes) [][2]string {
	var out [][2]string
	for _, t := range rt.Terraform {
		out = append(out, [2]string{"terraform", t})
	}
	for _, t := range rt.CloudFormation {
		out = append(out, [2]string{"cloudformation", t})
	}
	for _, gk := range rt.Kubernetes {
		out = append(out, [2]string{"kubernetes", gk.Group + "/" + gk.Kind})
	}
	for _, t := range rt.ContainerRegistry {
		out = append(out, [2]string{"container_registry", t})
	}
	for _, t := range rt.CIAction {
		out = append(out, [2]string{"ci_action", t})
	}
	return out
}

// (5) each resource type maps to at most one service (deprecated services
// do not classify and are excluded); edge hints must not conflict.
func (v *validator) resourceTypes() {
	owner := map[[2]string]string{}
	for _, s := range v.cat.Services {
		if s.Provenance.Status == StatusDeprecated {
			continue
		}
		for _, k := range typeKeys(s.ResourceTypes) {
			if o, ok := owner[k]; ok && o != s.ID {
				v.add(s.Pos, s.ID, "%s type %q already maps to service %q", k[0], k[1], o)
				continue
			}
			owner[k] = s.ID
		}
	}
	hints := map[[2]string]string{}
	for _, s := range v.cat.Services {
		if s.Provenance.Status == StatusDeprecated {
			continue
		}
		for _, h := range s.EdgeHints {
			k := [2]string{h.ResourceType, h.Attribute}
			if o, ok := hints[k]; ok && o != h.EdgeKind {
				v.add(s.Pos, s.ID, "edge hint %s.%s conflicts: %q vs %q", h.ResourceType, h.Attribute, h.EdgeKind, o)
				continue
			}
			hints[k] = h.EdgeKind
		}
	}
}

// (6) RE2 patterns compile; globs are well-formed.
func (v *validator) patterns() {
	re := func(p Pos, id, where, pat string) {
		if _, err := regexp.Compile(pat); err != nil {
			v.add(p, id, "%s pattern %q is not valid RE2: %v", where, pat, err)
		}
	}
	glob := func(p Pos, id, where, pat string) {
		if _, err := path.Match(pat, ""); err != nil {
			v.add(p, id, "%s glob %q is malformed", where, pat)
		}
	}
	ruleRE := func(p Pos, id string, r Rule) {
		walkConditions(r.When, func(c *Condition) {
			if c.Matches != "" {
				re(p, id, "rule "+r.ID+" matches", c.Matches)
			}
		})
		for _, t := range r.Match.ResourceType {
			glob(p, id, "rule "+r.ID+" resource_type", t)
		}
	}
	for _, t := range v.cat.Traits {
		for _, r := range t.Rules {
			ruleRE(t.Pos, t.ID, r)
		}
	}
	for _, s := range v.cat.Services {
		for _, sp := range s.SDK {
			for _, x := range sp.Imports {
				re(s.Pos, s.ID, "sdk "+sp.Ecosystem+" imports", x)
			}
			for _, x := range sp.Usage {
				re(s.Pos, s.ID, "sdk "+sp.Ecosystem+" usage", x)
			}
			for _, x := range sp.Packages {
				glob(s.Pos, s.ID, "sdk "+sp.Ecosystem+" packages", x)
			}
			for _, x := range sp.Gate {
				glob(s.Pos, s.ID, "sdk "+sp.Ecosystem+" gate", x)
			}
		}
		for _, r := range s.Rules {
			ruleRE(s.Pos, s.ID, r)
		}
		for _, k := range typeKeys(s.ResourceTypes) {
			glob(s.Pos, s.ID, k[0]+" resource type", k[1])
		}
	}
	for _, n := range v.cat.Neutrals {
		for _, k := range typeKeys(n.ResourceTypes) {
			glob(n.Pos, n.ID, k[0]+" resource type", k[1])
		}
	}
}

// (7) recommended only with maintainer approval.
func (v *validator) recommended() {
	for _, s := range v.cat.Services {
		for _, m := range s.Alternatives {
			if !m.Recommended {
				continue
			}
			if m.ApprovedBy == "" {
				v.add(s.Pos, s.ID, "alternative %q is recommended without approved_by", m.Ref)
			} else if !slices.Contains(v.cat.Manifest.Maintainers, m.ApprovedBy) {
				v.add(s.Pos, s.ID, "alternative %q is recommended but approved_by %q is not a catalog maintainer", m.Ref, m.ApprovedBy)
			}
		}
	}
}

// (8) verified services have >= 1 alternative mapping (FR-020).
func (v *validator) alternativesPresent() {
	for _, s := range v.cat.Services {
		if s.Provenance.Status == StatusVerified && len(s.Alternatives) == 0 {
			v.add(s.Pos, s.ID, "verified service has no alternatives")
		}
	}
}

// A level override always carries a reason (FR-013).
func (v *validator) levelReasons() {
	check := func(p Pos, id, where string, r TraitRef) {
		if r.Level != "" && strings.TrimSpace(r.Reason) == "" {
			v.add(p, id, "%s: level override for trait %q without reason", where, r.ID)
		}
	}
	for _, s := range v.cat.Services {
		for _, r := range s.Traits {
			check(s.Pos, s.ID, "traits", r)
		}
		for _, sp := range s.SDK {
			if sp.Trait != nil {
				check(s.Pos, s.ID, "sdk "+sp.Ecosystem, *sp.Trait)
			}
		}
		for _, m := range s.Alternatives {
			for _, r := range m.ResidualTraits {
				check(s.Pos, s.ID, "alternative "+m.Ref+" residual_traits", r)
			}
		}
	}
}

// Lifecycle consistency (DM 1.10): successor only on deprecated services;
// verified LLM-generated entries must have passed evals.
func (v *validator) lifecycle() {
	llm := func(p Pos, id string, pv Provenance) {
		if pv.Status == StatusVerified && pv.Origin == "llm" && (pv.LLM == nil || !pv.LLM.EvalPassed) {
			v.add(p, id, "verified LLM-generated entry requires llm.eval_passed: true")
		}
	}
	for _, t := range v.cat.Traits {
		llm(t.Pos, t.ID, t.Provenance)
	}
	for _, s := range v.cat.Services {
		llm(s.Pos, s.ID, s.Provenance)
		if s.Successor != "" && s.Provenance.Status != StatusDeprecated {
			v.add(s.Pos, s.ID, "successor is only allowed on deprecated services")
		}
	}
	for _, a := range v.cat.Alternatives {
		llm(a.Pos, a.ID, a.Provenance)
	}
	for _, n := range v.cat.Neutrals {
		llm(n.Pos, n.ID, n.Provenance)
	}
}

// walkConditions calls fn for c and every nested condition.
func walkConditions(c *Condition, fn func(*Condition)) {
	if c == nil {
		return
	}
	fn(c)
	for _, x := range c.All {
		walkConditions(x, fn)
	}
	for _, x := range c.Any {
		walkConditions(x, fn)
	}
	walkConditions(c.Not, fn)
}
