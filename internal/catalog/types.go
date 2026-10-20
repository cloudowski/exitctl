// Package catalog loads, validates, and indexes the exitctl knowledge catalog
// (data-model.md Part 1, Core Element 1). The catalog is data: no per-service
// behavior lives in code.
package catalog

// Level is a finding severity.
type Level string

// Levels in ascending order.
const (
	LevelNone     Level = "none"
	LevelLow      Level = "low"
	LevelMedium   Level = "medium"
	LevelHigh     Level = "high"
	LevelCritical Level = "critical"
)

// LevelRank returns the ordinal of a level (none=0 .. critical=4), -1 if invalid.
func LevelRank(l Level) int {
	switch l {
	case LevelNone:
		return 0
	case LevelLow:
		return 1
	case LevelMedium:
		return 2
	case LevelHigh:
		return 3
	case LevelCritical:
		return 4
	}
	return -1
}

// Dimension is a kind of lock-in.
type Dimension string

// Dimensions.
const (
	DimTechnical   Dimension = "technical"
	DimData        Dimension = "data"
	DimOperational Dimension = "operational"
	DimEconomic    Dimension = "economic"
	DimSkills      Dimension = "skills"
)

// AllDimensions in display order.
var AllDimensions = []Dimension{DimTechnical, DimData, DimOperational, DimSkills, DimEconomic}

// Status is an entry lifecycle state: draft -> verified -> deprecated.
type Status string

// Statuses.
const (
	StatusDraft      Status = "draft"
	StatusVerified   Status = "verified"
	StatusDeprecated Status = "deprecated"
)

// Pos records where an entry was defined, for error reporting.
type Pos struct {
	File string `yaml:"-" json:"-"`
	Line int    `yaml:"-" json:"-"`
}

// Manifest is catalog.yaml.
type Manifest struct {
	Pos            `yaml:"-"`
	Schema         string   `yaml:"schema"`
	Kind           string   `yaml:"kind"`
	CatalogVersion string   `yaml:"catalog_version"` // YYYY.MM.N
	Title          string   `yaml:"title"`
	Maintainers    []string `yaml:"maintainers"`
	Neutrality     string   `yaml:"neutrality"`
}

// Provider is a cloud or platform.
type Provider struct {
	ID   string `yaml:"id"`
	Name string `yaml:"name"`
	Kind string `yaml:"kind"` // hyperscaler | platform | generic
}

// Category is a node of the dotted category taxonomy.
type Category struct {
	ID   string `yaml:"id"`
	Name string `yaml:"name"`
}

// LLMInfo is required provenance for LLM-generated entries.
type LLMInfo struct {
	Batch         string `yaml:"batch"`
	Model         string `yaml:"model"`
	PromptVersion string `yaml:"prompt_version"`
	EvalPassed    bool   `yaml:"eval_passed"`
}

// Provenance records origin and review state of an entry.
type Provenance struct {
	Origin     string   `yaml:"origin"` // maintainer | community | vendor | llm
	Author     string   `yaml:"author"`
	ReviewedBy string   `yaml:"reviewed_by"`
	ReviewedOn string   `yaml:"reviewed_on"`
	Sources    []string `yaml:"sources"`
	Status     Status   `yaml:"status"`
	LLM        *LLMInfo `yaml:"llm"`
}

// TraitRef attaches a trait, optionally overriding its level. Reason is
// required when Level is set.
type TraitRef struct {
	ID     string `yaml:"id"`
	Level  Level  `yaml:"level"`
	Reason string `yaml:"reason"`
}

// Trait is a reusable lock-in characteristic.
type Trait struct {
	Pos         `yaml:"-"`
	ID          string      `yaml:"id"`
	Name        string      `yaml:"name"`
	Level       Level       `yaml:"level"`
	Dimensions  []Dimension `yaml:"dimensions"`
	Explanation string      `yaml:"explanation"` // <= 200 chars
	ExitHint    string      `yaml:"exit_hint"`   // <= 200 chars
	Rules       []Rule      `yaml:"rules"`       // global rules
	Provenance  Provenance  `yaml:"provenance"`
}

