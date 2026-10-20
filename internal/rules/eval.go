// Package rules evaluates declarative catalog rules with three-valued logic
// (data-model.md 1.7, research R14).
package rules

import (
	"path"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/cloudowski/exitctl/internal/catalog"
	"github.com/cloudowski/exitctl/internal/model"
)

// Tri is a three-valued truth value.
type Tri int

// Truth values.
const (
	False Tri = iota
	True
	Unknown
)

// EdgeRef describes one edge of the evaluated node for edge_to conditions.
type EdgeRef struct {
	Service  string // service ID of the node on the other end ("" if none)
	Category string // category ID of that service ("" if none)
	Kind     string // edge kind
}

// NodeContext is everything a rule can look at.
type NodeContext struct {
	Type        string
	Service     string
	Category    string
	SourceKind  string
	Traits      []string       // trait IDs currently on the node
	Attributes  map[string]any // literal attributes; model.Unknown for non-literals
	Out         []EdgeRef      // edges where this node depends on another (direction "out")
	In          []EdgeRef      // edges where another node depends on this node (direction "in")
	HasSDKUsage bool
}

// Outcome is the effect of one fired rule.
type Outcome struct {
	RuleID       string
	AddTrait     string
	Level        catalog.Level // level for AddTrait ("" = trait default)
	SetLevel     *catalog.SetLevel
	EvidenceAttr string
}

// Matches reports whether a selector matches the node.
func Matches(sel catalog.Selector, ctx *NodeContext) bool {
	if ctx == nil {
		ctx = &NodeContext{}
	}
	if len(sel.ResourceType) > 0 && !anyOf(sel.ResourceType, func(p string) bool {
		ok, err := path.Match(p, ctx.Type)
		return err == nil && ok
	}) {
		return false
	}
	if len(sel.Service) > 0 && !anyOf(sel.Service, func(s string) bool { return s == ctx.Service }) {
		return false
	}
	if len(sel.Category) > 0 && !anyOf(sel.Category, func(c string) bool { return categoryMatch(c, ctx.Category) }) {
		return false
	}
	if len(sel.Trait) > 0 && !anyOf(sel.Trait, func(t string) bool { return contains(ctx.Traits, t) }) {
		return false
	}
	if len(sel.Source) > 0 && !anyOf(sel.Source, func(s string) bool { return s == ctx.SourceKind }) {
		return false
	}
	return true
}

// Eval evaluates a condition. A nil condition is True.
func Eval(c *catalog.Condition, ctx *NodeContext) Tri {
	if c == nil {
		return True
	}
	if ctx == nil {
		ctx = &NodeContext{}
	}
	var parts []Tri
	if c.Attr != "" {
		parts = append(parts, evalAttrOps(c, ctx)...)
	}
	if c.All != nil {
		parts = append(parts, evalAll(c.All, ctx))
	}
	if c.Any != nil {
		parts = append(parts, evalAny(c.Any, ctx))
	}
	if c.Not != nil {
		parts = append(parts, not(Eval(c.Not, ctx)))
	}
	if c.EdgeTo != nil {
		parts = append(parts, evalEdgeTo(c.EdgeTo, ctx))
	}
	if c.HasSDKUsage {
		parts = append(parts, boolTri(ctx.HasSDKUsage))
	}
	return and(parts)
}

// Apply runs service rules then global rules (each sorted by rule ID),
// returning outcomes of rules that evaluated True and the IDs of rules that
// evaluated Unknown (recorded as "undetermined_rule:<id>" notes by callers).
// Traits added by earlier rules are visible to later rules' selectors.
func Apply(serviceRules, globalRules []catalog.Rule, ctx *NodeContext) (fired []Outcome, undetermined []string) {
	if ctx == nil {
		ctx = &NodeContext{}
	}
	work := *ctx
	work.Traits = append([]string(nil), ctx.Traits...)

	run := func(rs []catalog.Rule) {
		sorted := append([]catalog.Rule(nil), rs...)
		sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
		for i := range sorted {
			r := &sorted[i]
			if !Matches(r.Match, &work) {
				continue
			}
			switch Eval(r.When, &work) {
			case True:
				fired = append(fired, Outcome{
					RuleID:       r.ID,
					AddTrait:     r.Then.AddTrait,
					Level:        r.Then.Level,
					SetLevel:     r.Then.SetLevel,
					EvidenceAttr: r.Then.Evidence,
				})
				if r.Then.AddTrait != "" && !contains(work.Traits, r.Then.AddTrait) {
					work.Traits = append(work.Traits, r.Then.AddTrait)
				}
			case Unknown:
				undetermined = append(undetermined, r.ID)
			}
		}
	}
	run(serviceRules)
	run(globalRules)
	return fired, undetermined
}

