# The exitctl dependency graph

Every scan builds one dependency graph. It connects infrastructure resources, Kubernetes
objects, and application code, and it drives most of what exitctl reports: resource weights,
anchors, blast radius, migration waves, and lock-in chains. This page explains what the graph
contains and how each derived view is computed.

For how the graph feeds the index, see [scoring.md](./scoring.md).

## Nodes

| Kind              | What it is                                                              | Example ID                                      |
| ----------------- | ----------------------------------------------------------------------- | ----------------------------------------------- |
| `resource`        | anything declared in a source: Terraform, CloudFormation, Kubernetes, container images, CI actions | `shop//infra#aws_dynamodb_table.orders`         |
| `component`       | an application unit: a directory with a dependency manifest             | `shop//services/orders#component`               |
| `implied_service` | a provider service used by code or bound from Kubernetes, but not declared in any scanned infrastructure | `implied//aws.dynamodb`                         |

Every resource also has a **class** from the catalog:

| Class        | Meaning                                                                  |
| ------------ | ------------------------------------------------------------------------ |
| `service`    | recognized as a provider service (for example `aws.dynamodb`)            |
| `neutral`    | recognized, carries no lock-in by itself (core Kubernetes objects, `random_*`) |
| `unverified` | only a draft (unreviewed) catalog entry would recognize it               |
| `unknown`    | the catalog does not know the type                                       |

