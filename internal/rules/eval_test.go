package rules

import (
	"reflect"
	"testing"

	"github.com/cloudowski/exitctl/internal/catalog"
	"github.com/cloudowski/exitctl/internal/model"
)

func bp(b bool) *bool       { return &b }
func fp(f float64) *float64 { return &f }
func lit(t Tri) *catalog.Condition {
	// Encodes a fixed truth value as a condition over a fixed context.
	switch t {
	case True:
		return &catalog.Condition{Attr: "t", Equals: true}
	case False:
		return &catalog.Condition{Attr: "f", Equals: true}
	}
	return &catalog.Condition{Attr: "u", Equals: true}
}

var litCtx = &NodeContext{Attributes: map[string]any{"t": true, "f": false, "u": model.Unknown}}

func triName(t Tri) string {
	switch t {
	case True:
		return "T"
	case False:
		return "F"
	}
	return "U"
}

func TestLiterals(t *testing.T) {
	for _, v := range []Tri{True, False, Unknown} {
		if got := Eval(lit(v), litCtx); got != v {
			t.Fatalf("lit(%s) = %s", triName(v), triName(got))
		}
	}
}

func TestTruthTables(t *testing.T) {
	vals := []Tri{True, False, Unknown}
	andTab := map[[2]Tri]Tri{
		{True, True}: True, {True, False}: False, {True, Unknown}: Unknown,
		{False, True}: False, {False, False}: False, {False, Unknown}: False,
		{Unknown, True}: Unknown, {Unknown, False}: False, {Unknown, Unknown}: Unknown,
	}
	orTab := map[[2]Tri]Tri{
		{True, True}: True, {True, False}: True, {True, Unknown}: True,
		{False, True}: True, {False, False}: False, {False, Unknown}: Unknown,
		{Unknown, True}: True, {Unknown, False}: Unknown, {Unknown, Unknown}: Unknown,
	}
	for _, a := range vals {
		for _, b := range vals {
			c := &catalog.Condition{All: []*catalog.Condition{lit(a), lit(b)}}
			if got, want := Eval(c, litCtx), andTab[[2]Tri{a, b}]; got != want {
				t.Errorf("all(%s,%s) = %s, want %s", triName(a), triName(b), triName(got), triName(want))
			}
			c = &catalog.Condition{Any: []*catalog.Condition{lit(a), lit(b)}}
			if got, want := Eval(c, litCtx), orTab[[2]Tri{a, b}]; got != want {
				t.Errorf("any(%s,%s) = %s, want %s", triName(a), triName(b), triName(got), triName(want))
			}
		}
	}
	notTab := map[Tri]Tri{True: False, False: True, Unknown: Unknown}
	for _, a := range vals {
		if got := Eval(&catalog.Condition{Not: lit(a)}, litCtx); got != notTab[a] {
			t.Errorf("not(%s) = %s", triName(a), triName(got))
		}
	}
	// Empty combinators.
	if got := Eval(&catalog.Condition{All: []*catalog.Condition{}}, litCtx); got != True {
		t.Errorf("all() = %s", triName(got))
	}
	if got := Eval(&catalog.Condition{Any: []*catalog.Condition{}}, litCtx); got != False {
		t.Errorf("any() = %s", triName(got))
	}
	// Three-way.
	if got := Eval(&catalog.Condition{All: []*catalog.Condition{lit(True), lit(Unknown), lit(False)}}, litCtx); got != False {
		t.Errorf("all(T,U,F) = %s", triName(got))
	}
	if got := Eval(&catalog.Condition{Any: []*catalog.Condition{lit(False), lit(Unknown), lit(True)}}, litCtx); got != True {
		t.Errorf("any(F,U,T) = %s", triName(got))
	}
	// Nil and empty conditions.
	if Eval(nil, nil) != True {
		t.Error("nil condition must be True")
	}
	if Eval(&catalog.Condition{}, litCtx) != True {
		t.Error("empty condition must be True")
	}
	// Several groups in one condition combine with all-semantics.
	c := &catalog.Condition{Attr: "t", Equals: true, Not: lit(True)}
	if got := Eval(c, litCtx); got != False {
		t.Errorf("multi-group T and F = %s", triName(got))
	}
	c = &catalog.Condition{Attr: "t", Equals: true, Any: []*catalog.Condition{lit(Unknown)}}
	if got := Eval(c, litCtx); got != Unknown {
		t.Errorf("multi-group T and U = %s", triName(got))
	}
}