// --- three-valued combinators ---

func boolTri(b bool) Tri {
	if b {
		return True
	}
	return False
}

func not(t Tri) Tri {
	switch t {
	case True:
		return False
	case False:
		return True
	}
	return Unknown
}

// and: False if any False, else Unknown if any Unknown, else True (empty = True).
func and(ts []Tri) Tri {
	res := True
	for _, t := range ts {
		if t == False {
			return False
		}
		if t == Unknown {
			res = Unknown
		}
	}
	return res
}

// or: True if any True, else Unknown if any Unknown, else False (empty = False).
func or(ts []Tri) Tri {
	res := False
	for _, t := range ts {
		if t == True {
			return True
		}
		if t == Unknown {
			res = Unknown
		}
	}
	return res
}

func evalAll(cs []*catalog.Condition, ctx *NodeContext) Tri {
	ts := make([]Tri, 0, len(cs))
	for _, c := range cs {
		ts = append(ts, Eval(c, ctx))
	}
	return and(ts)
}

func evalAny(cs []*catalog.Condition, ctx *NodeContext) Tri {
	ts := make([]Tri, 0, len(cs))
	for _, c := range cs {
		ts = append(ts, Eval(c, ctx))
	}
	return or(ts)
}

// --- attribute operators ---

// leafFunc evaluates an operator against a resolved leaf value. present is
// false when the attribute is missing.
type leafFunc func(v any, present bool) Tri

func evalAttrOps(c *catalog.Condition, ctx *NodeContext) []Tri {
	segs := strings.Split(c.Attr, ".")
	var root any
	if ctx.Attributes != nil {
		root = ctx.Attributes
	}
	var ops []leafFunc
	if c.Exists != nil {
		want := *c.Exists
		ops = append(ops, func(v any, present bool) Tri {
			if present && isList(v) && listLen(v) == 0 {
				present = false
			}
			return boolTri(present == want)
		})
	}
	if c.Equals != nil {
		want := c.Equals
		ops = append(ops, compareLeaf(func(v any) bool { return valuesEqual(v, want) }, want))
	}
	if c.In != nil {
		set := c.In
		ops = append(ops, compareLeaf(func(v any) bool {
			for _, w := range set {
				if valuesEqual(v, w) {
					return true
				}
			}
			return false
		}, nil))
	}
	if c.Matches != "" {
		re := compileCached(c.Matches)
		ops = append(ops, compareLeaf(func(v any) bool {
			s, ok := v.(string)
			return ok && re != nil && re.MatchString(s)
		}, nil))
	}
	if c.Gte != nil {
		bound := *c.Gte
		ops = append(ops, compareLeaf(func(v any) bool {
			f, ok := toFloat(v)
			return ok && f >= bound
		}, nil))
	}
	if c.Lte != nil {
		bound := *c.Lte
		ops = append(ops, compareLeaf(func(v any) bool {
			f, ok := toFloat(v)
			return ok && f <= bound
		}, nil))
	}
	if len(ops) == 0 {
		// An attr with no operator is treated as an existence test.
		ops = append(ops, func(v any, present bool) Tri {
			if present && isList(v) && listLen(v) == 0 {
				return False
			}
			return boolTri(present)
		})
	}
	out := make([]Tri, 0, len(ops))
	for _, op := range ops {
		out = append(out, resolve(root, segs, op))
	}
	return out
}

