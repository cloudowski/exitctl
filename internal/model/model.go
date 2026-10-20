// Package model is the narrow waist of exitctl: every source detector emits
// these facts and every later stage (catalog, graph, scoring) consumes them.
// Facts never depend on the catalog (data-model.md Part 2).
package model

import (
	"sort"
	"strings"
)

// SourceKind identifies the kind of input a fact was parsed from.
type SourceKind string

// Source kinds (data-model.md 2.1).
const (
	KindTerraform       SourceKind = "terraform"
	KindCloudFormation  SourceKind = "cloudformation"
	KindCDK             SourceKind = "cdk"    // AWS CDK constructs found in code
	KindPulumi          SourceKind = "pulumi" // Pulumi resources found in code or Pulumi YAML
	KindKubernetes      SourceKind = "kubernetes"
	KindHelm            SourceKind = "helm"
	KindKustomize       SourceKind = "kustomize"
	KindDockerfile      SourceKind = "dockerfile"
	KindGitHubActions   SourceKind = "github-actions"
	KindGitLabCI        SourceKind = "gitlab-ci"
	KindPackageManifest SourceKind = "package-manifest"
	KindSBOM            SourceKind = "sbom"
	KindCode            SourceKind = "code"
)

// SourceStatus is the parse outcome of one source.
type SourceStatus string

// Source statuses.
const (
	StatusParsed     SourceStatus = "parsed"
	StatusPartial    SourceStatus = "partial"
	StatusSkipped    SourceStatus = "skipped"
	StatusFailed     SourceStatus = "failed"
	StatusUnresolved SourceStatus = "unresolved"
)

// Source is one parsed input file or chart. Note is a neutral, user-safe
// description; it never contains raw error text.
type Source struct {
	Repo   string       `json:"repo"`
	Path   string       `json:"path"`
	Kind   SourceKind   `json:"kind"`
	Status SourceStatus `json:"status"`
	Note   string       `json:"note,omitempty"`
}

// NodeKind distinguishes declared resources from application components.
type NodeKind string

// Raw node kinds.
const (
	NodeResource  NodeKind = "resource"
	NodeComponent NodeKind = "component"
)

// Multiplicity tells whether a declaration creates one object or an unknown
// number (count / for_each).
type Multiplicity string

// Multiplicities.
const (
	MultOne     Multiplicity = "one"
	MultUnknown Multiplicity = "unknown"
)

// Well-known node types that are not provider resource types.
const (
	TypeComponent      = "component"
	TypeContainerImage = "container_image"
	TypeCIAction       = "ci_action"
)

// Unknown marks an attribute whose value is not a literal (e.g. an
// expression). Redacted marks a value hidden because it looks like a secret.
// Use IsUnknown / IsRedacted to test values.
var (
	Unknown  = map[string]any{"$unknown": true}
	Redacted = map[string]any{"$redacted": true}
)

// IsUnknown reports whether v is the Unknown sentinel.
func IsUnknown(v any) bool { return isSentinel(v, "$unknown") }

// IsRedacted reports whether v is the Redacted sentinel.
func IsRedacted(v any) bool { return isSentinel(v, "$redacted") }

func isSentinel(v any, key string) bool {
	m, ok := v.(map[string]any)
	if !ok || len(m) != 1 {
		return false
	}
	b, ok := m[key].(bool)
	return ok && b
}

// RawNode is a declared resource or an application component.
type RawNode struct {
	ID               string         `json:"id"`
	NodeKind         NodeKind       `json:"node_kind"`
	SourceKind       SourceKind     `json:"source_kind"`
	Type             string         `json:"type"`
	NativeAddress    string         `json:"native_address"`
	ProviderIdentity string         `json:"provider_identity,omitempty"`
	Attributes       map[string]any `json:"attributes"`
	Multiplicity     Multiplicity   `json:"multiplicity"`
	Evidence         []Evidence     `json:"evidence"`
}

// RawEdge says From depends on To. To may be an address that does not resolve
// to a node; the graph builder records that as a gap.
type RawEdge struct {
	From      string     `json:"from"`
	To        string     `json:"to"`
	Attribute string     `json:"attribute,omitempty"`
	KindHint  string     `json:"kind_hint,omitempty"` // "ordering" or empty
	Evidence  []Evidence `json:"evidence"`
}