func TestAttributeOperators(t *testing.T) {
	attrs := map[string]any{
		"s":       "NEW_IMAGE",
		"i":       5,
		"i64":     int64(7),
		"f":       2.5,
		"b":       true,
		"null":    nil,
		"unk":     model.Unknown,
		"red":     model.Redacted,
		"tags":    []any{"a", "b"},
		"nums":    []any{1, 10},
		"mixed":   []any{"x", model.Unknown},
		"empty":   []any{},
		"ttl":     []any{map[string]any{"enabled": true, "attribute_name": "exp"}},
		"multi":   []any{map[string]any{"v": 1}, map[string]any{"v": 2}},
		"unkblk":  []any{map[string]any{"v": model.Unknown}, map[string]any{"v": 9}},
		"nested":  map[string]any{"a": map[string]any{"b": "deep"}},
		"unkpar":  model.Unknown,
		"strmap":  map[string]string{"k": "v"},
		"noblock": []any{},
	}
	ctx := &NodeContext{Attributes: attrs}
	cases := []struct {
		name string
		c    catalog.Condition
		want Tri
	}{
		// exists
		{"exists present", catalog.Condition{Attr: "s", Exists: bp(true)}, True},
		{"exists missing", catalog.Condition{Attr: "zz", Exists: bp(true)}, False},
		{"exists false missing", catalog.Condition{Attr: "zz", Exists: bp(false)}, True},
		{"exists false present", catalog.Condition{Attr: "s", Exists: bp(false)}, False},
		{"exists null", catalog.Condition{Attr: "null", Exists: bp(true)}, False},
		{"exists unknown", catalog.Condition{Attr: "unk", Exists: bp(true)}, True},
		{"exists false unknown", catalog.Condition{Attr: "unk", Exists: bp(false)}, False},
		{"exists redacted", catalog.Condition{Attr: "red", Exists: bp(true)}, True},
		{"exists nested", catalog.Condition{Attr: "nested.a.b", Exists: bp(true)}, True},
		{"exists nested missing", catalog.Condition{Attr: "nested.a.c", Exists: bp(true)}, False},
		{"exists through scalar", catalog.Condition{Attr: "s.x", Exists: bp(true)}, False},
		{"exists block", catalog.Condition{Attr: "ttl.enabled", Exists: bp(true)}, True},
		{"exists empty block", catalog.Condition{Attr: "noblock.enabled", Exists: bp(true)}, False},
		{"exists empty list leaf", catalog.Condition{Attr: "empty", Exists: bp(true)}, False},
		{"exists list leaf", catalog.Condition{Attr: "tags", Exists: bp(true)}, True},
		{"exists under unknown parent", catalog.Condition{Attr: "unkpar.x", Exists: bp(true)}, Unknown},
		{"exists typed map", catalog.Condition{Attr: "strmap.k", Exists: bp(true)}, True},
		{"attr without op", catalog.Condition{Attr: "s"}, True},
		{"attr without op missing", catalog.Condition{Attr: "zz"}, False},
		// equals
		{"equals string", catalog.Condition{Attr: "s", Equals: "NEW_IMAGE"}, True},
		{"equals string ne", catalog.Condition{Attr: "s", Equals: "OLD"}, False},
		{"equals int float", catalog.Condition{Attr: "i", Equals: 5.0}, True},
		{"equals int64 int", catalog.Condition{Attr: "i64", Equals: 7}, True},
		{"equals float", catalog.Condition{Attr: "f", Equals: 2.5}, True},
		{"equals bool", catalog.Condition{Attr: "b", Equals: true}, True},
		{"equals bool ne", catalog.Condition{Attr: "b", Equals: false}, False},
		{"equals string vs bool", catalog.Condition{Attr: "s", Equals: true}, False},
		{"equals string vs number", catalog.Condition{Attr: "i", Equals: "5"}, False},
		{"equals missing", catalog.Condition{Attr: "zz", Equals: "x"}, False},
		{"equals null", catalog.Condition{Attr: "null", Equals: "x"}, False},
		{"equals unknown", catalog.Condition{Attr: "unk", Equals: "x"}, Unknown},
		{"equals redacted", catalog.Condition{Attr: "red", Equals: "x"}, Unknown},
		{"equals nested", catalog.Condition{Attr: "nested.a.b", Equals: "deep"}, True},
		{"equals list element", catalog.Condition{Attr: "tags", Equals: "b"}, True},
		{"equals list element none", catalog.Condition{Attr: "tags", Equals: "c"}, False},
		{"equals whole list", catalog.Condition{Attr: "tags", Equals: []any{"a", "b"}}, True},
		{"equals mixed list unknown", catalog.Condition{Attr: "mixed", Equals: "y"}, Unknown},
		{"equals mixed list hit", catalog.Condition{Attr: "mixed", Equals: "x"}, True},
		{"equals block", catalog.Condition{Attr: "ttl.enabled", Equals: true}, True},
		{"equals multi block any", catalog.Condition{Attr: "multi.v", Equals: 2}, True},
		{"equals multi block none", catalog.Condition{Attr: "multi.v", Equals: 3}, False},
		{"equals block unknown elem miss", catalog.Condition{Attr: "unkblk.v", Equals: 3}, Unknown},
		{"equals block unknown elem hit", catalog.Condition{Attr: "unkblk.v", Equals: 9}, True},
		{"equals under unknown parent", catalog.Condition{Attr: "unkpar.x", Equals: 1}, Unknown},
		{"equals typed map", catalog.Condition{Attr: "strmap.k", Equals: "v"}, True},
		// in
		{"in hit", catalog.Condition{Attr: "s", In: []any{"NEW_AND_OLD_IMAGES", "NEW_IMAGE"}}, True},
		{"in miss", catalog.Condition{Attr: "s", In: []any{"KEYS_ONLY"}}, False},
		{"in numeric", catalog.Condition{Attr: "i", In: []any{1, 5.0}}, True},
		{"in empty", catalog.Condition{Attr: "s", In: []any{}}, False},
		{"in missing", catalog.Condition{Attr: "zz", In: []any{"x"}}, False},
		{"in unknown", catalog.Condition{Attr: "unk", In: []any{"x"}}, Unknown},
		{"in redacted", catalog.Condition{Attr: "red", In: []any{"x"}}, Unknown},
		{"in list attr", catalog.Condition{Attr: "tags", In: []any{"z", "a"}}, True},
		// matches
		{"matches hit", catalog.Condition{Attr: "s", Matches: "^NEW_"}, True},
		{"matches miss", catalog.Condition{Attr: "s", Matches: "^OLD"}, False},
		{"matches non-string", catalog.Condition{Attr: "i", Matches: "5"}, False},
		{"matches missing", catalog.Condition{Attr: "zz", Matches: "."}, False},
		{"matches unknown", catalog.Condition{Attr: "unk", Matches: "."}, Unknown},
		{"matches redacted", catalog.Condition{Attr: "red", Matches: "."}, Unknown},
		{"matches invalid regexp", catalog.Condition{Attr: "s", Matches: "("}, False},
		{"matches list", catalog.Condition{Attr: "tags", Matches: "^b$"}, True},
		{"matches block", catalog.Condition{Attr: "ttl.attribute_name", Matches: "^ex"}, True},
		// gte / lte
		{"gte hit", catalog.Condition{Attr: "i", Gte: fp(5)}, True},
		{"gte miss", catalog.Condition{Attr: "i", Gte: fp(6)}, False},
		{"gte float", catalog.Condition{Attr: "f", Gte: fp(2.4)}, True},
		{"gte non-numeric", catalog.Condition{Attr: "s", Gte: fp(0)}, False},
		{"gte bool", catalog.Condition{Attr: "b", Gte: fp(0)}, False},
		{"gte missing", catalog.Condition{Attr: "zz", Gte: fp(0)}, False},
		{"gte unknown", catalog.Condition{Attr: "unk", Gte: fp(0)}, Unknown},
		{"gte list any", catalog.Condition{Attr: "nums", Gte: fp(9)}, True},
		{"lte hit", catalog.Condition{Attr: "i", Lte: fp(5)}, True},
		{"lte miss", catalog.Condition{Attr: "i", Lte: fp(4)}, False},
		{"lte non-numeric", catalog.Condition{Attr: "s", Lte: fp(100)}, False},
		{"lte redacted", catalog.Condition{Attr: "red", Lte: fp(0)}, Unknown},
		{"lte list any", catalog.Condition{Attr: "nums", Lte: fp(1)}, True},
		{"lte list none", catalog.Condition{Attr: "nums", Lte: fp(0)}, False},
		// several operators on one attribute: all-semantics
		{"gte and lte range", catalog.Condition{Attr: "i", Gte: fp(1), Lte: fp(10)}, True},
		{"gte and lte outside", catalog.Condition{Attr: "i", Gte: fp(6), Lte: fp(10)}, False},
		{"exists and equals unknown", catalog.Condition{Attr: "unk", Exists: bp(true), Equals: "x"}, Unknown},
		{"exists false and equals unknown", catalog.Condition{Attr: "unk", Exists: bp(false), Equals: "x"}, False},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := tc.c
			if got := Eval(&c, ctx); got != tc.want {
				t.Fatalf("got %s, want %s", triName(got), triName(tc.want))
			}
		})
	}
}

