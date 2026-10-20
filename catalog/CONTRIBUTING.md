# Contributing to the exitctl catalog

The catalog is the knowledge behind every exitctl result: which resource types belong to which
service, what lock-in each service carries, how to detect it in code, and where to go instead.
It is plain YAML data. Adding or correcting knowledge never requires a code change.

This guide covers the entry layout, how to model lock-in, the rule syntax, provenance and
review, and how to check your change before opening a pull request.

## Layout

One entry per file, so every contribution is a small, reviewable pull request.

```text
catalog/
├── catalog.yaml                      # manifest: catalog version, maintainers, neutrality link
├── NEUTRALITY.md                     # neutrality rules for alternatives
├── providers.yaml                    # provider list (aws, gcp, azure, oci, kubernetes, generic)
├── categories.yaml                   # category taxonomy (database.nosql.key-value, ...)
├── traits/<trait-id>.yaml            # one trait per file
├── services/<provider>/<service>.yaml
├── alternatives/<alternative-id>.yaml
└── neutral/<name>.yaml               # resource types that are recognized but carry no lock-in
```

Every file starts with `schema: "<major.minor>"` (currently `"1.0"`) and `kind:` (`trait`,
`service`, `alternative`, `neutral`, `catalog`, `providers`, `categories`). The full format is
defined by `schemas/catalog-entry.schema.json`.

