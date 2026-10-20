package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	yaml "go.yaml.in/yaml/v3"

	"github.com/cloudowski/exitctl/internal/catalog"
)

// Statuses an entry can have (data-model.md 1.10).
const (
	statusDraft      = "draft"
	statusVerified   = "verified"
	statusDeprecated = "deprecated"
)

// Store reads and edits a catalog directory. Every edit is validated against
// the whole catalog and rolled back when it would add a problem.
type Store struct {
	mu       sync.Mutex
	dir      string
	reviewer string
	now      func() time.Time
}

// ValidationError lists the problems an edit would have introduced.
type ValidationError struct{ Problems []string }

func (e *ValidationError) Error() string {
	return "the change was rolled back because it would make the catalog invalid:\n  " + strings.Join(e.Problems, "\n  ")
}

// Summary is one row of the review list.
type Summary struct {
	Kind         string   `json:"kind"`
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Provider     string   `json:"provider,omitempty"`
	Category     string   `json:"category,omitempty"`
	Status       string   `json:"status"`
	Origin       string   `json:"origin"`
	ReviewedBy   string   `json:"reviewed_by,omitempty"`
	ReviewedOn   string   `json:"reviewed_on,omitempty"`
	File         string   `json:"file"`
	Alternatives int      `json:"alternatives,omitempty"`
	Recommended  int      `json:"recommended,omitempty"`
	UsedBy       int      `json:"used_by,omitempty"`
	Warnings     []string `json:"warnings,omitempty"`
}

// State is everything the list view needs.
type State struct {
	Reviewer     string                    `json:"reviewer"`
	Maintainers  []string                  `json:"maintainers"`
	CanRecommend bool                      `json:"can_recommend"`
	Catalog      string                    `json:"catalog"`
	Version      string                    `json:"version"`
	Stats        map[string]map[string]int `json:"stats"`
	Entries      []Summary                 `json:"entries"`
	Problems     []string                  `json:"problems"`
	Levels       []string                  `json:"levels"`
}

// TraitView is a trait attached to a service.
type TraitView struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Level  string `json:"level"` // effective level for this service
	// Default is the trait's own level; Override is the service's level
	// override ("" when the default applies).
	Default  string `json:"default_level"`
	Override string `json:"override_level,omitempty"`
	Reason   string `json:"reason,omitempty"`
	File     string `json:"file,omitempty"`
}

// MappingView is an alternative mapping of a service.
type MappingView struct {
	Ref           string `json:"ref"`
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	Status        string `json:"status"`
	Difficulty    string `json:"difficulty"`
	Compatibility string `json:"compatibility"`
	Notes         string `json:"notes,omitempty"`
	Recommended   bool   `json:"recommended"`
	ApprovedBy    string `json:"approved_by,omitempty"`
	Residual      int    `json:"residual_traits"`
	File          string `json:"file,omitempty"`
}

// LLMView is the LLM provenance of a drafted entry.
type LLMView struct {
	Batch         string `json:"batch"`
	Model         string `json:"model"`
	PromptVersion string `json:"prompt_version"`
	EvalPassed    bool   `json:"eval_passed"`
}

func llmView(l *catalog.LLMInfo) *LLMView {
	if l == nil {
		return nil
	}
	return &LLMView{Batch: l.Batch, Model: l.Model, PromptVersion: l.PromptVersion, EvalPassed: l.EvalPassed}
}

// UsageView is a service that references a trait or an alternative.
type UsageView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	File        string `json:"file"`
	Recommended bool   `json:"recommended,omitempty"`
	// Override is the service's level override of a trait ("" = default).
	Override string `json:"override_level,omitempty"`
}

// Detail is the full view of one entry.
type Detail struct {
	Summary
	YAML          string            `json:"yaml"`
	Sources       []string          `json:"sources"`
	LLM           *LLMView          `json:"llm,omitempty"`
	Level         string            `json:"level,omitempty"` // trait default level
	Explanation   string            `json:"explanation,omitempty"`
	ExitHint      string            `json:"exit_hint,omitempty"`
	License       string            `json:"license,omitempty"`
	Homepage      string            `json:"homepage,omitempty"`
	AltKind       string            `json:"alternative_kind,omitempty"`
	ResourceTypes map[string]string `json:"resource_types,omitempty"`
	NotScored     bool              `json:"not_scored,omitempty"`
	Traits        []TraitView       `json:"traits,omitempty"`
	Mappings      []MappingView     `json:"mappings,omitempty"`
	UsedByList    []UsageView       `json:"used_by_list,omitempty"`
}