func TestNilAttributes(t *testing.T) {
	ctx := &NodeContext{}
	if got := Eval(&catalog.Condition{Attr: "a", Exists: bp(true)}, ctx); got != False {
		t.Errorf("exists on nil attrs = %s", triName(got))
	}
	if got := Eval(&catalog.Condition{Attr: "a", Exists: bp(false)}, ctx); got != True {
		t.Errorf("exists false on nil attrs = %s", triName(got))
	}
	if got := Eval(&catalog.Condition{Attr: "a", Equals: 1}, ctx); got != False {
		t.Errorf("equals on nil attrs = %s", triName(got))
	}
}

func TestDataModelExample(t *testing.T) {
	// The example from data-model.md 1.7.
	cond := &catalog.Condition{All: []*catalog.Condition{
		{Attr: "stream_enabled", Equals: true},
		{Any: []*catalog.Condition{
			{Attr: "stream_view_type", In: []any{"NEW_IMAGE", "NEW_AND_OLD_IMAGES"}},
			{Not: &catalog.Condition{Attr: "ttl.enabled", Exists: bp(true)}},
		}},
		{EdgeTo: &catalog.EdgeTo{Service: "aws.lambda", Kind: "data-flow", Direction: "in"}},
	}}
	lambdaIn := []EdgeRef{{Service: "aws.lambda", Category: "compute.faas", Kind: "data-flow"}}
	cases := []struct {
		name  string
		attrs map[string]any
		in    []EdgeRef
		want  Tri
	}{
		{"fires", map[string]any{"stream_enabled": true, "stream_view_type": "NEW_IMAGE"}, lambdaIn, True},
		{"fires via no ttl", map[string]any{"stream_enabled": true, "stream_view_type": "KEYS_ONLY"}, lambdaIn, True},
		{"ttl present blocks", map[string]any{"stream_enabled": true, "stream_view_type": "KEYS_ONLY",
			"ttl": []any{map[string]any{"enabled": true}}}, lambdaIn, False},
		{"stream disabled", map[string]any{"stream_enabled": false, "stream_view_type": "NEW_IMAGE"}, lambdaIn, False},
		{"stream unknown", map[string]any{"stream_enabled": model.Unknown, "stream_view_type": "NEW_IMAGE"}, lambdaIn, Unknown},
		{"stream unknown but no edge", map[string]any{"stream_enabled": model.Unknown, "stream_view_type": "NEW_IMAGE"}, nil, False},
		{"view type unknown, ttl present", map[string]any{"stream_enabled": true, "stream_view_type": model.Unknown,
			"ttl": []any{map[string]any{"enabled": true}}}, lambdaIn, Unknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := &NodeContext{Attributes: tc.attrs, In: tc.in}
			if got := Eval(cond, ctx); got != tc.want {
				t.Fatalf("got %s, want %s", triName(got), triName(tc.want))
			}
		})
	}
}

