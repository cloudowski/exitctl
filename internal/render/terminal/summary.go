package terminal

import (
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/cloudowski/exitctl/internal/model"
	"github.com/cloudowski/exitctl/internal/result"
	"github.com/cloudowski/exitctl/internal/version"
)

// MaxSummaryLines is the hard cap on summary length (contracts/cli.md).
const MaxSummaryLines = 24

// DefaultAnchors is the number of anchors shown without --all.
const DefaultAnchors = 5

// maxAddressWidth caps the address column of the anchors table.
const maxAddressWidth = 40

// maxAlternatives is the number of alternatives listed per anchor.
const maxAlternatives = 2

// Status and confidence values of result.Analysis (scoring package).
const (
	statusNothingScored   = "nothing_scored"
	confidenceProvisional = "provisional"
	confidenceWithheld    = "withheld"
	exceptionActive       = "active"
	exceptionExpired      = "expired"
	exceptionInvalid      = "invalid"
)

// dimensionOrder is the display order of dimensions; the first four share the
// bars line, economic gets its own line.
var dimensionOrder = []string{"technical", "data", "operational", "skills", "economic"}

// Summary writes the scan summary of doc to w (contracts/cli.md
// "exitctl scan"). The output is at most MaxSummaryLines lines unless
// opts.All lists every anchor, and is deterministic for a given document and
// options.
func Summary(w io.Writer, doc *result.Document, opts Options) error {
	lines := summaryLines(doc, opts)
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(strings.TrimRight(l, " "))
		b.WriteByte('\n')
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func summaryLines(doc *result.Document, o Options) []string {
	a := &doc.Analysis
	head := []string{o.bold("Offline, read-only scan:") + " " + scanCounts(doc)}
	tail := []string{}

	if a.Status == statusNothingScored {
		head = append(head, "  Nothing to score: no recognized cloud resources or SDK usage found.")
		tail = append(tail, coverageLine(a.Coverage, o), "  "+o.bold("Unrecognized types:")+" exitctl coverage", footer(doc, o))
		return append(head, tail...)
	}

	head = append(head, indexLine(a, o))
	head = append(head, dimensionLines(a, o)...)
	head = append(head, "")

	if l := leverLine(doc, o); l != "" {
		tail = append(tail, l)
	}
	tail = append(tail, coverageLine(a.Coverage, o)+unmappedSDKHint(doc))
	if l := exceptionsLine(a); l != "" {
		tail = append(tail, l)
	}
	if len(a.Anchors) > 0 {
		rank := strconv.Itoa(a.Anchors[0].Rank)
		tail = append(tail, "  "+o.bold("Explain #"+rank+":")+" exitctl explain "+rank+"  |  "+o.bold("Report:")+" exitctl report --format html --open")
	} else {
		tail = append(tail, "  "+o.bold("Unrecognized types:")+" exitctl coverage")
	}
	tail = append(tail, footer(doc, o))

	if len(a.Anchors) == 0 {
		mid := []string{"  Top anchors: none (no scored resource has lock-in findings)", ""}
		return concat(head, mid, tail)
	}

	blocks, header := anchorBlocks(doc, a.Anchors, o)
	if o.All {
		// --all lists every anchor; the line cap applies only to the default view.
		mid := []string{header}
		for _, bl := range blocks {
			mid = append(mid, bl...)
		}
		return concat(head, append(mid, ""), tail)
	}
	total := len(blocks)
	limit := len(blocks)
	if limit > DefaultAnchors {
		limit = DefaultAnchors
	}
	// fixed: head + table header + blank line after the table + tail.
	fixed := len(head) + 1 + 1 + len(tail)
	k := limit
	for k > 0 {
		n := fixed
		for _, bl := range blocks[:k] {
			n += len(bl)
		}
		if k < len(blocks) {
			n++ // "... N more anchors" line
		}
		if n <= MaxSummaryLines {
			break
		}
		k--
	}
	// Re-render only the shown anchors so column widths fit them.
	blocks, header = anchorBlocks(doc, a.Anchors[:k], o)
	mid := []string{header}
	for _, bl := range blocks {
		mid = append(mid, bl...)
	}
	if hidden := total - k; hidden > 0 {
		mid = append(mid, fmt.Sprintf("  ... %d more %s: exitctl scan --all", hidden, plural(hidden, "anchor", "anchors")))
	}
	mid = append(mid, "")
	out := concat(head, mid, tail)
	if len(out) > MaxSummaryLines {
		// Only reachable when even the fixed part exceeds the cap; keep the
		// first lines and the footer.
		out = append(out[:MaxSummaryLines-1], out[len(out)-1])
	}
	return out
}

func concat(parts ...[]string) []string {
	var out []string
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// scanCounts summarizes what was scanned, by kind, from the raw facts
// (de-duplicated by node ID). Zero counts are omitted.
func scanCounts(doc *result.Document) string {
	var tf, cfn, cdk, pulumi, k8s, images, ci, comps int
	seen := map[string]bool{}
	for _, n := range doc.Facts.RawNodes {
		if seen[n.ID] {
			continue
		}
		seen[n.ID] = true
		switch {
		case n.NodeKind == model.NodeComponent:
			comps++
		case n.Type == model.TypeContainerImage:
			images++
		case n.Type == model.TypeCIAction:
			ci++
		case n.SourceKind == model.KindTerraform:
			tf++
		case n.SourceKind == model.KindCloudFormation:
			cfn++
		case n.SourceKind == model.KindCDK:
			cdk++
		case n.SourceKind == model.KindPulumi:
			pulumi++
		case n.SourceKind == model.KindKubernetes || n.SourceKind == model.KindHelm || n.SourceKind == model.KindKustomize:
			k8s++
		}
	}
	var parts []string
	add := func(n int, one, many string) {
		if n > 0 {
			parts = append(parts, strconv.Itoa(n)+" "+plural(n, one, many))
		}
	}
	add(tf, "Terraform resource", "Terraform resources")
	add(cfn, "CloudFormation resource", "CloudFormation resources")
	add(cdk, "CDK resource", "CDK resources")
	add(pulumi, "Pulumi resource", "Pulumi resources")
	add(k8s, "K8s object", "K8s objects")
	add(images, "image", "images")
	add(ci, "CI action", "CI actions")
	add(comps, "component", "components")
	if len(parts) == 0 {
		return "no supported inputs found"
	}
	return strings.Join(parts, ", ")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// pct renders a 0..1 fraction as a whole percentage, rounded down so that
// 100% is shown only for complete coverage.
func pct(f float64) string {
	p := int(math.Floor(f*100 + 1e-9))
	if p < 0 {
		p = 0
	}
	if p > 100 {
		p = 100
	}
	return strconv.Itoa(p) + "%"
}

func indexLine(a *result.Analysis, o Options) string {
	idx := 0
	if a.Index != nil {
		idx = *a.Index
	}
	if a.Confidence == confidenceWithheld {
		lo, hi := idx, idx
		if a.Range != nil {
			lo, hi = a.Range.Lower, a.Range.Upper
		}
		return fmt.Sprintf("  %s range %d-%d - insufficient coverage for a grade (%s)",
			o.bold("Lock-in index"), lo, hi, pct(a.Coverage.Weighted))
	}
	l := fmt.Sprintf("  %s  %s/100", o.bold("Lock-in index"), o.bold(strconv.Itoa(idx)))
	if a.Grade != "" {
		l += "  grade " + o.grade(a.Grade)
	}
	if a.Confidence == confidenceProvisional {
		l += " (provisional, coverage " + pct(a.Coverage.Weighted) + ")"
	}
	if a.Range != nil {
		l += fmt.Sprintf("  range %d-%d", a.Range.Lower, a.Range.Upper)
	}
	return l
}

func dimensionLines(a *result.Analysis, o Options) []string {
	var bars, rest, unassessed []string
	for _, d := range dimensionOrder {
		v := a.Dimensions[d]
		if v == nil {
			unassessed = append(unassessed, d)
			continue
		}
		s := d + " " + o.bar(*v)
		if d == "economic" {
			rest = append(rest, s)
		} else {
			bars = append(bars, s)
		}
	}
	if len(unassessed) > 0 {
		rest = append(rest, strings.Join(unassessed, ", ")+": not assessed")
	}
	var out []string
	if len(bars) > 0 {
		out = append(out, "  "+strings.Join(bars, "  "))
	}
	if len(rest) > 0 {
		out = append(out, "  "+strings.Join(rest, "  "))
	}
	return out
}

// name returns the display name for an ID from opts.Names, or the ID.
func (o Options) name(id string) string {
	if n, ok := o.Names[id]; ok && n != "" {
		return n
	}
	return id
}

// anchorBlocks renders every anchor as a row plus an optional code bridge
// line, and the table header aligned with the rows.
func anchorBlocks(doc *result.Document, anchors []result.Anchor, o Options) ([][]string, string) {
	addrCap := maxAddressWidth
	if o.Width > 0 && o.Width < 80 {
		addrCap = max(16, maxAddressWidth-(80-o.Width))
	}
	var locs map[string]string
	if o.Wide {
		locs = AnchorLocations(doc)
	}
	type row struct{ label, addr, deps, alts, bridge string }
	rows := make([]row, len(anchors))
	labelW, addrW := 0, 0
	for i, an := range anchors {
		n := doc.NodeByID(an.Node)
		addr := an.Node
		service := ""
		if n != nil {
			addr = Address(n)
			if n.Kind == "implied_service" {
				addr = o.name(n.Service) + " (code only)"
			}
			service = n.Service
		}
		if o.Wide {
			if loc := locs[an.Node]; loc != "" {
				addr = loc + ": " + addr
			}
		} else {
			addr = truncate(addr, addrCap)
		}
		r := row{label: strconv.Itoa(an.Rank) + ".", addr: addr, deps: strconv.Itoa(an.DependentsCount)}
		r.alts = alternatives(an.Alternatives, o)
		if an.SDKFiles > 0 {
			svc := service
			if svc == "" {
				svc = an.Node
			}
			files := strconv.Itoa(an.SDKFiles) + " " + plural(an.SDKFiles, "file", "files")
			if an.ComponentsUsing > 0 {
				files += " in " + strconv.Itoa(an.ComponentsUsing) + " " + plural(an.ComponentsUsing, "component", "components")
			}
			verb := "use"
			if an.SDKFiles == 1 {
				verb = "uses"
			}
			r.bridge = "     + " + files + " " + verb + " " + o.name(svc) + " SDK"
		}
		labelW = max(labelW, width(r.label))
		addrW = max(addrW, width(r.addr))
		rows[i] = r
	}
	const depsTitle, altsTitle = "dependents", "alternatives"
	firstW := max(labelW+1+addrW, width("Top anchors"))
	header := "  " + o.bold(padRight("Top anchors", firstW)) + "  " + o.dim(depsTitle) + "  " + o.dim(altsTitle)
	blocks := make([][]string, len(rows))
	for i, r := range rows {
		first := padRight(padRight(r.label, labelW)+" "+r.addr, firstW)
		l := "  " + first + "  " + padLeft(r.deps, width(depsTitle)) + "  " + r.alts
		blocks[i] = []string{l}
		if r.bridge != "" {
			blocks[i] = append(blocks[i], r.bridge)
		}
	}
	return blocks, header
}

// alternatives renders up to maxAlternatives alternatives (all of them with
// Wide) in their stored order, marking recommended ones, with "+N" for the
// rest.
func alternatives(alts []result.AlternativeView, o Options) string {
	if len(alts) == 0 {
		return "none in catalog"
	}
	limit := maxAlternatives
	if o.Wide {
		limit = len(alts)
	}
	var parts []string
	for i, a := range alts {
		if i == limit {
			break
		}
		name := a.Name
		if name == "" {
			name = o.name(a.ID)
		}
		if a.Recommended {
			name = o.recommended() + " " + name
		}
		parts = append(parts, name)
	}
	s := strings.Join(parts, ", ")
	if extra := len(alts) - limit; extra > 0 {
		s += " +" + strconv.Itoa(extra)
	}
	return s
}

func leverLine(doc *result.Document, o Options) string {
	lv := doc.Analysis.Lever
	if lv == nil {
		return ""
	}
	alt := o.name(lv.Alternative)
	if alt == lv.Alternative {
		// Fall back to the alternative name carried by the anchors.
	search:
		for _, an := range doc.Analysis.Anchors {
			for _, av := range an.Alternatives {
				if av.ID == lv.Alternative && av.Name != "" {
					alt = av.Name
					break search
				}
			}
		}
	}
	return fmt.Sprintf("  %s%s %s -> %s lowers the index by %d %s",
		o.leverMarker(), o.bold("Biggest lever:"), o.name(lv.Service), alt, lv.Delta, plural(lv.Delta, "pt", "pts"))
}

// unmappedSDKHint tells how many cloud SDK packages and clients the catalog
// does not map, so a clean type coverage is not mistaken for full coverage.
func unmappedSDKHint(doc *result.Document) string {
	n := 0
	for _, g := range doc.Facts.Gaps {
		if g.Kind == model.GapUnknownSDK {
			n++
		}
	}
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("; %d unmapped cloud %s: exitctl coverage", n, plural(n, "SDK", "SDKs"))
}

func coverageLine(c result.Coverage, o Options) string {
	band := min(c.Types, c.Resources)
	l := "  " + o.coverageMarker(band) + o.bold("Coverage:") + " " +
		o.paint(coverageColor(c.Types), false, pct(c.Types)) + " of types, " +
		o.paint(coverageColor(c.Resources), false, pct(c.Resources)) + " of resources"
	if c.UnknownTypes > 0 {
		l += fmt.Sprintf(" (%d unknown %s: exitctl coverage)", c.UnknownTypes, plural(c.UnknownTypes, "type", "types"))
	}
	return l
}

func exceptionsLine(a *result.Analysis) string {
	n := 0
	for _, e := range a.Exceptions {
		if e.State == exceptionActive {
			n++
		}
	}
	if n == 0 {
		return ""
	}
	l := fmt.Sprintf("  Exceptions: %d accepted", n)
	if a.IndexWithoutExceptions != nil {
		l += fmt.Sprintf(" (index without exceptions: %d)", *a.IndexWithoutExceptions)
	}
	return l
}

// ExceptionNotices returns one notice per expired or invalid exception, in
// exception ID order, for stderr (contracts/cli.md: they are not part of the
// summary). The invalid reason is derived from the exception itself: missing
// reason, missing owner, else a selector that matches nothing (other problems
// are rejected when the configuration is loaded).
func ExceptionNotices(doc *result.Document) []string {
	asOf := ""
	for _, in := range doc.Inputs {
		if in.AsOf > asOf {
			asOf = in.AsOf
		}
	}
	var out []string
	for _, e := range doc.Analysis.Exceptions {
		switch e.State {
		case exceptionExpired:
			l := fmt.Sprintf("notice: exception %s expired on %s", e.ID, e.Expires)
			if asOf != "" {
				l += " (as of " + asOf + ")"
			}
			n := len(e.MatchedFindings)
			l += fmt.Sprintf("; its %d %s count again", n, plural(n, "finding", "findings"))
			out = append(out, l)
		case exceptionInvalid:
			why := "its selector matches no finding"
			switch {
			case strings.TrimSpace(e.Reason) == "":
				why = "it has no reason"
			case strings.TrimSpace(e.Owner) == "":
				why = "it has no owner"
			}
			out = append(out, fmt.Sprintf("notice: exception %s is invalid and ignored: %s", e.ID, why))
		}
	}
	return out
}

func footer(doc *result.Document, o Options) string {
	v := doc.Versions
	tool := v.Tool
	if tool == "" {
		tool = version.Tool
	}
	rules := v.Rules
	if rules == "" {
		rules = version.RulesVersion
	}
	fp := v.ConfigFingerprint
	if len(fp) > 8 {
		fp = fp[:8]
	}
	s := " " + o.sep() + " "
	l := "  exitctl " + tool + s + "catalog " + v.Catalog + s + "rules " + rules + s + "config " + fp
	if v.CustomScoring {
		l += " (custom scoring)"
	}
	if v.CatalogDraftsIncluded {
		l += " (draft catalog)"
	}
	return o.dim(l)
}

// AnchorLocations returns, for anchors whose display address is shared with
// another anchor, the part of their source location that tells them apart:
// the location path (node ID before "#") without the leading and trailing
// segments the group has in common. For example, two
// aws_kms_key.cmk_dynamodb anchors under
// manifests/modules/security/{irsa,eks-pod-identity}/.workshop/terraform get
// "irsa" and "eks-pod-identity". Anchors with a unique address are absent.
func AnchorLocations(doc *result.Document) map[string]string {
	groups := map[string][]string{}
	var order []string
	for _, an := range doc.Analysis.Anchors {
		addr := an.Node
		if n := doc.NodeByID(an.Node); n != nil {
			addr = Address(n)
		}
		if _, ok := groups[addr]; !ok {
			order = append(order, addr)
		}
		groups[addr] = append(groups[addr], an.Node)
	}
	out := map[string]string{}
	for _, addr := range order {
		ids := groups[addr]
		if len(ids) < 2 {
			continue
		}
		segs := make([][]string, len(ids))
		for i, id := range ids {
			segs[i] = strings.Split(strings.ReplaceAll(location(id), "//", "/"), "/")
		}
		pre, suf := distinctMiddle(segs)
		for i, id := range ids {
			mid := segs[i][pre : len(segs[i])-suf]
			if loc := strings.Join(mid, "/"); loc != "" {
				out[id] = loc
			} else {
				out[id] = "."
			}
		}
	}
	return out
}

// location is the source location part of a node ID: everything before the
// address separator "#".
func location(id string) string {
	loc, _, _ := strings.Cut(id, "#")
	return strings.Trim(loc, "/")
}

// distinctMiddle returns how many leading (pre) and trailing (suf) segments
// all paths share, reduced until the remaining middles are distinct.
func distinctMiddle(segs [][]string) (pre, suf int) {
	minLen := len(segs[0])
	for _, s := range segs[1:] {
		minLen = min(minLen, len(s))
	}
	pre = commonCount(segs, minLen, func(s []string, k int) string { return s[k] })
	suf = commonCount(segs, minLen-pre, func(s []string, k int) string { return s[len(s)-1-k] })
	for (pre > 0 || suf > 0) && !distinct(segs, pre, suf) {
		if suf > 0 {
			suf--
		} else {
			pre--
		}
	}
	return pre, suf
}

// commonCount counts the positions k < limit where at(s, k) is the same for
// every path.
func commonCount(segs [][]string, limit int, at func(s []string, k int) string) int {
	n := 0
	for ; n < limit; n++ {
		ref := at(segs[0], n)
		for _, s := range segs[1:] {
			if at(s, n) != ref {
				return n
			}
		}
	}
	return n
}

// distinct reports whether the paths stay pairwise different after dropping
// pre leading and suf trailing segments.
func distinct(segs [][]string, pre, suf int) bool {
	seen := map[string]bool{}
	for _, s := range segs {
		k := strings.Join(s[pre:len(s)-suf], "/")
		if seen[k] {
			return false
		}
		seen[k] = true
	}
	return true
}