// Ecosystem is a package ecosystem.
type Ecosystem string

// Ecosystems.
const (
	EcoNPM      Ecosystem = "npm"
	EcoPyPI     Ecosystem = "pypi"
	EcoGo       Ecosystem = "go"
	EcoMaven    Ecosystem = "maven" // also Scala (sbt) and Kotlin
	EcoNuGet    Ecosystem = "nuget"
	EcoRubyGems Ecosystem = "rubygems"
	EcoComposer Ecosystem = "composer"
	EcoCargo    Ecosystem = "cargo"
)

// PackageUse is a dependency declared by a component.
type PackageUse struct {
	Component string    `json:"component"`
	Ecosystem Ecosystem `json:"ecosystem"`
	Name      string    `json:"name"`
	Version   string    `json:"version,omitempty"`
	Manifest  Evidence  `json:"manifest"`
}

// CodeKindSDKName is the PatternKind of a CodeUse that records only the
// cloud service name of a generic client call (boto3.client("x")); it has no
// Service. The graph builder reports it as an unknown_sdk gap unless a
// catalog pattern matched the same line.
const CodeKindSDKName = "sdk-name"

// CodeUse is a source-level proof that a component uses a provider service.
type CodeUse struct {
	Component   string    `json:"component"`
	Ecosystem   Ecosystem `json:"ecosystem"`
	PatternKind string    `json:"pattern_kind"` // "import" or "usage"
	Service     string    `json:"service"`
	Match       string    `json:"match,omitempty"`
	Evidence    Evidence  `json:"evidence"`
}

// GapKind classifies what could not be known.
type GapKind string

// Gap kinds.
const (
	GapUnknownType         GapKind = "unknown_type"
	GapUnknownSDK          GapKind = "unknown_sdk"
	GapUnresolvedModule    GapKind = "unresolved_module"
	GapUnresolvedChart     GapKind = "unresolved_chart"
	GapUnresolvedReference GapKind = "unresolved_reference"
	GapParseFailure        GapKind = "parse_failure"
	GapRemoteKustomize     GapKind = "remote_kustomize"
)

// Gap is a coverage gap: something that was found but not understood.
type Gap struct {
	Kind    GapKind `json:"kind"`
	Subject string  `json:"subject"`
	Repo    string  `json:"repo,omitempty"`
	Path    string  `json:"path,omitempty"`
	Count   int     `json:"count"`
	// Provider and Service are set on unknown_sdk gaps: the cloud and the
	// service name derived from the SDK package or client name.
	Provider string `json:"provider,omitempty"`
	Service  string `json:"service,omitempty"`
}

// MaxExcerpt is the maximum excerpt length in characters.
const MaxExcerpt = 120

// Evidence points to a file and line that proves a fact.
type Evidence struct {
	Repo       string     `json:"repo,omitempty"`
	Path       string     `json:"path"`
	Line       int        `json:"line"`
	Excerpt    string     `json:"excerpt"`
	SourceKind SourceKind `json:"source_kind"`
}

// NewEvidence builds Evidence: the excerpt is the trimmed source line, max 120
// characters, and the line is at least 1. Callers must redact the line first.
func NewEvidence(repo, path string, line int, excerpt string, kind SourceKind) Evidence {
	if line < 1 {
		line = 1
	}
	return Evidence{Repo: repo, Path: path, Line: line, Excerpt: TrimExcerpt(excerpt), SourceKind: kind}
}

// TrimExcerpt trims whitespace and truncates to MaxExcerpt runes.
func TrimExcerpt(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	r := []rune(s)
	if len(r) > MaxExcerpt {
		s = string(r[:MaxExcerpt])
	}
	return s
}

// Less orders evidence by repo, path, line, excerpt.
func (e Evidence) Less(o Evidence) bool {
	if e.Repo != o.Repo {
		return e.Repo < o.Repo
	}
	if e.Path != o.Path {
		return e.Path < o.Path
	}
	if e.Line != o.Line {
		return e.Line < o.Line
	}
	return e.Excerpt < o.Excerpt
}

