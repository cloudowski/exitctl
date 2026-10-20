package catalog

// This file defines the Index API used by the graph builder and detectors.
// Bodies are implemented in index_impl.go (task T020).

// IndexOptions controls which entries take part in classification.
type IndexOptions struct {
	// IncludeDrafts makes draft entries classify resources as if verified.
	// Development only (hidden --include-drafts flag); results are then
	// stamped as using an unverified catalog.
	IncludeDrafts bool
}

// TypeRef identifies a native resource type within a source format.
// SourceFormat is one of: terraform, cloudformation, kubernetes,
// container_registry, ci_action. For kubernetes, Type is "<group>/<Kind>"
// with an empty group for core objects (e.g. "/ConfigMap", "apps/Deployment").
// For container_registry, Type is the registry host. For ci_action, Type is
// the action name without @ref.
type TypeRef struct {
	SourceFormat string
	Type         string
}

// Classification is the outcome of looking up a type.
type Classification struct {
	Class   string // "service" | "neutral" | "unknown" | "unverified"
	Service *Service
	Neutral *Neutral
}

// Index provides deterministic lookups over a validated catalog.
type Index struct {
	cat  *Catalog
	opts IndexOptions
	impl *indexImpl
}

// Catalog returns the underlying catalog.
func (ix *Index) Catalog() *Catalog { return ix.cat }

// Options returns the options the index was built with.
func (ix *Index) Options() IndexOptions { return ix.opts }