IDs are stable forever. Never reuse or rename an ID; deprecate it and point to a successor
instead (see [Lifecycle](#provenance-and-review-lifecycle)).

| Kind        | ID format             | Example                    |
| ----------- | --------------------- | -------------------------- |
| trait       | slug                  | `managed-change-stream`    |
| service     | `<provider>.<slug>`   | `aws.dynamodb`             |
| alternative | slug                  | `scylladb`                 |
| neutral     | slug                  | `kubernetes-core`          |
| category    | dotted path           | `database.nosql.key-value` |

## Model lock-in with traits first

A **trait** is a reusable lock-in characteristic, defined once and attached to many services.
Services never encode findings directly; they compose traits. Before writing a new trait,
check `traits/` for one that already describes the coupling.

```yaml
schema: '1.0'
kind: trait
id: proprietary-sdk
name: Proprietary SDK
level: medium                 # default level: none, low, medium, high, critical
dimensions:                   # one or more: technical, data, operational, economic, skills
- technical
explanation: Application code imports a provider-specific SDK for this service, so code changes are needed in every component that uses it.
exit_hint: Isolate SDK calls behind an adapter layer and replace the adapter, or use a client for an open protocol.
provenance: { ... }
```

- `explanation` and `exit_hint` are at most 200 characters, factual, and free of fear
  language. They appear in reports as written.
- Choose dimensions carefully: they decide which dimension scores a finding counts toward and,
  through dimension weights, how much it counts (see `docs/scoring.md`).
- A trait file may also hold global `rules:` (see [Rules](#rules)).

## Services

A service maps resource types to a provider offering and lists its traits.

```yaml
schema: '1.0'
kind: service
id: aws.dynamodb
provider: aws
name: Amazon DynamoDB
category: database.nosql.key-value
resource_types:
  terraform: [aws_dynamodb_table, aws_dynamodb_global_table, ...]
  cloudformation: ['AWS::DynamoDB::Table', 'AWS::DynamoDB::GlobalTable', ...]
  kubernetes:
  - {group: dynamodb.services.k8s.aws, kind: Table}
traits:
- {id: proprietary-api, level: high, reason: 'DynamoDB has its own wire API; only ScyllaDB Alternator implements a compatible subset.'}
- {id: proprietary-data-model, level: high, reason: 'Partition/sort keys, GSIs, LSIs and single-table design shape the application around DynamoDB access patterns.'}
- {id: proprietary-sdk}
rules: [...]
sdk: [...]
edge_hints: [...]
alternatives: [...]
provenance: { ... }
```

### Trait references and level overrides

Each entry in `traits` is `{ id, level?, reason? }`. Without `level`, the trait's default level
applies. When you set `level`, you must also give a `reason` that explains why this service
differs from the default. Validation rejects an override without a reason.

### Resource types

`resource_types` keys are the source formats:

| Key                  | Values                                                      |
| -------------------- | ----------------------------------------------------------- |
| `terraform`          | Terraform/OpenTofu type names, e.g. `aws_sqs_queue`         |
| `cloudformation`     | `AWS::Service::Resource` names                              |
| `kubernetes`         | `{group, kind}` objects (controller CRDs such as ACK)        |
| `container_registry` | registry host patterns, e.g. `*.dkr.ecr.*.amazonaws.com`    |
| `ci_action`          | CI action names, e.g. `aws-actions/configure-aws-credentials` |

Each resource type maps to at most one service. When a type is matched by both a service and a
neutral entry, the service wins. Exact types win over wildcards.

### Neutral classifications

Types that are recognized but carry no lock-in (core Kubernetes objects, open source operators'
custom resources, generic CI actions, ...) go into `neutral/`. They count toward coverage and
score risk 0 unless a rule or binding adds a trait.

Types that deploy nothing at all (lookups such as `aws_caller_identity` or
`aws_iam_policy_document`, local-only types such as `random_*` and `time_*`, local rendering,
metadata files) go into a neutral entry with `scored: false`. Their resources are left out of
the score and coverage entirely instead of counting as portable infrastructure, which would
dilute the index. Use it only when the type creates nothing outside Terraform state or the
machine running the tool; a data source that looks up a provider resource (`data "aws_vpc"`)
still belongs to its service.

```yaml
schema: '1.0'
kind: neutral
id: kubernetes-core
provider: kubernetes
resource_types:
  kubernetes:
  - {group: '', kind: '*'}
  - {group: apps, kind: '*'}
  - {group: networking.k8s.io, kind: Ingress}
provenance: { ... }
```

## Rules

Rules adjust findings from resource attributes and graph context, without code. A rule lives
either in a service file (it applies only to that service's resources) or under `rules:` in a
trait file (a global rule, applied to any node its selector matches).

```yaml
- id: aws.dynamodb.streams            # unique; prefix with the service ID or "generic."
  match:                              # selector, at least one field
    resource_type: [aws_dynamodb_table]
  when: {attr: stream_enabled, equals: true}   # optional; absent means always
  then: {add_trait: managed-change-stream, level: high, evidence: stream_enabled}
```

### Selector (`match`)

Every field is a list; a field matches when any listed value matches, and all present fields
must match.

| Field           | Matches                                                        |
| --------------- | -------------------------------------------------------------- |
| `resource_type` | resource type, glob allowed (`'*'` matches every type)         |
| `service`       | service ID of the node                                         |
| `category`      | category ID or any category below it (`database` matches `database.nosql`) |
| `trait`         | a trait already on the node (including traits added by earlier rules) |
| `source`        | source kind, e.g. `terraform`, `cloudformation`, `kubernetes`  |

### Conditions (`when`)

| Operator             | Meaning                                                              |
| -------------------- | -------------------------------------------------------------------- |
| `attr` + `exists`    | the attribute is present (`exists: false` for absent); an empty list counts as absent |
| `attr` + `equals`    | the value equals a literal (numbers compare numerically)             |
| `attr` + `in`        | the value is one of a list                                           |
| `attr` + `matches`   | the string value matches an RE2 regular expression                   |
| `attr` + `gte`/`lte` | numeric comparison                                                   |
| `all: [...]`         | every sub-condition holds                                            |
| `any: [...]`         | at least one sub-condition holds                                     |
| `not: {...}`         | negation                                                             |
| `edge_to: {service \| category, kind, direction}` | the node has an edge to (direction `out`, the default) or from (direction `in`) a node of that service or category, optionally of one edge kind |
| `has_sdk_usage: true` | application code uses this node through an SDK (an incoming `usage` edge) |

Attribute paths are dotted (`ttl.enabled`, `capture_description.enabled`). When a path crosses
a list of blocks, the condition holds if any element matches. An `attr` without an operator is
an existence test. Several operators on one `attr` must all hold.

### Three-valued logic

IaC is often dynamic: a variable, a `for_each`, or a computed reference has no literal value
when scanning offline. exitctl never guesses. Each condition is **true**, **false**, or
**unknown**:

- A non-literal (or redacted) value makes the attribute comparison unknown. A missing
  attribute is false, not unknown.
- `all`: false if any part is false, else unknown if any part is unknown, else true.
- `any`: true if any part is true, else unknown if any part is unknown, else false.
- `not unknown` is unknown.

A rule fires only on **true**. When the result is unknown, the rule does not fire and the node
gets an `undetermined_rule:<rule-id>` note, so the reader can see that a rule could not be
decided.

### Effects (`then`)

Exactly one of:

- `add_trait: <trait-id>` with optional `level` (default: the trait's level). Adding a trait
  that is already present keeps the higher level.
- `set_level: { trait: <trait-id>, level: <level> }` changes the level of a trait already on
  the node. It can raise or lower it (`none` neutralizes it). It has no effect when the trait
  is absent.

Optional `evidence: <attr path>` names the attribute whose source line becomes the finding's
evidence. Without it, the resource declaration is used.

Rules run in a fixed order: the service's own rules, then global rules, each sorted by rule ID.
A trait added by an earlier rule is visible to the `trait` selector of a later rule.

### Examples

Raise lock-in when a feature is enabled (from `services/azure/event-hubs.yaml`):

```yaml
- id: azure.event-hubs.capture
  match: {resource_type: [azurerm_eventhub]}
  when: {attr: capture_description.enabled, equals: true}
  then: {add_trait: managed-only-feature, level: low, evidence: capture_description.enabled}
```

Lower lock-in when a compatible API is used (from `services/azure/cosmos-db.yaml`):

```yaml
- id: azure.cosmos-db.mongo-api
  match: {resource_type: [azurerm_cosmosdb_account]}
  when: {attr: kind, equals: MongoDB}
  then: {set_level: {trait: proprietary-query-language, level: low}, evidence: kind}
```

A global rule in a trait file, applied to every resource type (from
`traits/managed-change-stream.yaml`):

```yaml
rules:
- id: generic.stream-enabled
  match: {resource_type: ['*']}
  when: {attr: stream_enabled, equals: true}
  then: {add_trait: managed-change-stream, level: high, evidence: stream_enabled}
```

A combined condition (illustrative):

```yaml
when:
  all:
    - {attr: stream_enabled, equals: true}
    - any:
        - {attr: stream_view_type, in: [NEW_IMAGE, NEW_AND_OLD_IMAGES]}
        - not: {attr: ttl.enabled, exists: true}
    - {edge_to: {service: aws.lambda, kind: data-flow, direction: in}}
```

## SDK patterns

SDK patterns connect application code to a service. Each entry covers one ecosystem: `npm`,
`pypi`, `go`, `maven` (also Scala and Kotlin), `nuget`, `rubygems`, `composer` (PHP), or
`cargo` (Rust).

```yaml
sdk:
- ecosystem: pypi
  packages: [pynamodb, amazon-dax-client]                      # service-specific packages (globs)
  imports: ['^\s*(from|import)\s+(pynamodb|amazondax)\b']      # RE2, proves use at file:line
  usage: ['\.(client|resource)\(\s*(service_name\s*=\s*)?["''](dynamodb|dynamodbstreams|dax)["'']']
  gate: [boto3, aioboto3]                                      # generic SDK packages
  trait: {id: proprietary-sdk, level: low, reason: '...'}      # optional, default proprietary-sdk
```

- `packages`: dependency names that point to this service specifically. A match in a manifest
  is enough to record usage (the manifest line is the evidence).
- `imports`: source-line patterns, scanned only in components that depend on one of
  `packages`. A hit gives file:line evidence.
- `usage`: patterns for generic clients (for example `boto3.client("dynamodb")`), scanned only
  when a `gate` package is present.
- A generic SDK package alone (`boto3`, `aws-sdk`) never creates a usage. Only service-specific
  packages, imports, or usage patterns do.
- `trait`: the finding for SDK coupling. Use a level override with a reason when switching is
  easier than usual (S3-compatible clients, for example).

Cloud SDK packages that no `packages` glob (or `gate`) claims are reported as `unknown_sdk`
gaps in `exitctl coverage`, with the provider and service derived from the package name
(`@aws-sdk/client-*`, `google-cloud-*`, `azure-*`, `com.amazonaws:*`, ...) and from
`boto3.client("x")` calls. Use that list to find which entries and ecosystems to add.

Patterns must compile as Go RE2 (no backreferences or lookaround). Keep them anchored and
specific; a false positive here creates a finding with misleading evidence.

## Kubernetes bindings

Bindings describe how plain Kubernetes objects tie a workload to a cloud service. A matching
object gets a `binding` edge to the service's resources in scope, or to an implied service node
when none are declared.

```yaml
kubernetes_bindings:
- match: {kind: StorageClass, field: provisioner, in: [disk.csi.azure.com, kubernetes.io/azure-disk]}
- match: {kind: PersistentVolume, field: spec.csi.driver, in: [disk.csi.azure.com]}
- match: {kind: ServiceAccount, annotation: azure.workload.identity/client-id, exists: true}
- match: {kind: Service, annotation: service.beta.kubernetes.io/azure-*, exists: true}
```

- `match.kind` is required; `group` is optional.
- Test either a `field` (dotted path) or an `annotation` key (a trailing `*` matches a prefix).
- Then `exists`, `equals`, or `in`.
- `service:` on a binding points it to another service than the file's own (for example a
  ServiceAccount annotation that binds to an identity service).

## Edge hints

Edge hints classify references between resources. Without a hint, a reference becomes a
`reference` edge; explicit `depends_on`/`DependsOn` becomes `ordering`.

```yaml
edge_hints:
- {resource_type: aws_dynamodb_kinesis_streaming_destination, attribute: stream_arn, edge_kind: data-flow}
- {resource_type: aws_dax_cluster, attribute: subnet_group_name, edge_kind: containment}
```

Allowed `edge_kind` values: `reference`, `binding`, `data-flow`, `containment`. A hint
attribute matches the full attribute path first, then its last segment (`subnet_ids` matches
`vpc_config.subnet_ids`). `resource_type` may be a glob; exact types win. See `docs/graph.md`
for what each edge kind means.

## Alternatives

An alternative is described once in `alternatives/<id>.yaml` and mapped from services.

```yaml
schema: '1.0'
kind: alternative
id: scylladb
name: ScyllaDB
alternative_kind: oss-self-hosted   # oss-self-hosted, managed-oss, european-provider, other-provider
license: LicenseRef-ScyllaDB-Source-Available-1.0   # SPDX ID or "proprietary"
homepage: https://www.scylladb.com/
vendor_contributed: false
provenance: { ... }
```

The mapping inside a service carries the migration facts:

```yaml
alternatives:
- ref: kubernetes
  difficulty: medium                # low, medium, high, very-high
  compatibility: conceptual         # api-compatible, protocol-compatible, conceptual
  notes: Functions host runs in containers with KEDA scaling, but bindings to Azure services remain.
  residual_traits:
  - {id: proprietary-runtime, level: low, reason: 'The Functions host is open source and runs in containers, the programming model stays.'}
  recommended: true                 # maintainers only
  approved_by: cloudowski           # required for recommended and for verified mappings
```

- `residual_traits` list the lock-in that stays after switching. The biggest-lever simulation
  replaces the service's findings with exactly these traits, so a mapping without residual
  traits claims the switch removes all lock-in. List what remains honestly.
- `notes` are at most 200 characters.
- `recommended: true` may only be set by a maintainer listed in `catalog.yaml`, with
  `approved_by` naming them. It changes display order and adds a marker; it never changes any
  score, grade, or lever.
- Display order: recommended first, then difficulty ascending, then name.
- Every verified service needs at least one alternative mapping.

Alternatives must meet the entry criteria in [NEUTRALITY.md](./NEUTRALITY.md): publicly
available, actively maintained, and with a documented migration path. There are no paid
placements. Vendor-contributed entries are marked `vendor_contributed: true` and need
maintainer review like any other entry.

## Provenance and review lifecycle

Every entry carries provenance:

```yaml
provenance:
  origin: community                 # maintainer, community, vendor, llm
  author: your-handle
  reviewed_by: cloudowski           # required for verified
  reviewed_on: 2026-10-06           # required for verified
  status: verified                  # draft, verified, deprecated
  sources:                          # documentation you relied on
  - https://docs.aws.amazon.com/amazondynamodb/latest/developerguide/Streams.html
```

```text
draft ──(review: reviewed_by + reviewed_on [+ eval_passed for llm])──> verified
verified ──(replaced or withdrawn: successor or reason)──> deprecated
```

- **draft**: new or changed knowledge awaiting review. Drafts never affect scores. A resource
  that only a draft entry would recognize is classified `unverified`: it is counted as unknown
  for coverage and the index range, and it is listed under `coverage.unverified_matches` so the
  reader sees what the catalog is about to learn.
- **verified**: reviewed by a maintainer. Only verified entries classify resources and produce
  findings in a normal result.
- **deprecated**: kept for compatibility. A deprecated service no longer classifies resources
  but still resolves through `successor` and `aliases`, so older results and exceptions keep
  working. Rename a service by creating the new ID, listing the old ID in its `aliases`, and
  deprecating the old entry with `successor`.

## LLM-drafted entries

LLM-generated entries are welcome as a starting point, under stricter rules:

- `origin: llm` and an `llm` block are required:

  ```yaml
  provenance:
    origin: llm
    author: claude
    status: draft
    sources: [...]
    llm: {batch: 2026-10-06-v01, model: claude-opus-5-5, prompt_version: catalog-draft-v1, eval_passed: false}
  ```

  - `batch`: the generation batch, so a whole batch can be reviewed or withdrawn together.
  - `model`: the model that produced the draft.
  - `prompt_version`: the prompt used, so batches can be compared.
  - `eval_passed`: whether the entry passed evaluation. There is no automated catalog
    evaluation yet, so a maintainer's review is the evaluation: the review tool sets
    `eval_passed: true` when it verifies an LLM entry, and `false` when it moves the entry back
    to draft.
- An LLM entry stays `draft` until a human reviewer has checked it against its `sources`,
  `eval_passed` is `true`, and `reviewed_by`/`reviewed_on` are filled. Only then may its status
  become `verified`.
- `exitctl catalog validate` reports LLM-generated entries awaiting review as a separate
  count.

## Reviewing entries (maintainers)

`tools/catalogreview` is a local web tool for working through the review queue:

```sh
make review                      # or: go run ./tools/catalogreview -open
go run ./tools/catalogreview -reviewer <maintainer> -catalog catalog
```

It lists every service, trait, alternative, and neutral entry with filters (kind, status,
"has warnings", search) and progress per kind, and lets you:

- **Verify**, move **back to draft**, or **deprecate** one entry or a selection. Verifying
  records `reviewed_by`, `reviewed_on`, and (for LLM drafts) `eval_passed: true`, and sets
  `approved_by` on the service's alternative mappings. Back to draft removes the review record.
- Mark an alternative mapping as **recommended** (the star). Only catalog maintainers can; the
  tool records `approved_by`.
- **Remove** an alternative mapping from one service.
- **Hide an alternative everywhere** by deprecating the alternative entry: only verified
  alternatives are shown to users.
- Change **levels**: a trait's default level (on the trait's page, which also lists the
  services that override it), or a per-service override with its required reason (in the
  service's Traits table; choose "default" to remove the override). Editing a verified entry
  records you as its reviewer with today's date.

Warnings point at entries that will not behave as expected, for example a verified service
whose traits are still drafts (draft traits are ignored in scoring) or a recommended
alternative that is not verified (so it is not shown). Keyboard: `j`/`k` move, `v` verify,
`d` draft, `x` deprecate, `space` select.

Every change is validated against the whole catalog and rolled back if it would make the
catalog invalid (for example removing the last alternative of a verified service). Files keep
their layout, so `git diff` shows only what changed. The tool listens on 127.0.0.1 only. The
built-in catalog is embedded at build time, so rebuild exitctl (`make build`) to scan with the
reviewed catalog.

## Checking your change

Validate the catalog directory:

```sh
exitctl catalog validate catalog/
```

Validation checks, in order: every file against the schema; unique IDs per kind; that all
references resolve (provider, category, trait, alternative, successor, aliases); no alias
cycles; each resource type maps to at most one service; RE2 patterns compile; `recommended`
only with maintainer approval; verified services have at least one alternative. All problems
are reported at once as `<file>:<line>  <entry-id>  <problem>`, followed by counts per kind and
status. The exit code is 0 when the catalog is valid and 3 otherwise.

Then scan a repository that uses the service with your local catalog:

```sh
exitctl scan path/to/repo --catalog catalog/
exitctl explain <resource-address>
exitctl coverage
```

Check that the resource is recognized, that each finding has the evidence you expect, and that
no `undetermined_rule` note hides a rule you meant to fire. The `--catalog` directory is
validated before use; an invalid catalog is never used.

## Pull request checklist

- One entry per file; IDs follow the formats above and are new (never reused).
- Lock-in expressed through existing traits where possible; every level override has a reason.
- Rules have IDs prefixed with the service ID (or `generic.` for global rules).
- SDK patterns are service-specific and compile as RE2.
- Alternative mappings meet [NEUTRALITY.md](./NEUTRALITY.md) and list residual traits.
- Provenance is complete, with sources; new entries start as `draft`.
- `exitctl catalog validate` passes.