// SortEvidence sorts and de-duplicates evidence in place and returns it.
func SortEvidence(ev []Evidence) []Evidence {
	sort.SliceStable(ev, func(i, j int) bool { return ev[i].Less(ev[j]) })
	out := ev[:0]
	for i, e := range ev {
		if i > 0 && e == ev[i-1] {
			continue
		}
		out = append(out, e)
	}
	return out
}

// Facts aggregates everything detectors found.
type Facts struct {
	Sources     []Source     `json:"sources"`
	RawNodes    []RawNode    `json:"raw_nodes"`
	RawEdges    []RawEdge    `json:"raw_edges"`
	PackageUses []PackageUse `json:"package_uses"`
	CodeUses    []CodeUse    `json:"code_uses"`
	Gaps        []Gap        `json:"gaps"`
}

// Append adds all facts of o to f.
func (f *Facts) Append(o Facts) {
	f.Sources = append(f.Sources, o.Sources...)
	f.RawNodes = append(f.RawNodes, o.RawNodes...)
	f.RawEdges = append(f.RawEdges, o.RawEdges...)
	f.PackageUses = append(f.PackageUses, o.PackageUses...)
	f.CodeUses = append(f.CodeUses, o.CodeUses...)
	f.Gaps = append(f.Gaps, o.Gaps...)
}

// Sort orders every slice by its documented key so output is deterministic,
// and normalizes nil slices to empty ones.
func (f *Facts) Sort() {
	sort.SliceStable(f.Sources, func(i, j int) bool {
		a, b := f.Sources[i], f.Sources[j]
		if a.Repo != b.Repo {
			return a.Repo < b.Repo
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Kind < b.Kind
	})
	for i := range f.RawNodes {
		f.RawNodes[i].Evidence = SortEvidence(f.RawNodes[i].Evidence)
	}
	sort.SliceStable(f.RawNodes, func(i, j int) bool { return f.RawNodes[i].ID < f.RawNodes[j].ID })
	for i := range f.RawEdges {
		f.RawEdges[i].Evidence = SortEvidence(f.RawEdges[i].Evidence)
	}
	sort.SliceStable(f.RawEdges, func(i, j int) bool {
		a, b := f.RawEdges[i], f.RawEdges[j]
		if a.From != b.From {
			return a.From < b.From
		}
		if a.To != b.To {
			return a.To < b.To
		}
		if a.Attribute != b.Attribute {
			return a.Attribute < b.Attribute
		}
		return a.KindHint < b.KindHint
	})
	sort.SliceStable(f.PackageUses, func(i, j int) bool {
		a, b := f.PackageUses[i], f.PackageUses[j]
		if a.Component != b.Component {
			return a.Component < b.Component
		}
		if a.Ecosystem != b.Ecosystem {
			return a.Ecosystem < b.Ecosystem
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Manifest.Less(b.Manifest)
	})
	sort.SliceStable(f.CodeUses, func(i, j int) bool {
		a, b := f.CodeUses[i], f.CodeUses[j]
		if a.Component != b.Component {
			return a.Component < b.Component
		}
		if a.Service != b.Service {
			return a.Service < b.Service
		}
		if a.Evidence != b.Evidence {
			return a.Evidence.Less(b.Evidence)
		}
		return a.PatternKind < b.PatternKind
	})
	sort.SliceStable(f.Gaps, func(i, j int) bool {
		a, b := f.Gaps[i], f.Gaps[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Subject != b.Subject {
			return a.Subject < b.Subject
		}
		if a.Repo != b.Repo {
			return a.Repo < b.Repo
		}
		return a.Path < b.Path
	})
	if f.Sources == nil {
		f.Sources = []Source{}
	}
	if f.RawNodes == nil {
		f.RawNodes = []RawNode{}
	}
	if f.RawEdges == nil {
		f.RawEdges = []RawEdge{}
	}
	if f.PackageUses == nil {
		f.PackageUses = []PackageUse{}
	}
	if f.CodeUses == nil {
		f.CodeUses = []CodeUse{}
	}
	if f.Gaps == nil {
		f.Gaps = []Gap{}
	}
}
