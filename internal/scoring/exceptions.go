package scoring

import (
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/cloudowski/exitctl/internal/config"
	"github.com/cloudowski/exitctl/internal/graph"
)

// Exception states (data-model.md 4.6).
const (
	ExceptionActive  = "active"
	ExceptionExpired = "expired"
	ExceptionInvalid = "invalid"
)

// Reasons an exception is invalid. They are derivable from the exception
// itself, so renderers can explain a stored result without extra fields.
const (
	ProblemMissingReason = "missing reason"
	ProblemMissingOwner  = "missing owner"
	ProblemBadID         = "id must match ^[a-z0-9][a-z0-9-]*$"
	ProblemDuplicateID   = "duplicate id"
	ProblemBadSelector   = "select must name exactly one of finding, resource, service, trait (or resource + trait)"
	ProblemBadExpires    = "expires must be a date YYYY-MM-DD"
	ProblemNoMatch       = "selector matches no finding"
)

var exceptionIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

const dateLayout = "2006-01-02"

// ExceptionState is the evaluated state of one configured exception.
type ExceptionState struct {
	ID      string
	State   string // active | expired | invalid
	Problem string // why the exception is invalid; "" otherwise
	Reason  string
	Owner   string
	Expires string
	// Matched lists the IDs of the findings the selector matches, sorted.
	// Empty for invalid exceptions. For expired exceptions the findings are
	// listed but stay active.
	Matched []string
}

// ApplyExceptions evaluates the configured exceptions against the graph and
// marks every finding matched by an active exception as accepted (status
// "accepted", Exception = exception ID). Accepted findings are excluded from
// r(n) by Risk; the node stays in S, so it remains in the denominator.
//
// asOf is the reference date (YYYY-MM-DD) for expiry: an exception expires
// when asOf is after its expires date (the expires day itself is still
// active). When asOf is empty, expiry is not evaluated.
//
// Exceptions are evaluated in ID order; when several active exceptions match
// the same finding, the first by ID is recorded on the finding (all of them
// list it in Matched). The result is sorted by ID and deterministic. Calling
// ApplyExceptions resets previous exception marks first.
func ApplyExceptions(g *graph.Graph, excs []config.Exception, asOf string) []ExceptionState {
	for _, n := range g.Nodes {
		for i := range n.Findings {
			n.Findings[i].Status = graph.FindingActive
			n.Findings[i].Exception = ""
		}
	}
	if len(excs) == 0 {
		return nil
	}
	sorted := append([]config.Exception(nil), excs...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })

	ref, refErr := time.Parse(dateLayout, asOf)
	nodes := sortedNodes(g)
	out := make([]ExceptionState, 0, len(sorted))
	for i, e := range sorted {
		st := ExceptionState{ID: e.ID, Reason: e.Reason, Owner: e.Owner, Expires: e.Expires, Matched: []string{}}
		st.Problem = exceptionProblem(e)
		if st.Problem == "" && i > 0 && sorted[i-1].ID == e.ID {
			st.Problem = ProblemDuplicateID
		}
		if st.Problem != "" {
			st.State = ExceptionInvalid
			out = append(out, st)
			continue
		}
		st.Matched = matchFindings(nodes, e.Select)
		if len(st.Matched) == 0 {
			st.State = ExceptionInvalid
			st.Problem = ProblemNoMatch
			out = append(out, st)
			continue
		}
		st.State = ExceptionActive
		if e.Expires != "" && asOf != "" && refErr == nil {
			exp, _ := time.Parse(dateLayout, e.Expires) // validated by exceptionProblem
			if ref.After(exp) {
				st.State = ExceptionExpired
			}
		}
		out = append(out, st)
	}

	// Mark accepted findings: first active exception (by ID) wins.
	accepted := map[string]string{}
	for _, st := range out {
		if st.State != ExceptionActive {
			continue
		}
		for _, f := range st.Matched {
			if _, ok := accepted[f]; !ok {
				accepted[f] = st.ID
			}
		}
	}
	for _, n := range g.Nodes {
		for i := range n.Findings {
			if id, ok := accepted[n.Findings[i].ID]; ok {
				n.Findings[i].Status = graph.FindingAccepted
				n.Findings[i].Exception = id
			}
		}
	}
	return out
}

// exceptionProblem returns the reason an exception is invalid by itself
// (without looking at the graph), or "".
func exceptionProblem(e config.Exception) string {
	switch {
	case !exceptionIDPattern.MatchString(e.ID):
		return ProblemBadID
	case !validSelector(e.Select):
		return ProblemBadSelector
	case strings.TrimSpace(e.Reason) == "":
		return ProblemMissingReason
	case strings.TrimSpace(e.Owner) == "":
		return ProblemMissingOwner
	}
	if e.Expires != "" {
		if _, err := time.Parse(dateLayout, e.Expires); err != nil {
			return ProblemBadExpires
		}
	}
	return ""
}

// validSelector: exactly one of finding, resource, service, trait, or the
// combination resource + trait.
func validSelector(s config.Selector) bool {
	n := 0
	for _, v := range []string{s.Finding, s.Resource, s.Service, s.Trait} {
		if v != "" {
			n++
		}
	}
	if n == 1 {
		return true
	}
	return n == 2 && s.Resource != "" && s.Trait != ""
}

// matchFindings returns the sorted IDs of the findings the selector matches.
// A resource matches a node by canonical ID, alias, or native address (all
// nodes with that address match, e.g. the same address in several repos).
func matchFindings(nodes []*graph.Node, s config.Selector) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, n := range nodes {
		if s.Resource != "" && !nodeMatches(n, s.Resource) {
			continue
		}
		if s.Service != "" && n.Service != s.Service {
			continue
		}
		for _, f := range n.Findings {
			if s.Finding != "" && f.ID != s.Finding {
				continue
			}
			if s.Trait != "" && f.Trait != s.Trait {
				continue
			}
			if !seen[f.ID] {
				seen[f.ID] = true
				out = append(out, f.ID)
			}
		}
	}
	sort.Strings(out)
	return out
}

func nodeMatches(n *graph.Node, ref string) bool {
	if n.ID == ref || (n.NativeAddress != "" && n.NativeAddress == ref) {
		return true
	}
	for _, a := range n.Aliases {
		if a == ref {
			return true
		}
	}
	return false
}

// allActive returns a node's findings with every status reset to active, so
// IndexWith computes the index without exceptions.
func allActive(n *graph.Node) []graph.Finding {
	accepted := false
	for _, f := range n.Findings {
		if f.Status != graph.FindingActive {
			accepted = true
			break
		}
	}
	if !accepted {
		return n.Findings
	}
	out := make([]graph.Finding, len(n.Findings))
	copy(out, n.Findings)
	for i := range out {
		out[i].Status = graph.FindingActive
		out[i].Exception = ""
	}
	return out
}

// IndexWithoutExceptions returns the index computed as if no exception were
// active (every finding counts). ok is false when S is empty.
func IndexWithoutExceptions(g *graph.Graph, dependents func(id string) int, cfg config.Scoring) (int, bool) {
	return IndexWith(g, dependents, cfg, allActive)
}

// LatestDate returns the latest of the given YYYY-MM-DD dates ("" when none
// is set). Dates in that layout compare correctly as strings.
func LatestDate(dates []string) string {
	latest := ""
	for _, d := range dates {
		if d > latest {
			latest = d
		}
	}
	return latest
}
