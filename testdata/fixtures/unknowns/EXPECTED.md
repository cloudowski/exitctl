# unknowns fixture: expected results

Coverage honesty (US6, FR-037, quickstart V7). The fixture mixes recognized AWS resources with
types the catalog does not know, an unresolved remote module, a remote Kustomize base, an
unresolved reference, and one file that cannot be parsed. Unknown resources carry most of the
graph weight, so the grade is withheld and only the index range is shown.

The numbers below are for `exitctl scan . --include-drafts` (the built-in AWS entries are
still drafts). `internal/cli/testdata/script/coverage.txtar` asserts them.

## What each file exercises

| Path | Exercises |
| ---- | --------- |
| `infra/providers.tf` | `required_providers` for aws, `acme-corp/acme`, `exoticcloud/exoticcloud`; not resources |
| `infra/widgets.tf` | unknown types: `acme_widget.core`, `acme_widget.edge` (depends on core), `exoticcloud_bucket.assets` and `.logs` (both depend on `acme_widget.core`) |
| `infra/app.tf` | recognized `aws_sqs_queue.jobs`, `aws_dynamodb_table.state`, `aws_lambda_function.worker`, all referencing unknown resources; `aws_iam_role.worker` is referenced but never declared (unresolved reference) |
| `infra/network.tf` | registry module `terraform-aws-modules/vpc/aws` with no `.terraform/modules` (unresolved module, never downloaded); `aws_security_group.app` uses `module.vpc.vpc_id` |
| `infra/broken.tf` | unresolved merge conflict markers: the whole file fails to parse (status `failed`, neutral note) and its `aws_sns_topic` is not counted |
| `k8s/kustomization.yaml` | remote base `github.com/example-org/platform-base//deploy/base?ref=v1.4.0`: recorded as a `remote_kustomize` gap, never fetched; the kustomization is not built |
| `k8s/deployment.yaml` | a plain Deployment, read on its own as a Kubernetes manifest (neutral) |

## Graph weights

w = 1 + log2(1 + D), D = transitive dependents.

| Node | Class | D | w |
| ---- | ----- | - | - |
| `acme_widget.core` | unknown | 6 | 3.8074 |
| `acme_widget.edge` | unknown | 1 | 2 |
| `exoticcloud_bucket.assets` | unknown | 1 | 2 |
| `exoticcloud_bucket.logs` | unknown | 1 | 2 |
| `aws_sqs_queue.jobs` | service | 1 | 2 |
| `aws_dynamodb_table.state` | service | 0 | 1 |
| `aws_lambda_function.worker` | service | 0 | 1 |
| `aws_security_group.app` | service | 0 | 1 |
| `apps/Deployment/default/worker` | neutral | 0 | 1 |

Recognized weight 6, unknown weight 9.8074.

## Expected coverage and confidence

- Weighted coverage: 6 / 15.8074 = 0.3796 (37%), below the 70% provisional threshold.
- Confidence: **withheld**. No grade is given.
- Index 44 over the recognized nodes; range **17-79** (unknown resources at risk 0 and 1).
- Types: 5 of 7 recognized (71%); resources: 5 of 9 (55%); 2 unknown types.

Summary line:

```text
  Lock-in index range 17-79 - insufficient coverage for a grade (37%)
  ...
  Coverage: 71% of types, 55% of resources (2 unknown types: exitctl coverage)
```

## Expected `exitctl coverage`

- Unknown types (terraform), sorted by dependents: `acme_widget` (2 resources, 5 dependents,
  `infra/widgets.tf:3`), then `exoticcloud_bucket` (2 resources, 2 dependents,
  `infra/widgets.tf:13`). The type's dependents are the distinct resources of other types that
  depend on any resource of the type, so `acme_widget.edge` is not counted for `acme_widget`.
- Unresolved inputs: the module `terraform-aws-modules/vpc/aws` (`infra/network.tf`), the remote
  Kustomize base (`k8s/kustomization.yaml`), and the reference `aws_iam_role.worker`
  (`infra/app.tf`).
- Unverified knowledge: none with `--include-drafts`. Without it, the five AWS and Kubernetes
  matches are listed as unverified knowledge (draft entries) and nothing is scored.
- Parse failures: `infra/broken.tf (terraform): could not be parsed as HCL`. No parser error
  text is shown.
- No network access: remote module and remote base are reported as gaps without any fetch.