func TestEdgeTo(t *testing.T) {
	ctx := &NodeContext{
		Out: []EdgeRef{
			{Service: "aws.kms", Category: "security.kms", Kind: "reference"},
			{Service: "", Category: "", Kind: "containment"},
		},
		In: []EdgeRef{
			{Service: "aws.lambda", Category: "compute.faas", Kind: "data-flow"},
		},
	}
	cases := []struct {
		name string
		e    catalog.EdgeTo
		want Tri
	}{
		{"out service default dir", catalog.EdgeTo{Service: "aws.kms"}, True},
		{"out service explicit", catalog.EdgeTo{Service: "aws.kms", Direction: "out"}, True},
		{"out service wrong dir", catalog.EdgeTo{Service: "aws.kms", Direction: "in"}, False},
		{"in service", catalog.EdgeTo{Service: "aws.lambda", Direction: "in"}, True},
		{"in service default dir", catalog.EdgeTo{Service: "aws.lambda"}, False},
		{"service and kind", catalog.EdgeTo{Service: "aws.kms", Kind: "reference"}, True},
		{"service wrong kind", catalog.EdgeTo{Service: "aws.kms", Kind: "data-flow"}, False},
		{"category exact", catalog.EdgeTo{Category: "security.kms"}, True},
		{"category prefix", catalog.EdgeTo{Category: "security"}, True},
		{"category non-boundary prefix", catalog.EdgeTo{Category: "secur"}, False},
		{"category prefix in", catalog.EdgeTo{Category: "compute", Direction: "in", Kind: "data-flow"}, True},
		{"kind only", catalog.EdgeTo{Kind: "containment"}, True},
		{"kind only missing", catalog.EdgeTo{Kind: "usage"}, False},
		{"empty matches any edge", catalog.EdgeTo{}, True},
		{"service mismatch", catalog.EdgeTo{Service: "aws.s3"}, False},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := tc.e
			if got := Eval(&catalog.Condition{EdgeTo: &e}, ctx); got != tc.want {
				t.Fatalf("got %s, want %s", triName(got), triName(tc.want))
			}
		})
	}
	if got := Eval(&catalog.Condition{EdgeTo: &catalog.EdgeTo{}}, &NodeContext{}); got != False {
		t.Errorf("edge_to with no edges = %s", triName(got))
	}
}