// GroupKind identifies a Kubernetes type. Group "" is the core group, Kind "*"
// is a wildcard.
type GroupKind struct {
	Group string `yaml:"group"`
	Kind  string `yaml:"kind"`
}

// ResourceTypes maps source formats to native type names.
type ResourceTypes struct {
	Terraform         []string    `yaml:"terraform"`
	CloudFormation    []string    `yaml:"cloudformation"`
	Kubernetes        []GroupKind `yaml:"kubernetes"`
	ContainerRegistry []string    `yaml:"container_registry"` // host globs
	CIAction          []string    `yaml:"ci_action"`          // action name globs
}

// SdkPattern maps code to a service for one ecosystem.
type SdkPattern struct {
	Ecosystem string    `yaml:"ecosystem"` // npm | pypi | go | maven | nuget | rubygems | composer | cargo
	Packages  []string  `yaml:"packages"`  // service-specific package globs
	Imports   []string  `yaml:"imports"`   // RE2 line patterns
	Usage     []string  `yaml:"usage"`     // RE2 generic-client patterns
	Gate      []string  `yaml:"gate"`      // packages required to scan Usage
	Trait     *TraitRef `yaml:"trait"`     // default proprietary-sdk
}

// BindingMatch selects Kubernetes objects bound to a cloud service.
type BindingMatch struct {
	Kind       string   `yaml:"kind"`
	Group      string   `yaml:"group"`
	Field      string   `yaml:"field"`      // dotted path in the object, e.g. provisioner, spec.ingressClassName
	Annotation string   `yaml:"annotation"` // annotation key; may end with * as prefix wildcard
	Exists     *bool    `yaml:"exists"`
	Equals     string   `yaml:"equals"`
	In         []string `yaml:"in"`
}

// Binding binds matching Kubernetes objects to Service (default: the service
// that declares the binding).
type Binding struct {
	Match   BindingMatch `yaml:"match"`
	Service string       `yaml:"service"`
}

// EdgeHint classifies reference edges created by a given attribute.
type EdgeHint struct {
	ResourceType string `yaml:"resource_type"`
	Attribute    string `yaml:"attribute"`
	EdgeKind     string `yaml:"edge_kind"` // reference | binding | data-flow | containment
}

// AlternativeMapping links a service to a replacement option.
type AlternativeMapping struct {
	Ref            string     `yaml:"ref"`
	Difficulty     string     `yaml:"difficulty"`    // low | medium | high | very-high
	Compatibility  string     `yaml:"compatibility"` // api-compatible | protocol-compatible | conceptual
	ResidualTraits []TraitRef `yaml:"residual_traits"`
	Notes          string     `yaml:"notes"` // <= 200 chars
	Recommended    bool       `yaml:"recommended"`
	ApprovedBy     string     `yaml:"approved_by"`
}

// DifficultyRank orders difficulties (low=0 .. very-high=3), 99 if unknown.
func DifficultyRank(d string) int {
	switch d {
	case "low":
		return 0
	case "medium":
		return 1
	case "high":
		return 2
	case "very-high":
		return 3
	}
	return 99
}

// Service is a provider offering.
type Service struct {
	Pos                `yaml:"-"`
	ID                 string               `yaml:"id"` // <provider>.<slug>
	Provider           string               `yaml:"provider"`
	Name               string               `yaml:"name"`
	Category           string               `yaml:"category"`
	ResourceTypes      ResourceTypes        `yaml:"resource_types"`
	Traits             []TraitRef           `yaml:"traits"`
	Rules              []Rule               `yaml:"rules"`
	SDK                []SdkPattern         `yaml:"sdk"`
	KubernetesBindings []Binding            `yaml:"kubernetes_bindings"`
	EdgeHints          []EdgeHint           `yaml:"edge_hints"`
	Alternatives       []AlternativeMapping `yaml:"alternatives"`
	Aliases            []string             `yaml:"aliases"`
	Successor          string               `yaml:"successor"`
	Provenance         Provenance           `yaml:"provenance"`
}