// snapshot is the loaded catalog with lookup tables.
type snapshot struct {
	cat      *catalog.Catalog
	problems []catalog.Problem
	traits   map[string]*catalog.Trait
	alts     map[string]*catalog.Alternative
}

func (s *Store) load() (*snapshot, error) {
	cat, probs, err := catalog.LoadAndValidate(os.DirFS(s.dir))
	if err != nil {
		return nil, err
	}
	sn := &snapshot{cat: cat, problems: probs, traits: map[string]*catalog.Trait{}, alts: map[string]*catalog.Alternative{}}
	for i := range cat.Traits {
		sn.traits[cat.Traits[i].ID] = &cat.Traits[i]
	}
	for i := range cat.Alternatives {
		sn.alts[cat.Alternatives[i].ID] = &cat.Alternatives[i]
	}
	return sn, nil
}

func (sn *snapshot) maintainer(name string) bool {
	return slices.Contains(sn.cat.Manifest.Maintainers, name)
}

// State returns the review list.
func (s *Store) State() (*State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sn, err := s.load()
	if err != nil {
		return nil, err
	}
	st := &State{
		Reviewer: s.reviewer, Maintainers: sn.cat.Manifest.Maintainers, CanRecommend: sn.maintainer(s.reviewer),
		Catalog: s.dir, Version: sn.cat.Manifest.CatalogVersion,
		Stats: map[string]map[string]int{}, Entries: sn.summaries(), Problems: problemStrings(sn.problems),
		Levels: levels,
	}
	for _, e := range st.Entries {
		if st.Stats[e.Kind] == nil {
			st.Stats[e.Kind] = map[string]int{}
		}
		st.Stats[e.Kind][e.Status]++
	}
	return st, nil
}

