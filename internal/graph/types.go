// Package graph builds the single dependency graph of a result and computes
// graph-derived facts (data-model.md Part 3, Core Element 3).
//
// Edge semantics: an Edge always means "From depends on To" (FR-051).
// Dependents of N are all nodes with a directed path to N.
package graph

import (
	"github.com/cloudowski/exitctl/internal/catalog"
	"github.com/cloudowski/exitctl/internal/model"
)

// NodeKind of a graph node.
type NodeKind string

// Node kinds.
const (
	NodeResource       NodeKind = "resource"
	NodeComponent      NodeKind = "component"
	NodeImpliedService NodeKind = "implied_service"
)

// Class of a node after catalog classification.
const (
	ClassService    = "service"
	ClassNeutral    = "neutral"
	ClassUnknown    = "unknown"
	ClassUnverified = "unverified"
	// ClassContext marks resources of types that deploy nothing (a neutral
	// catalog entry with scored: false). They stay in the graph but are left
	// out of the score, the index range, coverage, and dependents counts.
	ClassContext   = "context"
	ClassComponent = "component" // components are not classified
)

// EdgeKind of a graph edge.
type EdgeKind string

// Edge kinds.
const (
	EdgeReference   EdgeKind = "reference"
	EdgeOrdering    EdgeKind = "ordering"
	EdgeBinding     EdgeKind = "binding"
	EdgeDataFlow    EdgeKind = "data-flow"
	EdgeUsage       EdgeKind = "usage"
	EdgeContainment EdgeKind = "containment"
)

// Confidence of an edge.
const (
	ConfExact         = "exact"
	ConfResourceLevel = "resource-level"
	ConfServiceLevel  = "service-level"
)

// Finding status.
const (
	FindingActive   = "active"
	FindingAccepted = "accepted"
)

// Finding is a trait instance on a node.
type Finding struct {
	ID         string              `json:"id"` // <node-id>::<trait-id>
	Trait      string              `json:"trait"`
	Level      catalog.Level       `json:"level"`
	Dimensions []catalog.Dimension `json:"dimensions"`
	Origin     string              `json:"origin"` // service | sdk | binding | rule:<id>
	Evidence   []model.Evidence    `json:"evidence"`
	Status     string              `json:"status"`
	Exception  string              `json:"exception,omitempty"`
}

// Node is a resource, application component, or implied service.
type Node struct {
	ID            string
	Aliases       []string
	Kind          NodeKind
	Type          string
	NativeAddress string
	// ProviderIdentity is the first non-empty provider identity of the merged
	// raw nodes (e.g. "aws:dynamodb:table/orders"), "" when unknown.
	ProviderIdentity string
	Repo             string
	Service          string // service ID when recognized
	Class            string
	Sources          []string // "<repo>//<path>" of every source merged into this node
	SourceKinds      []model.SourceKind
	Attributes       map[string]any
	Multiplicity     model.Multiplicity
	Evidence         []model.Evidence // declaration evidence, first is the primary declaration
	Findings         []Finding
	Group            string
	SCC              int // 0 = not in a multi-member cycle
	Wave             int
	Notes            []string
}

// Edge: From depends on To.
type Edge struct {
	From       string
	To         string
	Kind       EdgeKind
	Confidence string
	Evidence   []model.Evidence
}

// Graph holds nodes and edges sorted by ID with adjacency lists.
// Out[i] = indexes of nodes that node i depends on; In[i] = direct dependents.
// Both adjacency lists are sorted by node ID.
type Graph struct {
	Nodes []*Node
	Edges []*Edge
	Index map[string]int // node ID (and aliases) -> position in Nodes
	Out   [][]int
	In    [][]int
	Gaps  []model.Gap // gaps produced during building (unknown types, unresolved references)

	// algo holds the lazily computed condensation and memoized dependents
	// (see algo.go). It is reset by link.
	algo *algoState
}

// Node returns the node with the given ID or alias, or nil.
func (g *Graph) Node(id string) *Node {
	i, ok := g.Index[id]
	if !ok {
		return nil
	}
	return g.Nodes[i]
}