// Alternative is a replacement option.
type Alternative struct {
	Pos               `yaml:"-"`
	ID                string     `yaml:"id"`
	Name              string     `yaml:"name"`
	Kind              string     `yaml:"alternative_kind"` // oss-self-hosted | managed-oss | european-provider | other-provider
	License           string     `yaml:"license"`
	Homepage          string     `yaml:"homepage"`
	VendorContributed bool       `yaml:"vendor_contributed"`
	Provenance        Provenance `yaml:"provenance"`
}

// Neutral classifies resource types that are recognized but carry no lock-in.
type Neutral struct {
	Pos           `yaml:"-"`
	ID            string        `yaml:"id"`
	Provider      string        `yaml:"provider"`
	ResourceTypes ResourceTypes `yaml:"resource_types"`
	// Scored false marks types that deploy nothing (lookups, local rendering,
	// metadata files): their resources are left out of the score and of
	// coverage entirely instead of counting as portable. Default true.
	Scored     *bool      `yaml:"scored"`
	Provenance Provenance `yaml:"provenance"`
}

// Counts reports whether resources matched by the entry count in the score.
func (n *Neutral) Counts() bool { return n.Scored == nil || *n.Scored }

// Rule is a declarative condition plus effect (data-model.md 1.7).
type Rule struct {
	ID    string     `yaml:"id"`
	Match Selector   `yaml:"match"`
	When  *Condition `yaml:"when"`
	Then  Effect     `yaml:"then"`
}

// Selector chooses the nodes a rule applies to. All non-empty fields must
// match (each field matches if any listed value matches).
type Selector struct {
	ResourceType []string `yaml:"resource_type"`
	Service      []string `yaml:"service"`
	Category     []string `yaml:"category"` // prefix match on dotted IDs
	Trait        []string `yaml:"trait"`
	Source       []string `yaml:"source"`
}

// EdgeTo is a context condition on the node's edges.
type EdgeTo struct {
	Service   string `yaml:"service"`
	Category  string `yaml:"category"`
	Kind      string `yaml:"kind"`
	Direction string `yaml:"direction"` // in | out (default out)
}

// Condition is a three-valued boolean expression. Exactly one operator group
// is used per condition object: an attribute test (Attr + one of Exists,
// Equals, In, Matches, Gte, Lte), a combinator (All, Any, Not), or a context
// test (EdgeTo, HasSDKUsage).
type Condition struct {
	Attr        string       `yaml:"attr"`
	Exists      *bool        `yaml:"exists"`
	Equals      any          `yaml:"equals"`
	In          []any        `yaml:"in"`
	Matches     string       `yaml:"matches"`
	Gte         *float64     `yaml:"gte"`
	Lte         *float64     `yaml:"lte"`
	All         []*Condition `yaml:"all"`
	Any         []*Condition `yaml:"any"`
	Not         *Condition   `yaml:"not"`
	EdgeTo      *EdgeTo      `yaml:"edge_to"`
	HasSDKUsage bool         `yaml:"has_sdk_usage"`
}

// SetLevel changes the level of a trait already present on the node.
type SetLevel struct {
	Trait string `yaml:"trait"`
	Level Level  `yaml:"level"`
}

// Effect is what a rule does when its condition is true.
type Effect struct {
	AddTrait string    `yaml:"add_trait"`
	Level    Level     `yaml:"level"`
	SetLevel *SetLevel `yaml:"set_level"`
	Evidence string    `yaml:"evidence"` // attribute path whose line becomes evidence
}

// Catalog is a loaded catalog (all statuses). Use NewIndex for lookups.
type Catalog struct {
	Manifest     Manifest
	Providers    []Provider
	Categories   []Category
	Traits       []Trait
	Services     []Service
	Alternatives []Alternative
	Neutrals     []Neutral

	// locs records where providers and categories were declared, keyed
	// "provider:<id>" / "category:<id>" (filled by Load, used by Validate).
	locs map[string][]Pos
}

// Problem is one validation problem.
type Problem struct {
	File    string
	Line    int
	EntryID string
	Message string
}
