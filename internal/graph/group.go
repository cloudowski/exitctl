package graph

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"

	"github.com/cloudowski/exitctl/internal/model"
)

// GroupKind of a node group (data-model.md 3.7).
type GroupKind string

// Group kinds.
const (
	GroupType      GroupKind = "type"      // collapsed same-type nodes with identical neighbors
	GroupModule    GroupKind = "module"    // Terraform module path
	GroupNamespace GroupKind = "namespace" // Kubernetes namespace
	GroupComponent GroupKind = "component" // application component directory
)

// MinTypeGroup is the smallest number of equivalent nodes collapsed into a
// type group (FR-059).
const MinTypeGroup = 3

// minContainerGroup is the smallest number of nodes forming a container group;
// a container with a single node adds nothing to the picture.
const minContainerGroup = 2

// Group is a collapsed (type) or container (module, namespace, component)
// group of nodes. Members are sorted node IDs. Container groups list every
// node inside the container, including nodes that are also members of a
// nested type group; a nested type group points at its container via Parent.
type Group struct {
	ID           string
	Kind         GroupKind
	Label        string
	Members      []string
	Parent       string // container group ID of a type group, "" otherwise
	MaxRisk      float64
	Contribution float64
}

// Groups computes the node groups of FR-059 and sets Node.Group on every
// node (resetting previous assignments). Type collapse applies always;
// container grouping applies only when the graph has more than threshold
// nodes. A node's Group is its type group when it has one, else its container
// group, else "". Group IDs are "g-" + the first 12 hex digits of the SHA-256
// of the group signature, so they do not depend on input order. The result is
// sorted by group ID. MaxRisk and Contribution are zero; see ScoreGroups.
func (g *Graph) Groups(threshold int) []Group {
	for _, n := range g.Nodes {
		n.Group = ""
	}
	types := g.typeGroups()
	var containers []Group
	if len(g.Nodes) > threshold {
		containers = g.containerGroups()
	}

	// Container group of every node, for Parent and Node.Group.
	containerOf := map[string]string{}
	for _, c := range containers {
		for _, m := range c.Members {
			containerOf[m] = c.ID
		}
	}
	for i := range types {
		t := &types[i]
		parent := containerOf[t.Members[0]]
		for _, m := range t.Members[1:] {
			if containerOf[m] != parent {
				parent = ""
				break
			}
		}
		t.Parent = parent
	}
	for _, c := range containers {
		for _, m := range c.Members {
			g.Nodes[g.Index[m]].Group = c.ID
		}
	}
	for _, t := range types {
		for _, m := range t.Members {
			g.Nodes[g.Index[m]].Group = t.ID
		}
	}

	out := append(types, containers...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ScoreGroups fills MaxRisk (maximum member risk) and Contribution (sum of
// member contributions) of every group from score, which returns the risk and
// contribution of a node ID.
func ScoreGroups(groups []Group, score func(id string) (risk, contribution float64)) {
	for i := range groups {
		gr := &groups[i]
		gr.MaxRisk, gr.Contribution = 0, 0
		for _, m := range gr.Members {
			r, c := score(m)
			if r > gr.MaxRisk {
				gr.MaxRisk = r
			}
			gr.Contribution += c
		}
	}
}

// groupID hashes a group signature into a stable ID.
func groupID(kind GroupKind, parts ...string) string {
	h := sha256.New()
	h.Write([]byte(kind))
	for _, p := range parts {
		h.Write([]byte{0})
		h.Write([]byte(p))
	}
	return "g-" + hex.EncodeToString(h.Sum(nil))[:12]
}

// typeGroups collapses nodes with equal (type, service, out-neighbors,
// in-neighbors) signatures when at least MinTypeGroup share one.
func (g *Graph) typeGroups() []Group {
	var order []string // signatures in first-seen (node ID) order
	members := map[string][]string{}
	nodeType := map[string]string{}
	for i, n := range g.Nodes {
		sig := n.Type + "\x00" + n.Service + "\x00" + g.idList(g.Out[i]) + "\x00" + g.idList(g.In[i])
		if _, ok := members[sig]; !ok {
			order = append(order, sig)
			nodeType[sig] = n.Type
		}
		members[sig] = append(members[sig], n.ID)
	}
	var out []Group
	for _, sig := range order {
		m := members[sig]
		if len(m) < MinTypeGroup {
			continue
		}
		out = append(out, Group{
			ID:      groupID(GroupType, sig),
			Kind:    GroupType,
			Label:   nodeType[sig] + " x " + strconv.Itoa(len(m)),
			Members: m, // already sorted: Nodes are sorted by ID
		})
	}
	return out
}

// idList joins the IDs of node indexes (sorted by ID, as in Out and In).
func (g *Graph) idList(idx []int) string {
	ids := make([]string, len(idx))
	for i, j := range idx {
		ids[i] = g.Nodes[j].ID
	}
	return strings.Join(ids, "\x01")
}

// containerGroups groups nodes by Terraform module path (keyed per root
// module directory), Kubernetes namespace (across repos, since a namespace is
// a cluster-wide name), or component directory (the component node plus the
// resources declared in the same directory). Containers with fewer than
// minContainerGroup nodes are dropped.
func (g *Graph) containerGroups() []Group {
	type container struct {
		kind GroupKind
		key  string
	}
	componentDirs := map[string]bool{}
	for _, n := range g.Nodes {
		if n.Kind == NodeComponent {
			componentDirs[idPrefix(n.ID)] = true
		}
	}
	var order []container // containers in first-seen (node ID) order
	members := map[container][]string{}
	for _, n := range g.Nodes {
		var c container
		switch {
		case n.Kind == NodeComponent:
			c = container{GroupComponent, idPrefix(n.ID)}
		case n.Kind == NodeResource && hasKind(n.SourceKinds, model.KindTerraform):
			if mp := modulePath(n.NativeAddress); mp != "" {
				c = container{GroupModule, idPrefix(n.ID) + "#" + mp}
			}
		case n.Kind == NodeResource && hasK8s(n.SourceKinds):
			if ns := k8sNamespace(n.NativeAddress); ns != "" {
				c = container{GroupNamespace, ns}
			}
		}
		if c.kind == "" && n.Kind == NodeResource && componentDirs[idPrefix(n.ID)] {
			c = container{GroupComponent, idPrefix(n.ID)}
		}
		if c.kind == "" {
			continue
		}
		if _, seen := members[c]; !seen {
			order = append(order, c)
		}
		members[c] = append(members[c], n.ID)
	}
	var out []Group
	for _, c := range order {
		m := members[c]
		if len(m) < minContainerGroup {
			continue
		}
		label := c.key
		switch c.kind {
		case GroupModule:
			label = c.key[strings.LastIndex(c.key, "#")+1:]
		case GroupComponent:
			label = componentLabel(g.Node(c.key+"#component"), c.key)
		}
		out = append(out, Group{ID: groupID(c.kind, c.key), Kind: c.kind, Label: label, Members: m})
	}
	return out
}

// idPrefix is the "<repo>//<dir>" part of a canonical ID.
func idPrefix(id string) string {
	if i := strings.Index(id, "#"); i >= 0 {
		return id[:i]
	}
	return id
}

// componentLabel is the component directory (its native address), falling
// back to the ID prefix.
func componentLabel(n *Node, prefix string) string {
	if n != nil && n.NativeAddress != "" && n.NativeAddress != "component" {
		return n.NativeAddress
	}
	return prefix
}

func hasKind(kinds []model.SourceKind, k model.SourceKind) bool {
	for _, x := range kinds {
		if x == k {
			return true
		}
	}
	return false
}

// modulePath returns the module path prefix of a Terraform address with
// instance keys removed ("module.a[0].module.b.aws_x.y" -> "module.a.module.b"),
// or "" for root-module resources.
func modulePath(addr string) string {
	var parts []string
	rest := addr
	for strings.HasPrefix(rest, "module.") {
		rest = rest[len("module."):]
		name, tail := splitStep(rest)
		if name == "" {
			break
		}
		if i := strings.Index(name, "["); i >= 0 {
			name = name[:i]
		}
		parts = append(parts, "module."+name)
		rest = tail
	}
	return strings.Join(parts, ".")
}

// splitStep splits an address at the first "." outside brackets and quotes.
func splitStep(s string) (step, rest string) {
	depth, quoted := 0, false
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '"' && (i == 0 || s[i-1] != '\\'):
			quoted = !quoted
		case quoted:
		case c == '[':
			depth++
		case c == ']':
			depth--
		case c == '.' && depth == 0:
			return s[:i], s[i+1:]
		}
	}
	return s, ""
}

// k8sNamespace extracts the namespace from a "group/kind/namespace/name"
// address; "" for cluster-scoped objects ("_") or other address forms.
func k8sNamespace(addr string) string {
	p := strings.Split(addr, "/")
	if len(p) != 4 || p[2] == "_" {
		return ""
	}
	return p[2]
}