func TestHasSDKUsage(t *testing.T) {
	c := &catalog.Condition{HasSDKUsage: true}
	if Eval(c, &NodeContext{HasSDKUsage: true}) != True {
		t.Error("want True")
	}
	if Eval(c, &NodeContext{}) != False {
		t.Error("want False")
	}
	if Eval(&catalog.Condition{Not: c}, &NodeContext{}) != True {
		t.Error("not has_sdk_usage want True")
	}
}

func TestMatches(t *testing.T) {
	ctx := &NodeContext{
		Type:       "aws_dynamodb_table",
		Service:    "aws.dynamodb",
		Category:   "database.nosql.document",
		SourceKind: "terraform",
		Traits:     []string{"proprietary-api", "change-stream"},
	}
	cases := []struct {
		name string
		sel  catalog.Selector
		want bool
	}{
		{"empty", catalog.Selector{}, true},
		{"type exact", catalog.Selector{ResourceType: []string{"aws_dynamodb_table"}}, true},
		{"type glob", catalog.Selector{ResourceType: []string{"aws_dynamodb_*"}}, true},
		{"type glob any", catalog.Selector{ResourceType: []string{"google_*", "aws_*_table"}}, true},
		{"type glob miss", catalog.Selector{ResourceType: []string{"aws_s3_*"}}, false},
		{"type bad pattern", catalog.Selector{ResourceType: []string{"aws_["}}, false},
		{"service", catalog.Selector{Service: []string{"aws.s3", "aws.dynamodb"}}, true},
		{"service miss", catalog.Selector{Service: []string{"aws.dynamo"}}, false},
		{"category exact", catalog.Selector{Category: []string{"database.nosql.document"}}, true},
		{"category prefix", catalog.Selector{Category: []string{"database"}}, true},
		{"category prefix 2", catalog.Selector{Category: []string{"database.nosql"}}, true},
		{"category non-boundary", catalog.Selector{Category: []string{"database.no"}}, false},
		{"category other", catalog.Selector{Category: []string{"storage"}}, false},
		{"trait", catalog.Selector{Trait: []string{"x", "change-stream"}}, true},
		{"trait miss", catalog.Selector{Trait: []string{"x"}}, false},
		{"source", catalog.Selector{Source: []string{"terraform"}}, true},
		{"source miss", catalog.Selector{Source: []string{"cloudformation"}}, false},
		{"all fields", catalog.Selector{
			ResourceType: []string{"aws_*"}, Service: []string{"aws.dynamodb"},
			Category: []string{"database"}, Trait: []string{"proprietary-api"}, Source: []string{"terraform"},
		}, true},
		{"one field fails", catalog.Selector{
			ResourceType: []string{"aws_*"}, Service: []string{"aws.dynamodb"}, Source: []string{"kubernetes"},
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Matches(tc.sel, ctx); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
	if Matches(catalog.Selector{Category: []string{"database"}}, &NodeContext{}) {
		t.Error("category selector must not match a node without category")
	}
}

func TestApply(t *testing.T) {
	traits := []string{"base"}
	ctx := &NodeContext{
		Type:       "aws_dynamodb_table",
		Service:    "aws.dynamodb",
		Traits:     traits,
		Attributes: map[string]any{"stream_enabled": true, "pitr": model.Unknown},
	}
	svc := []catalog.Rule{
		// Deliberately out of ID order.
		{ID: "svc.c", When: &catalog.Condition{Attr: "pitr", Equals: true}, Then: catalog.Effect{AddTrait: "never"}},
		{ID: "svc.a", When: &catalog.Condition{Attr: "stream_enabled", Equals: true},
			Then: catalog.Effect{AddTrait: "change-stream", Level: catalog.LevelHigh, Evidence: "stream_enabled"}},
		{ID: "svc.b", Match: catalog.Selector{Trait: []string{"change-stream"}},
			Then: catalog.Effect{SetLevel: &catalog.SetLevel{Trait: "base", Level: catalog.LevelLow}}},
		{ID: "svc.d", Match: catalog.Selector{Service: []string{"aws.s3"}},
			When: &catalog.Condition{Attr: "pitr", Equals: true}},
		{ID: "svc.e", When: &catalog.Condition{Attr: "stream_enabled", Equals: false}, Then: catalog.Effect{AddTrait: "no"}},
	}
	glob := []catalog.Rule{
		{ID: "g.z", Match: catalog.Selector{Trait: []string{"from-global-a"}}, Then: catalog.Effect{AddTrait: "from-global-z"}},
		{ID: "g.a", Match: catalog.Selector{Trait: []string{"change-stream"}}, Then: catalog.Effect{AddTrait: "from-global-a"}},
		{ID: "g.0", Match: catalog.Selector{Trait: []string{"from-global-a"}}, Then: catalog.Effect{AddTrait: "too-early"}},
	}
	fired, undet := Apply(svc, glob, ctx)
	gotIDs := make([]string, len(fired))
	for i, o := range fired {
		gotIDs[i] = o.RuleID
	}
	wantIDs := []string{"svc.a", "svc.b", "g.a", "g.z"}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("fired = %v, want %v", gotIDs, wantIDs)
	}
	if !reflect.DeepEqual(undet, []string{"svc.c"}) {
		t.Fatalf("undetermined = %v", undet)
	}
	want0 := Outcome{RuleID: "svc.a", AddTrait: "change-stream", Level: catalog.LevelHigh, EvidenceAttr: "stream_enabled"}
	if !reflect.DeepEqual(fired[0], want0) {
		t.Errorf("outcome 0 = %+v", fired[0])
	}
	if fired[1].SetLevel == nil || fired[1].SetLevel.Trait != "base" || fired[1].SetLevel.Level != catalog.LevelLow {
		t.Errorf("outcome 1 = %+v", fired[1])
	}
	// Caller's traits are untouched.
	if !reflect.DeepEqual(ctx.Traits, []string{"base"}) || len(traits) != 1 {
		t.Errorf("ctx.Traits mutated: %v", ctx.Traits)
	}
	// Input slices are not reordered.
	if svc[0].ID != "svc.c" || glob[0].ID != "g.z" {
		t.Error("input rule slices were reordered")
	}
}

func TestApplyTraitsCapacityNotShared(t *testing.T) {
	// A slice with spare capacity must not be written through.
	traits := make([]string, 1, 4)
	traits[0] = "base"
	ctx := &NodeContext{Traits: traits}
	rules := []catalog.Rule{{ID: "r", Then: catalog.Effect{AddTrait: "added"}}}
	Apply(rules, nil, ctx)
	if got := traits[:2][1]; got != "" {
		t.Errorf("backing array written: %q", got)
	}
}

func TestApplyEmpty(t *testing.T) {
	fired, undet := Apply(nil, nil, &NodeContext{})
	if fired != nil || undet != nil {
		t.Errorf("got %v %v", fired, undet)
	}
}

func TestRegexpCache(t *testing.T) {
	a := compileCached("^x+$")
	b := compileCached("^x+$")
	if a == nil || a != b {
		t.Error("regexp not cached")
	}
	if compileCached("(") != nil {
		t.Error("invalid pattern should cache nil")
	}
}