// compareLeaf wraps a value predicate with missing/unknown/redacted/list
// handling. whole, when non-nil, lets a list leaf match as a whole first
// (e.g. equals with a list operand).
func compareLeaf(pred func(any) bool, whole any) leafFunc {
	var leaf leafFunc
	leaf = func(v any, present bool) Tri {
		if !present {
			return False
		}
		if model.IsUnknown(v) || model.IsRedacted(v) {
			return Unknown
		}
		if isList(v) {
			if whole != nil && isList(whole) && pred(v) {
				return True
			}
			rv := reflect.ValueOf(v)
			ts := make([]Tri, 0, rv.Len())
			for i := 0; i < rv.Len(); i++ {
				e := rv.Index(i).Interface()
				ts = append(ts, leaf(e, e != nil))
			}
			return or(ts)
		}
		return boolTri(pred(v))
	}
	return leaf
}

// resolve walks the dotted path from v and applies leaf to every reached
// value, combining results with any-semantics across list elements.
func resolve(v any, segs []string, leaf leafFunc) Tri {
	if v == nil {
		return leaf(nil, false)
	}
	if len(segs) == 0 {
		return leaf(v, true)
	}
	if model.IsUnknown(v) || model.IsRedacted(v) {
		// An unknown or redacted parent: the child may or may not exist.
		return Unknown
	}
	if isList(v) {
		rv := reflect.ValueOf(v)
		if rv.Len() == 0 {
			return leaf(nil, false)
		}
		ts := make([]Tri, 0, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			ts = append(ts, resolve(rv.Index(i).Interface(), segs, leaf))
		}
		return or(ts)
	}
	child, ok := lookup(v, segs[0])
	if !ok {
		return leaf(nil, false)
	}
	return resolve(child, segs[1:], leaf)
}

func lookup(v any, key string) (any, bool) {
	if m, ok := v.(map[string]any); ok {
		c, ok := m[key]
		return c, ok
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Map || rv.Type().Key().Kind() != reflect.String {
		return nil, false
	}
	e := rv.MapIndex(reflect.ValueOf(key).Convert(rv.Type().Key()))
	if !e.IsValid() {
		return nil, false
	}
	return e.Interface(), true
}

func isList(v any) bool {
	if v == nil {
		return false
	}
	if _, ok := v.([]byte); ok {
		return false
	}
	k := reflect.TypeOf(v).Kind()
	return k == reflect.Slice || k == reflect.Array
}

func listLen(v any) int { return reflect.ValueOf(v).Len() }

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int8:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint8:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	case float32:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}

func valuesEqual(a, b any) bool {
	if fa, ok := toFloat(a); ok {
		fb, ok := toFloat(b)
		return ok && fa == fb
	}
	switch x := a.(type) {
	case string:
		y, ok := b.(string)
		return ok && x == y
	case bool:
		y, ok := b.(bool)
		return ok && x == y
	}
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if isList(a) && isList(b) {
		ra, rb := reflect.ValueOf(a), reflect.ValueOf(b)
		if ra.Len() != rb.Len() {
			return false
		}
		for i := 0; i < ra.Len(); i++ {
			if !valuesEqual(ra.Index(i).Interface(), rb.Index(i).Interface()) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(a, b)
}

var reCache sync.Map // pattern -> *regexp.Regexp (nil for invalid patterns)

func compileCached(p string) *regexp.Regexp {
	if v, ok := reCache.Load(p); ok {
		return v.(*regexp.Regexp)
	}
	re, err := regexp.Compile(p)
	if err != nil {
		re = nil
	}
	v, _ := reCache.LoadOrStore(p, re)
	return v.(*regexp.Regexp)
}

// --- context operators ---

func evalEdgeTo(e *catalog.EdgeTo, ctx *NodeContext) Tri {
	edges := ctx.Out
	if e.Direction == "in" {
		edges = ctx.In
	}
	for _, ed := range edges {
		if e.Service != "" && ed.Service != e.Service {
			continue
		}
		if e.Category != "" && !categoryMatch(e.Category, ed.Category) {
			continue
		}
		if e.Kind != "" && ed.Kind != e.Kind {
			continue
		}
		return True
	}
	return False
}

// categoryMatch reports whether category id equals prefix or lies under it
// in the dotted taxonomy.
func categoryMatch(prefix, id string) bool {
	if prefix == "" || id == "" {
		return prefix == id
	}
	return id == prefix || strings.HasPrefix(id, prefix+".")
}

func anyOf(xs []string, f func(string) bool) bool {
	for _, x := range xs {
		if f(x) {
			return true
		}
	}
	return false
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
