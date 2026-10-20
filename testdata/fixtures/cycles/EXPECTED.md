# cycles fixture: expected results

Exercises cycle detection (SCC condensation) and migration waves (FR-055, FR-056,
data-model.md 3.5). Terraform security groups reference each other and form a true 2-cycle;
Kubernetes objects form a plain chain. The golden files under `testdata/golden/` are the
source of truth for exact output; this file documents intent.

IDs below assume the repository name `cycles` (scanned directory name) and the native
address formats of the Terraform and Kubernetes detectors (K8s: `<group>/<Kind>/<ns>/<name>`,
empty group for core kinds).

## Expected edges (from depends on to)

| From | To | Why |
| ---- | -- | --- |
| `aws_security_group.a` | `aws_security_group.b` | `tags.Peer = aws_security_group.b.id` |
| `aws_security_group.b` | `aws_security_group.a` | `tags.Peer = aws_security_group.a.id` |
| `aws_security_group_rule.a_from_b` | `aws_security_group.a` | `security_group_id` |
| `aws_security_group_rule.a_from_b` | `aws_security_group.b` | `source_security_group_id` |
| `aws_security_group_rule.b_from_a` | `aws_security_group.a` | `source_security_group_id` |
| `aws_security_group_rule.b_from_a` | `aws_security_group.b` | `security_group_id` |
| `Service/cycles/web` | `apps/Deployment/cycles/web` | selector `app: web` matches the pod labels |
| `apps/Deployment/cycles/web` | `ConfigMap/cycles/web-config` | `envFrom.configMapRef` |

## Expected SCC

- Exactly one multi-member unit: `{aws_security_group.a, aws_security_group.b}`. Both nodes
  get the same non-zero `scc` value and are reported as "must move together".
- The two rules are not part of the cycle (they only depend on the groups). Every other node
  has `scc` 0.
- Dependents: each group has the other group and both rules as dependents (D = 3).

## Expected waves

| Wave | Units |
| ---- | ----- |
| 1 | `{aws_security_group.a, aws_security_group.b}` (cycle), `ConfigMap/cycles/web-config` |
| 2 | `aws_security_group_rule.a_from_b`, `aws_security_group_rule.b_from_a`, `apps/Deployment/cycles/web` |
| 3 | `Service/cycles/web` |

Within a wave, units are sorted by their smallest member ID, so Terraform units (dir `""`)
come before Kubernetes units (dir `k8s`).

## Files

| Path | Exercises |
| ---- | --------- |
| `main.tf` | security groups referencing each other through tags (2-cycle), `aws_security_group_rule` resources depending on both groups |
| `k8s/app.yaml` | multi-document YAML: ConfigMap, Deployment with `envFrom`, Service selecting the Deployment |