Unknown and unverified nodes stay in the graph. They are real dependencies, so they still count
when computing dependents, waves, and blast radius. They are only left out of the index (see
[scoring.md](./scoring.md#index-range-and-coverage)).

### Identity

A node's canonical ID is `<repo>//<path>#<native-address>`:

- `repo` is `--repo-name`, else the normalized `origin` URL from the local `.git/config`, else
  the directory name.
- `path` is the directory of the source within the repository, with `/` separators.
- `native-address` is the source's own address: a Terraform address such as
  `module.db.aws_dynamodb_table.orders`, a CloudFormation logical ID, or
  `group/kind/namespace/name` for Kubernetes objects (`_` for cluster-scoped ones).

IDs never contain absolute paths, so they are the same on every machine.

### Identity merge

The same thing is often declared more than once: a Kubernetes object in a raw manifest and in a
rendered chart, or a bucket in two repositories. exitctl merges such declarations into one node
when any of these match:

- the canonical ID;
- the **provider identity**, when it is statically known (a literal name, for example
  `aws:dynamodb:table/orders`);
- for Kubernetes objects in the same repository, `group/kind/namespace/name`.

The merged node keeps the smallest canonical ID as its `id`; the other IDs become `aliases`
and can still be used in `explain`, `graph`, and exceptions. The node lists all sources and all
evidence. When the merged declarations classify differently, the strongest classification wins
(service, then neutral, then unverified, then unknown).

## Edges

An edge always means **"from depends on to"**: the `from` node cannot exist or work without
the `to` node. Arrows point from the dependent to its dependency.

| Kind          | Meaning                                                    | Example                                                  |
| ------------- | ---------------------------------------------------------- | -------------------------------------------------------- |
| `reference`   | from's configuration refers to to                          | a workload mounts a claim; a policy names a table        |
| `ordering`    | an explicit declared dependency without a data reference   | Terraform `depends_on`, CloudFormation `DependsOn`       |
| `binding`     | a Kubernetes object bound to cloud infrastructure          | a StorageClass with provisioner `ebs.csi.aws.com` -> EBS |
| `data-flow`   | to feeds from with data or events                          | a function triggered by a queue depends on the queue     |
| `usage`       | a component uses a service through its SDK                 | `orders` component -> DynamoDB table                     |
| `containment` | to is the network or organizational container of from      | subnet -> VPC                                            |

How the kind is chosen for a reference between resources: an explicit `depends_on` is
`ordering`; otherwise a catalog **edge hint** for the resource type and attribute decides
(for example `stream_arn` on a streaming destination is `data-flow`); otherwise it is
`reference`.

Every edge carries evidence (file and line). Duplicate edges (same from, to, and kind) are
merged and their evidence combined. Self-loops are dropped. A reference whose target cannot be
resolved (for example a remote module that is not present locally) becomes a coverage gap of
kind `unresolved_reference`, not an edge.

### Confidence

| Confidence       | Used on               | Meaning                                                        |
| ---------------- | --------------------- | -------------------------------------------------------------- |
| `exact`          | all non-usage edges   | stated in the source                                           |
| `resource-level` | `usage` edges         | a quoted literal in the matched code equals the target resource's name |
| `service-level`  | `usage` edges         | the code uses the service, but which resource cannot be determined |

## Code-to-infrastructure bridge

SDK detection finds which components use which services (from service-specific packages in
dependency manifests and from import or usage patterns in source files). For each
(component, service) pair:

1. If resources of that service exist in the scan, the component gets a `usage` edge to each of
   them. Resources in the component's own repository are preferred; when it has none,
   resources of that service in other merged repositories are used.
2. If no resource of that service exists, the component gets a `usage` edge to the **implied
   service** node `implied//<service-id>`, created once and shared by all components that use
   the service.

The usage adds an SDK finding (by default the `proprietary-sdk` trait) to the target node, so
code coupling counts even when the infrastructure lives elsewhere. A repository with only
application code still gets a meaningful result through implied service nodes.

Kubernetes bindings work the same way: a matching object gets a `binding` edge to the bound
service's resources, or to the implied service node. An implied service reached by a binding
also carries the service's own traits.

## Dependents and blast radius

The **dependents** of a node N are all nodes that have a directed path to N, over any edge
kind, excluding N itself. Their count D drives the node's weight `w = 1 + log2(1 + D)`.
Members of the same cycle are dependents of each other.

The **blast radius** of N is its dependents shown as a tree: a breadth-first walk from N
against the edge direction, where each dependent is attached to the node that first reached
it, visiting neighbors in ID order. The tree therefore shows the shortest dependency path from
every dependent to N, and it is the same on every run.

```sh
exitctl graph aws_dynamodb_table.orders --blast-radius
exitctl explain aws_dynamodb_table.orders
```

## Cycles and migration waves

Real Terraform and Kubernetes graphs contain cycles. exitctl finds the strongly connected
components (Tarjan's algorithm). A component with more than one member is a cycle: its nodes
can only move together, and they are reported as a "must move together" group with a shared
`scc` number. Every other node forms a unit on its own. Collapsing the cycles leaves a graph
without cycles, the condensation, on which waves and chains are computed.

A **migration wave** says when a unit can move, assuming dependencies move before their
dependents:

```text
wave(u) = 1                                   if u depends on nothing
          1 + max(wave(v) for v that u depends on)   otherwise
```

Wave 1 holds the foundations that depend on nothing. Each later wave holds units whose
dependencies are all in earlier waves. All members of a cycle share one wave. Unknown,
neutral, and component nodes take part in waves, since they are real dependencies, and are
labeled as such.

Waves are in the JSON result (`analysis.waves`), in `explain`, and in the graph view:

```sh
exitctl graph --waves
```

## Lock-in chains

A **lock-in chain** is a path of dependencies in which every step carries significant lock-in,
so the whole path tends to move as one.

- A node **qualifies** when its risk r is above 0 and at least the risk of a medium finding in
  the highest-weighted dimension. With default weights that threshold is 0.5: a medium finding
  in the data dimension qualifies, a medium finding that is only technical (0.45) does not, and
  any high or critical finding in a weighted dimension does.
- A unit (a single node or a cycle) qualifies when at least one member qualifies. In a chain it
  is named by its smallest qualifying member ID.
- Chains follow edges from dependent to dependency over qualifying units only. Each chain
  starts at a qualifying unit that no other qualifying unit depends on and follows the longest
  qualifying path from there; equally long paths are decided by the alphabetical order of their
  IDs.
- Only chains of at least two units are reported, longest first.

`exitctl explain <resource>` shows up to three chains that pass through the resource.

## Grouping

Large graphs are hard to read, so the terminal, image, and HTML views group nodes. Grouping
changes only the presentation; the graph data and all scores stay the same.

- **Type collapse** (always): nodes with the same type, the same service, and exactly the same
  dependencies and dependents collapse into one group labeled `<type> x N`, when there are at
  least 3 of them. Twenty identical queue policies pointing at the same queue become one node.
- **Container grouping** (only when the graph has more than `group_threshold` nodes, default
  50): nodes are grouped by Terraform module path (per root module directory, instance keys
  removed), by Kubernetes namespace, or by component directory (the component plus resources
  declared in the same directory). Containers with a single node are not grouped. A type group
  sits inside its container when all its members share one.
- Group IDs are derived from a hash of the group's signature, so they are stable across runs
  and input order. Each group carries the highest risk of its members and the sum of their
  contributions. In the HTML report, groups are expandable.

`exitctl graph --expand` turns grouping off.

## Export and merge

```sh
exitctl graph --format tree                       # terminal (default)
exitctl graph --format dot  -o graph.dot
exitctl graph --format svg  -o graph.svg
exitctl graph --format png  -o graph.png
exitctl graph --format json -o shop.graph.json    # data export
```

The JSON export is a result document with `kind: "graph"`. It carries the facts read from
the sources, nodes, edges, groups, cycles, evidence, identities, and every version (tool,
catalog, catalog schema, rules, configuration fingerprint). It is a valid input to `merge`
and round-trips without change.

`exitctl merge` combines results or graph exports from several repositories:

```sh
exitctl merge infra.json shop.graph.json payments.json -o estate.json
```

Merge takes the union of the facts of all inputs and then rebuilds the graph and the analysis
with the current catalog and configuration. Because facts do not depend on the catalog, a
result scanned with an older catalog can be re-evaluated with a newer one. Nodes from different
repositories merge by identity, so a bucket declared in one repository and used from code in
another becomes one node with a `usage` edge. The output does not depend on the order of the
inputs. The merged result lists every input with its original versions; an input with an
unsupported format version is rejected with its file name.

## Glossary

**Anchor**
: A scored node ranked high by contribution (weight x risk): high lock-in with many
  dependents. See [scoring.md](./scoring.md#anchors).

**Blast radius**
: The dependents of a node, shown as a tree of shortest dependency paths.

**Canonical ID**
: `<repo>//<path>#<native-address>`, stable across machines.

**Chain (lock-in chain)**
: A path of dependencies in which every unit carries at least medium-level risk in the
  highest-weighted dimension, so the path tends to move as one.

**Component**
: An application unit: a directory with a dependency manifest. Not scored itself.

**Cycle (SCC)**
: Nodes that depend on each other in a loop. They share a wave and must move together.

**Dependents**
: All nodes with a directed path to a node, excluding itself.

**Edge direction**
: "from depends on to". Arrows point at what is depended on.

**Implied service**
: A node for a service used from code or bound from Kubernetes, with no matching resource in
  the scanned infrastructure.

**Unknown vs unverified**
: An unknown node has a type the catalog does not know. An unverified node has a type that only
  an unreviewed draft catalog entry would recognize. Both stay in the graph and count against
  coverage; neither is scored.

**Wave**
: The step in which a unit can migrate: wave 1 depends on nothing, wave N depends only on
  earlier waves.