func (sn *snapshot) summaries() []Summary {
	usedBy := map[string][]*catalog.Service{} // "trait:<id>" / "alt:<id>" -> services
	for i := range sn.cat.Services {
		s := &sn.cat.Services[i]
		for _, t := range s.Traits {
			usedBy["trait:"+t.ID] = append(usedBy["trait:"+t.ID], s)
		}
		for _, m := range s.Alternatives {
			usedBy["alt:"+m.Ref] = append(usedBy["alt:"+m.Ref], s)
		}
	}
	verifiedUsers := func(key string) int {
		n := 0
		for _, s := range usedBy[key] {
			if s.Provenance.Status == statusVerified {
				n++
			}
		}
		return n
	}
	var out []Summary
	for i := range sn.cat.Services {
		s := &sn.cat.Services[i]
		row := summary("service", s.ID, s.Name, s.Pos, s.Provenance)
		row.Provider, row.Category, row.Alternatives = s.Provider, s.Category, len(s.Alternatives)
		var draftTraits, usableAlts int
		for _, t := range s.Traits {
			if tr := sn.traits[t.ID]; tr != nil && tr.Provenance.Status != statusVerified {
				draftTraits++
			}
		}
		for _, m := range s.Alternatives {
			a := sn.alts[m.Ref]
			if m.Recommended {
				row.Recommended++
				if a != nil && a.Provenance.Status != statusVerified {
					row.Warnings = append(row.Warnings, fmt.Sprintf("recommended alternative %s is not verified, so it is not shown", m.Ref))
				}
			}
			if a != nil && a.Provenance.Status == statusVerified {
				usableAlts++
			}
		}
		if row.Status == statusVerified {
			if draftTraits > 0 {
				row.Warnings = append(row.Warnings, fmt.Sprintf("%d of its traits are not verified and are ignored in scoring", draftTraits))
			}
			if usableAlts == 0 {
				row.Warnings = append(row.Warnings, "none of its alternatives is verified, so no alternative is shown")
			}
		}
		out = append(out, row)
	}
	for i := range sn.cat.Traits {
		t := &sn.cat.Traits[i]
		row := summary("trait", t.ID, t.Name, t.Pos, t.Provenance)
		row.UsedBy = len(usedBy["trait:"+t.ID])
		if n := verifiedUsers("trait:" + t.ID); n > 0 && row.Status != statusVerified {
			row.Warnings = append(row.Warnings, fmt.Sprintf("used by %d verified services but not verified, so it is ignored in scoring", n))
		}
		out = append(out, row)
	}
	for i := range sn.cat.Alternatives {
		a := &sn.cat.Alternatives[i]
		row := summary("alternative", a.ID, a.Name, a.Pos, a.Provenance)
		row.UsedBy = len(usedBy["alt:"+a.ID])
		switch {
		case row.Status == statusDeprecated:
			row.Warnings = append(row.Warnings, "hidden everywhere (deprecated)")
		case row.Status != statusVerified:
			if n := verifiedUsers("alt:" + a.ID); n > 0 {
				row.Warnings = append(row.Warnings, fmt.Sprintf("mapped by %d verified services but not verified, so it is not shown", n))
			}
		}
		if row.UsedBy == 0 {
			row.Warnings = append(row.Warnings, "not mapped by any service")
		}
		out = append(out, row)
	}
	for i := range sn.cat.Neutrals {
		n := &sn.cat.Neutrals[i]
		row := summary("neutral", n.ID, n.ID, n.Pos, n.Provenance)
		row.Provider = n.Provider
		out = append(out, row)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return kindRank(out[i].Kind) < kindRank(out[j].Kind)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func kindRank(k string) int {
	return slices.Index([]string{"service", "trait", "alternative", "neutral"}, k)
}

func summary(kind, id, name string, pos catalog.Pos, p catalog.Provenance) Summary {
	return Summary{Kind: kind, ID: id, Name: name, File: pos.File, Status: string(p.Status),
		Origin: p.Origin, ReviewedBy: p.ReviewedBy, ReviewedOn: p.ReviewedOn}
}

// Detail returns one entry by file.
func (s *Store) Detail(file string) (*Detail, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sn, err := s.load()
	if err != nil {
		return nil, err
	}
	var row *Summary
	for _, r := range sn.summaries() {
		if r.File == file {
			row = &r
			break
		}
	}
	if row == nil {
		return nil, fmt.Errorf("no catalog entry in %s", file)
	}
	b, err := os.ReadFile(filepath.Join(s.dir, filepath.FromSlash(file)))
	if err != nil {
		return nil, err
	}
	d := &Detail{Summary: *row, YAML: string(b)}
	switch row.Kind {
	case "service":
		for i := range sn.cat.Services {
			if sv := &sn.cat.Services[i]; sv.File == file {
				sn.serviceDetail(d, sv)
			}
		}
	case "trait":
		t := sn.traits[row.ID]
		d.Sources, d.LLM, d.Explanation, d.ExitHint = t.Provenance.Sources, llmView(t.Provenance.LLM), t.Explanation, t.ExitHint
		d.Level = string(t.Level)
		d.UsedByList = sn.users(func(sv *catalog.Service) (bool, bool) {
			return slices.ContainsFunc(sv.Traits, func(r catalog.TraitRef) bool { return r.ID == row.ID }), false
		})
		for i := range d.UsedByList {
			for _, r := range sn.serviceByFile(d.UsedByList[i].File).Traits {
				if r.ID == row.ID {
					d.UsedByList[i].Override = string(r.Level)
				}
			}
		}
	case "alternative":
		a := sn.alts[row.ID]
		d.Sources, d.LLM, d.License, d.Homepage, d.AltKind = a.Provenance.Sources, llmView(a.Provenance.LLM), a.License, a.Homepage, a.Kind
		d.UsedByList = sn.users(func(sv *catalog.Service) (bool, bool) {
			for _, m := range sv.Alternatives {
				if m.Ref == row.ID {
					return true, m.Recommended
				}
			}
			return false, false
		})
	case "neutral":
		for i := range sn.cat.Neutrals {
			if n := &sn.cat.Neutrals[i]; n.File == file {
				d.Sources, d.LLM = n.Provenance.Sources, llmView(n.Provenance.LLM)
				d.ResourceTypes = resourceTypes(n.ResourceTypes)
				d.NotScored = !n.Counts()
			}
		}
	}
	return d, nil
}

func (sn *snapshot) serviceDetail(d *Detail, sv *catalog.Service) {
	d.Sources, d.LLM = sv.Provenance.Sources, llmView(sv.Provenance.LLM)
	d.ResourceTypes = resourceTypes(sv.ResourceTypes)
	for _, r := range sv.Traits {
		tv := TraitView{ID: r.ID, Level: string(r.Level), Override: string(r.Level), Reason: r.Reason, Status: "missing"}
		if t := sn.traits[r.ID]; t != nil {
			tv.Name, tv.Status, tv.File, tv.Default = t.Name, string(t.Provenance.Status), t.File, string(t.Level)
			if tv.Level == "" {
				tv.Level = tv.Default
			}
		}
		d.Traits = append(d.Traits, tv)
	}
	for _, m := range sv.Alternatives {
		mv := MappingView{Ref: m.Ref, Name: m.Ref, Status: "missing", Difficulty: m.Difficulty, Compatibility: m.Compatibility,
			Notes: m.Notes, Recommended: m.Recommended, ApprovedBy: m.ApprovedBy, Residual: len(m.ResidualTraits)}
		if a := sn.alts[m.Ref]; a != nil {
			mv.Name, mv.Kind, mv.Status, mv.File = a.Name, a.Kind, string(a.Provenance.Status), a.File
		}
		d.Mappings = append(d.Mappings, mv)
	}
}

func (sn *snapshot) serviceByFile(file string) *catalog.Service {
	for i := range sn.cat.Services {
		if sn.cat.Services[i].File == file {
			return &sn.cat.Services[i]
		}
	}
	return &catalog.Service{}
}

func (sn *snapshot) users(match func(*catalog.Service) (bool, bool)) []UsageView {
	var out []UsageView
	for i := range sn.cat.Services {
		sv := &sn.cat.Services[i]
		if ok, rec := match(sv); ok {
			out = append(out, UsageView{ID: sv.ID, Name: sv.Name, Status: string(sv.Provenance.Status), File: sv.File, Recommended: rec})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func resourceTypes(rt catalog.ResourceTypes) map[string]string {
	m := map[string]string{}
	add := func(k string, v []string) {
		if len(v) > 0 {
			m[k] = strings.Join(v, ", ")
		}
	}
	add("terraform", rt.Terraform)
	add("cloudformation", rt.CloudFormation)
	var k8s []string
	for _, gk := range rt.Kubernetes {
		k8s = append(k8s, gk.Group+"/"+gk.Kind)
	}
	add("kubernetes", k8s)
	add("container_registry", rt.ContainerRegistry)
	add("ci_action", rt.CIAction)
	return m
}

// mutate applies fn to the documents of files, writes them, and validates
// the whole catalog. When the edit adds problems, every file is restored.
func (s *Store) mutate(files []string, fn func(sn *snapshot, docs []*doc) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sn, err := s.load()
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, r := range sn.summaries() {
		known[r.File] = true
	}
	docs := make([]*doc, 0, len(files))
	for _, f := range files {
		if !known[f] {
			return fmt.Errorf("no catalog entry in %s", f)
		}
		d, err := loadDoc(filepath.Join(s.dir, filepath.FromSlash(f)))
		if err != nil {
			return err
		}
		docs = append(docs, d)
	}
	if err := fn(sn, docs); err != nil {
		return err
	}
	restore := func() {
		for _, d := range docs {
			_ = os.WriteFile(d.path, d.orig, 0o644)
		}
	}
	for _, d := range docs {
		b, err := d.encode()
		if err == nil {
			err = os.WriteFile(d.path, b, 0o644)
		}
		if err != nil {
			restore()
			return err
		}
	}
	after, err := s.load()
	if err != nil {
		restore()
		return err
	}
	if added := newProblems(sn.problems, after.problems); len(added) > 0 {
		restore()
		return &ValidationError{Problems: added}
	}
	return nil
}

func problemKey(p catalog.Problem) string { return p.File + "|" + p.EntryID + "|" + p.Message }

func newProblems(before, after []catalog.Problem) []string {
	seen := map[string]bool{}
	for _, p := range before {
		seen[problemKey(p)] = true
	}
	var out []string
	for _, p := range after {
		if !seen[problemKey(p)] {
			out = append(out, p.String())
		}
	}
	return out
}

func problemStrings(ps []catalog.Problem) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.String())
	}
	return out
}

// SetStatus changes the review status of entries.
//   - verified records reviewed_by and reviewed_on, marks LLM drafts as having
//     passed review (llm.eval_passed), and approves the service's mappings;
//   - draft removes the review record;
//   - deprecated keeps it (for alternatives this hides them everywhere).
func (s *Store) SetStatus(files []string, status string) error {
	if status != statusDraft && status != statusVerified && status != statusDeprecated {
		return fmt.Errorf("unknown status %q", status)
	}
	if len(files) == 0 {
		return errors.New("no entries selected")
	}
	today := s.now().Format("2006-01-02")
	return s.mutate(files, func(sn *snapshot, docs []*doc) error {
		for _, d := range docs {
			top := d.top()
			prov := mapGet(top, "provenance")
			if prov == nil {
				return fmt.Errorf("%s has no provenance", d.path)
			}
			mapSet(prov, "status", strNode(status), "author")
			llm := mapGet(prov, "llm")
			switch status {
			case statusVerified:
				mapSet(prov, "reviewed_by", strNode(s.reviewer), "author")
				mapSet(prov, "reviewed_on", dateNode(today), "reviewed_by")
				if llm != nil {
					mapSet(llm, "eval_passed", boolNode(true), "")
				}
				if scalar(top, "kind") == "service" {
					approveMappings(top, s.reviewer)
				}
			case statusDraft:
				mapDel(prov, "reviewed_by")
				mapDel(prov, "reviewed_on")
				if llm != nil {
					mapSet(llm, "eval_passed", boolNode(false), "")
				}
			}
		}
		return nil
	})
}

// SetRecommended marks or unmarks an alternative mapping of a service as
// recommended. Only catalog maintainers may recommend (FR-015, DM 1.8).
func (s *Store) SetRecommended(file, ref string, on bool) error {
	return s.mutate([]string{file}, func(sn *snapshot, docs []*doc) error {
		if on && !sn.maintainer(s.reviewer) {
			return fmt.Errorf("%s is not a catalog maintainer (maintainers: %s), so cannot recommend",
				s.reviewer, strings.Join(sn.cat.Manifest.Maintainers, ", "))
		}
		m := mapping(docs[0].top(), ref)
		if m == nil {
			return fmt.Errorf("%s has no alternative %s", file, ref)
		}
		if !on {
			mapDel(m, "recommended")
			return nil
		}
		mapSet(m, "recommended", boolNode(true), "")
		if !sn.maintainer(scalar(m, "approved_by")) {
			mapSet(m, "approved_by", strNode(s.reviewer), "recommended")
		}
		return nil
	})
}

// RemoveMapping deletes one alternative mapping from a service.
func (s *Store) RemoveMapping(file, ref string) error {
	return s.mutate([]string{file}, func(sn *snapshot, docs []*doc) error {
		seq := mapGet(docs[0].top(), "alternatives")
		if seq != nil {
			for i, it := range seq.Content {
				if scalar(it, "ref") == ref {
					seq.Content = append(seq.Content[:i], seq.Content[i+1:]...)
					return nil
				}
			}
		}
		return fmt.Errorf("%s has no alternative %s", file, ref)
	})
}

// levels are the lock-in levels in ascending order (data-model.md 1.5).
var levels = []string{"none", "low", "medium", "high", "critical"}

func validLevel(l string) bool { return slices.Contains(levels, l) }

// touchReview records the maintainer as the last reviewer of a verified
// entry whose content they changed, so reviewed_by/reviewed_on stay truthful.
func (s *Store) touchReview(top *yaml.Node) {
	prov := mapGet(top, "provenance")
	if prov == nil || scalar(prov, "status") != statusVerified {
		return
	}
	mapSet(prov, "reviewed_by", strNode(s.reviewer), "author")
	mapSet(prov, "reviewed_on", dateNode(s.now().Format("2006-01-02")), "reviewed_by")
}

// SetTraitLevel changes the default level of a trait. It applies to every
// service that attaches the trait without its own override.
func (s *Store) SetTraitLevel(file, level string) error {
	if !validLevel(level) {
		return fmt.Errorf("unknown level %q (use %s)", level, strings.Join(levels, ", "))
	}
	return s.mutate([]string{file}, func(sn *snapshot, docs []*doc) error {
		top := docs[0].top()
		if scalar(top, "kind") != "trait" {
			return fmt.Errorf("%s is not a trait", file)
		}
		mapSet(top, "level", strNode(level), "name")
		s.touchReview(top)
		return nil
	})
}

// SetTraitRefLevel overrides the level of one trait on one service, with the
// reason the catalog requires (DM 1.6). An empty level removes the override,
// so the trait's default level applies again.
func (s *Store) SetTraitRefLevel(file, traitID, level, reason string) error {
	reason = strings.TrimSpace(reason)
	if level != "" && !validLevel(level) {
		return fmt.Errorf("unknown level %q (use %s)", level, strings.Join(levels, ", "))
	}
	if level != "" && reason == "" {
		return errors.New("a level override needs a reason")
	}
	return s.mutate([]string{file}, func(sn *snapshot, docs []*doc) error {
		top := docs[0].top()
		seq := mapGet(top, "traits")
		if scalar(top, "kind") != "service" || seq == nil || seq.Kind != yaml.SequenceNode {
			return fmt.Errorf("%s is not a service with traits", file)
		}
		for _, it := range seq.Content {
			if it.Kind != yaml.MappingNode || scalar(it, "id") != traitID {
				continue
			}
			if level == "" {
				mapDel(it, "level")
				mapDel(it, "reason")
			} else {
				mapSet(it, "level", strNode(level), "id")
				mapSet(it, "reason", quotedNode(reason), "level")
			}
			s.touchReview(top)
			return nil
		}
		return fmt.Errorf("%s does not attach trait %s", file, traitID)
	})
}
