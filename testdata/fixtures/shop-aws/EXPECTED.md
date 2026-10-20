# shop-aws fixture: expected results

A small AWS shop: Terraform (VPC, DynamoDB with stream, SQS, Lambda, Cognito), Kubernetes
on EKS (ALB ingress, EBS storage class, IRSA), and a Node.js service that uses the
DynamoDB SDK. Used by golden tests for `exitctl scan`, `explain`, `report`, and `graph`.

The golden files under `testdata/golden/` are the source of truth for exact output. This
file documents intent, so a reviewer can tell whether a golden change is a fix or a
regression.

## Expected top anchors

1. `aws_dynamodb_table.orders` (first). Reasons: `stream_enabled = true` with
   `stream_view_type = "NEW_IMAGE"` (infra/db.tf, lines 11-12), consumed by
   `aws_lambda_event_source_mapping.orders_stream`; SDK usage via
   `@aws-sdk/client-dynamodb` in `services/orders` (package.json, src/repo.ts,
   src/handler.ts); several dependents (Lambda env var, event source mapping).
2. `aws_cognito_user_pool.main` (identity, with `aws_cognito_user_pool_client.web` as a
   dependent).
3. `aws_lambda_function.order_processor` and `aws_sqs_queue.events` follow; exact order is
   decided by the golden file.

## Expected lever

`Biggest lever:` is either DynamoDB -> ScyllaDB or Cognito -> Keycloak. The exact value
is decided by the golden file (it depends on catalog weights).

## Redaction

`infra/vars.tf` sets `default = "hunter2"` on variable `db_password` and
`value = "hunter2"` on `aws_ssm_parameter.db`. The literal `hunter2` must not appear in
any result, excerpt, report, or graph output; attributes show `{"$redacted": true}`.

## What each file exercises

| Path | Exercises |
| ---- | --------- |
| `infra/providers.tf` | `required_providers` (aws, random), provider block; not resources |
| `infra/network.tf` | `aws_vpc.main`, `aws_subnet.a`/`b` and `aws_security_group.app` referencing the VPC via `vpc_id` (reference edges) |
| `infra/db.tf` | DynamoDB table with stream traits on known lines; `name = "orders"` literal for code bridging |
| `infra/queue.tf` | `aws_sqs_queue.events` with `name = "events"` |
| `infra/fn.tf` | IAM role, Lambda with `vpc_config` (subnet and SG refs) and env vars referencing table name and queue URL; two `aws_lambda_event_source_mapping` resources (DynamoDB stream via `stream_arn`, SQS queue via `arn`) |
| `infra/auth.tf` | Cognito user pool plus client referencing it |
| `infra/vars.tf` | Secret redaction (variable default and SSM parameter value); `random_id.suffix` as a neutral type (no lock-in, not an unknown type) |
| `k8s/namespace.yaml` | Namespace `shop` |
| `k8s/deployment.yaml` | Deployment `orders-api`: ECR image (registry lock-in), service account, PVC volume, `envFrom` ConfigMap, labels |
| `k8s/service.yaml` | Service selecting `app=orders-api` (selector edge) |
| `k8s/serviceaccount.yaml` | IRSA annotation `eks.amazonaws.com/role-arn` |
| `k8s/ingress.yaml` | `ingressClassName: alb` and `alb.ingress.kubernetes.io/*` annotations; backend service edge |
| `k8s/storageclass.yaml` | Provisioner `ebs.csi.aws.com` |
| `k8s/pvc.yaml` | PVC `orders-cache` using storage class `gp3` |
| `k8s/configmap.yaml` | ConfigMap `orders-config` (neutral) |
| `services/orders/package.json` | npm dependencies `@aws-sdk/client-dynamodb`, `@aws-sdk/lib-dynamodb`, `express` (neutral) |
| `services/orders/src/repo.ts` | Import of `@aws-sdk/client-dynamodb`; `TableName: "orders"` literal |
| `services/orders/src/handler.ts` | Import of `@aws-sdk/client-dynamodb`; commented-out `@aws-sdk/client-sqs` import that must be ignored (no SQS code use) |
| `services/orders/Dockerfile` | Multi-stage build: base `public.ecr.aws/...` (AWS registry), final `gcr.io/distroless/nodejs20`, `COPY --from=build` stage reference (not an image) |
| `.github/workflows/deploy.yml` | `aws-actions/configure-aws-credentials@v4`, `aws-actions/amazon-ecr-login@v2`; `actions/checkout@v4` is neutral |
