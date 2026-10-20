# exitctl lock-in report

Repository: shop-aws

## Executive Summary

**Grade C** - lock-in index **33/100**

The lock-in index is 33 out of 100, grade C. Lock-in is moderate: some provider-specific services hold application data or logic, so changing provider is a planned project. The top anchor is aws_dynamodb_table.orders with 4 dependent resources and SDK usage in 3 files. Replacing aws.vpc with Scaleway is the biggest single lever and lowers the index by 9 points. The catalog recognizes 100% of resource types and 100% of resources in the scanned inputs.

## Dimensions

| Dimension | Score |
| --- | ---: |
| technical | 34/100 |
| data | 11/100 |
| operational | 25/100 |
| skills | 3/100 |
| economic | not assessed |

## Biggest lever

aws.vpc -> Scaleway lowers the index by 9 pts.

## Anchors

| # | Address | Level | Dependents | Components using | Alternatives |
| ---: | --- | --- | ---: | ---: | --- |
| 1 | aws_dynamodb_table.orders | high | 4 | 1 | ScyllaDB, Apache Cassandra, MongoDB Community Server, PostgreSQL |
| 2 | aws_lambda_function.order_processor | high | 2 | 0 | Knative, OpenFaaS, Scaleway |
| 3 | aws_iam_role.lambda | medium | 7 | 0 | [R] Keycloak, OpenBao, ZITADEL |
| 4 | aws_vpc.main | medium | 6 | 0 | Scaleway |
| 5 | aws_cognito_user_pool.main | high | 1 | 0 | Keycloak, ZITADEL |
| 6 | action/aws-actions/configure-aws-credentials | medium | 4 | 0 | [R] Keycloak, OpenBao, ZITADEL |
| 7 | aws_security_group.app | medium | 3 | 0 | Scaleway |
| 8 | aws_sqs_queue.events | medium | 3 | 0 | Apache Kafka, NATS, RabbitMQ |
| 9 | aws_subnet.a | medium | 3 | 0 | Scaleway |
| 10 | aws_subnet.b | medium | 3 | 0 | Scaleway |
| 11 | aws.ebs (used by code, not declared) | low | 5 | 0 | Ceph, Scaleway |
| 12 | aws_cognito_user_pool_client.web | high | 0 | 0 | Keycloak, ZITADEL |
| 13 | aws_lambda_event_source_mapping.events_queue | high | 0 | 0 | Knative, OpenFaaS, Scaleway |
| 14 | aws_lambda_event_source_mapping.orders_stream | high | 0 | 0 | Knative, OpenFaaS, Scaleway |
| 15 | aws.elb (used by code, not declared) | low | 1 | 0 | Envoy Proxy, HAProxy, NGINX, Traefik Proxy |
| 16 | aws_ssm_parameter.db | medium | 0 | 0 | Kubernetes, OpenBao, SOPS |
| 17 | action/aws-actions/amazon-ecr-login | low | 0 | 0 | Forgejo, Gitea, Harbor, Scaleway |
| 18 | image/gcr.io/distroless/nodejs20 | low | 0 | 0 | Forgejo, Harbor, Scaleway |
| 19 | image/public.ecr.aws/docker/library/node:20-alpine | low | 0 | 0 | Forgejo, Gitea, Harbor, Scaleway |

[R] = recommended alternative

### 1. aws_dynamodb_table.orders

Dependents: 4. SDK usage: 3 files in 1 component. Service: Amazon DynamoDB.

Why this is an anchor: Managed change stream + Proprietary API + 3 SDK files + 4 dependent resources

Evidence:

- `infra/db.tf:1` `resource "aws_dynamodb_table" "orders" {`
- `services/orders/package.json:7` `"@aws-sdk/client-dynamodb": "^3.600.0",`
- `services/orders/package.json:8` `"@aws-sdk/lib-dynamodb": "^3.600.0",`
- `services/orders/src/handler.ts:2` `import { AttributeValue } from "@aws-sdk/client-dynamodb";`
- `services/orders/src/repo.ts:1` `import { DynamoDBClient } from "@aws-sdk/client-dynamodb";`
- `services/orders/src/repo.ts:2` `import { DynamoDBDocumentClient, GetCommand, PutCommand } from "@aws-sdk/lib-dynamodb";`

Findings:

- high: managed-change-stream (technical, data)
- high: proprietary-api (technical)
- high: proprietary-data-model (technical, data)
- medium: proprietary-sdk (technical)

### 2. aws_lambda_function.order_processor

Dependents: 2. Service: aws.lambda.

Why this is an anchor: Proprietary runtime + Proprietary SDK + 2 dependent resources

Evidence:

- `infra/fn.tf:14` `resource "aws_lambda_function" "order_processor" {`

Findings:

- medium: proprietary-event-model (technical)
- high: proprietary-runtime (technical, operational)
- medium: proprietary-sdk (technical)

### 3. aws_iam_role.lambda

Dependents: 7. Service: AWS Identity and Access Management (IAM).

Why this is an anchor: Proprietary IAM model + Skills specialization + 7 dependent resources

Evidence:

- `infra/fn.tf:1` `resource "aws_iam_role" "lambda" {`

Findings:

- medium: proprietary-iam-model (technical, operational)
- low: skills-specialization (skills)

### 4. aws_vpc.main

Dependents: 6. Service: aws.vpc.

Why this is an anchor: Provider network construct + 6 dependent resources

Evidence:

- `infra/network.tf:1` `resource "aws_vpc" "main" {`

Findings:

- medium: provider-network-construct (technical, operational)

### 5. aws_cognito_user_pool.main

Dependents: 1. Service: aws.cognito.

Why this is an anchor: Non-exportable data + Proprietary API + 1 dependent resource

Evidence:

- `infra/auth.tf:1` `resource "aws_cognito_user_pool" "main" {`

Findings:

- high: non-exportable-data (data)
- high: proprietary-api (technical)
- medium: proprietary-sdk (technical)

### 6. action/aws-actions/configure-aws-credentials

Dependents: 4. Service: AWS Identity and Access Management (IAM).

Why this is an anchor: Proprietary IAM model + Provider CI authentication + 4 dependent resources

Evidence:

- `.github/workflows/deploy.yml:16` `- uses: aws-actions/configure-aws-credentials@v4`

Findings:

- medium: proprietary-iam-model (technical, operational)
- low: provider-ci-auth (operational)
- low: skills-specialization (skills)

### 7. aws_security_group.app

Dependents: 3. Service: aws.vpc.

Why this is an anchor: Provider network construct + 3 dependent resources

Evidence:

- `infra/network.tf:22` `resource "aws_security_group" "app" {`

Findings:

- medium: provider-network-construct (technical, operational)

### 8. aws_sqs_queue.events

Dependents: 3. Service: aws.sqs.

Why this is an anchor: Proprietary API + Proprietary SDK + 3 dependent resources

Evidence:

- `infra/queue.tf:1` `resource "aws_sqs_queue" "events" {`

Findings:

- medium: proprietary-api (technical)
- medium: proprietary-sdk (technical)

### 9. aws_subnet.a

Dependents: 3. Service: aws.vpc.

Why this is an anchor: Provider network construct + 3 dependent resources

Evidence:

- `infra/network.tf:10` `resource "aws_subnet" "a" {`

Findings:

- medium: provider-network-construct (technical, operational)

### 10. aws_subnet.b

Dependents: 3. Service: aws.vpc.

Why this is an anchor: Provider network construct + 3 dependent resources

Evidence:

- `infra/network.tf:16` `resource "aws_subnet" "b" {`

Findings:

- medium: provider-network-construct (technical, operational)

### 11. aws.ebs (used by code, not declared)

Dependents: 5. Service: aws.ebs.

Why this is an anchor: Data gravity + Operational tooling coupling + 5 dependent resources

Evidence:

- `k8s/storageclass.yaml:2` `kind: StorageClass`

Findings:

- low: data-gravity (data)
- low: operational-tooling-coupling (operational)

### 12. aws_cognito_user_pool_client.web

Dependents: 0. Service: aws.cognito.

Why this is an anchor: Non-exportable data + Proprietary API

Evidence:

- `infra/auth.tf:9` `resource "aws_cognito_user_pool_client" "web" {`

Findings:

- high: non-exportable-data (data)
- high: proprietary-api (technical)
- medium: proprietary-sdk (technical)

### 13. aws_lambda_event_source_mapping.events_queue

Dependents: 0. Service: aws.lambda.

Why this is an anchor: Proprietary runtime + Managed change stream

Evidence:

- `infra/fn.tf:40` `resource "aws_lambda_event_source_mapping" "events_queue" {`

Findings:

- medium: managed-change-stream (technical, data)
- medium: proprietary-event-model (technical)
- high: proprietary-runtime (technical, operational)
- medium: proprietary-sdk (technical)

### 14. aws_lambda_event_source_mapping.orders_stream

Dependents: 0. Service: aws.lambda.

Why this is an anchor: Proprietary runtime + Managed change stream

Evidence:

- `infra/fn.tf:34` `resource "aws_lambda_event_source_mapping" "orders_stream" {`

Findings:

- medium: managed-change-stream (technical, data)
- medium: proprietary-event-model (technical)
- high: proprietary-runtime (technical, operational)
- medium: proprietary-sdk (technical)

### 15. aws.elb (used by code, not declared)

Dependents: 1. Service: aws.elb.

Why this is an anchor: Provider network construct + Provider-specific annotations + 1 dependent resource

Evidence:

- `k8s/ingress.yaml:2` `kind: Ingress`

Findings:

- low: provider-network-construct (technical, operational)
- low: provider-specific-annotations (technical)

### 16. aws_ssm_parameter.db

Dependents: 0. Service: aws.ssm-parameter-store.

Why this is an anchor: Proprietary SDK + Proprietary API

Evidence:

- `infra/vars.tf:7` `resource "aws_ssm_parameter" "db" {`

Findings:

- low: proprietary-api (technical)
- medium: proprietary-sdk (technical)

### 17. action/aws-actions/amazon-ecr-login

Dependents: 0. Service: aws.ecr.

Why this is an anchor: Provider registry

Evidence:

- `.github/workflows/deploy.yml:20` `- uses: aws-actions/amazon-ecr-login@v2`

Findings:

- low: provider-registry (operational)

### 18. image/gcr.io/distroless/nodejs20

Dependents: 0. Service: gcp.artifact-registry.

Why this is an anchor: Provider registry

Evidence:

- `services/orders/Dockerfile:8` `FROM gcr.io/distroless/nodejs20`

Findings:

- low: provider-registry (operational)

### 19. image/public.ecr.aws/docker/library/node:20-alpine

Dependents: 0. Service: aws.ecr.

Why this is an anchor: Provider registry

Evidence:

- `services/orders/Dockerfile:1` `FROM public.ecr.aws/docker/library/node:20-alpine AS build`

Findings:

- low: provider-registry (operational)

## Dependents of top anchors

### 1. aws_dynamodb_table.orders (4 dependents)

```text
aws_dynamodb_table.orders
|- aws_lambda_event_source_mapping.orders_stream
|- aws_lambda_function.order_processor
|  `- aws_lambda_event_source_mapping.events_queue
`- component services/orders
```

### 2. aws_lambda_function.order_processor (2 dependents)

```text
aws_lambda_function.order_processor
|- aws_lambda_event_source_mapping.events_queue
`- aws_lambda_event_source_mapping.orders_stream
```

### 3. aws_iam_role.lambda (7 dependents)

```text
aws_iam_role.lambda
|- ServiceAccount/shop/orders-api
|  `- apps/Deployment/shop/orders-api
|     `- Service/shop/orders-api
|        `- networking.k8s.io/Ingress/shop/orders-api
`- aws_lambda_function.order_processor
   |- aws_lambda_event_source_mapping.events_queue
   `- aws_lambda_event_source_mapping.orders_stream
```

### 4. aws_vpc.main (6 dependents)

```text
aws_vpc.main
|- aws_security_group.app
|  `- aws_lambda_function.order_processor
|     |- aws_lambda_event_source_mapping.events_queue
|     `- aws_lambda_event_source_mapping.orders_stream
|- aws_subnet.a
`- aws_subnet.b
```

### 5. aws_cognito_user_pool.main (1 dependent)

```text
aws_cognito_user_pool.main
`- aws_cognito_user_pool_client.web
```

## Alternatives

| # | Anchor | Alternative | Kind | Difficulty | Compatibility | Notes |
| ---: | --- | --- | --- | --- | --- | --- |
| 1 | aws_dynamodb_table.orders | ScyllaDB | oss-self-hosted | medium | api-compatible | ScyllaDB Alternator implements the DynamoDB API; streams, transactions and some expressions differ, so test coverage matters. |
| 1 | aws_dynamodb_table.orders | Apache Cassandra | oss-self-hosted | high | conceptual | Wide-column model fits key-based access; data model and queries must be redesigned in CQL. |
| 1 | aws_dynamodb_table.orders | MongoDB Community Server | oss-self-hosted | high | conceptual | Document model covers items and secondary indexes; access code and change streams need a rewrite. |
| 1 | aws_dynamodb_table.orders | PostgreSQL | oss-self-hosted | high | conceptual | JSONB tables with composite keys can host single-table designs; queries and streams are rewritten. |
| 2 | aws_lambda_function.order_processor | Knative | oss-self-hosted | medium | conceptual | Knative Serving runs HTTP containers with scale to zero; Eventing replaces event source mappings. |
| 2 | aws_lambda_function.order_processor | OpenFaaS | oss-self-hosted | medium | conceptual | Functions run as containers on Kubernetes; handlers and event triggers (queues, cron, HTTP) need adaptation. |
| 2 | aws_lambda_function.order_processor | Scaleway | european-provider | medium | conceptual | Scaleway Serverless Functions and Containers offer managed functions with HTTP, cron and queue triggers. |
| 3 | aws_iam_role.lambda | [R] Keycloak | oss-self-hosted | high | conceptual | Keycloak provides identities and OIDC tokens; resource permissions must be redone in each target system. |
| 3 | aws_iam_role.lambda | OpenBao | oss-self-hosted | medium | conceptual | Vault/OpenBao issues short-lived credentials and secrets; replaces access keys and STS role assumption. |
| 3 | aws_iam_role.lambda | ZITADEL | oss-self-hosted | high | conceptual | ZITADEL provides identities and OIDC; cloud resource authorization must be redesigned. |
| 4 | aws_vpc.main | Scaleway | european-provider | low | conceptual | Scaleway VPC and Private Networks with Public Gateways map to VPCs, subnets and NAT. |
| 5 | aws_cognito_user_pool.main | Keycloak | oss-self-hosted | high | protocol-compatible | OIDC/OAuth2 apps switch issuer; user import works, but passwords need reset or a lazy migration flow. |
| 5 | aws_cognito_user_pool.main | ZITADEL | oss-self-hosted | high | protocol-compatible | OIDC provider with user import APIs; Cognito triggers become actions and password hashes are not portable. |
| 6 | action/aws-actions/configure-aws-credentials | [R] Keycloak | oss-self-hosted | high | conceptual | Keycloak provides identities and OIDC tokens; resource permissions must be redone in each target system. |
| 6 | action/aws-actions/configure-aws-credentials | OpenBao | oss-self-hosted | medium | conceptual | Vault/OpenBao issues short-lived credentials and secrets; replaces access keys and STS role assumption. |
| 6 | action/aws-actions/configure-aws-credentials | ZITADEL | oss-self-hosted | high | conceptual | ZITADEL provides identities and OIDC; cloud resource authorization must be redesigned. |
| 7 | aws_security_group.app | Scaleway | european-provider | low | conceptual | Scaleway VPC and Private Networks with Public Gateways map to VPCs, subnets and NAT. |
| 8 | aws_sqs_queue.events | Apache Kafka | oss-self-hosted | medium | conceptual | Fits when queues carry event streams; per-message visibility and delays have no direct equivalent. |
| 8 | aws_sqs_queue.events | NATS | oss-self-hosted | medium | conceptual | JetStream work-queue streams with ack and redelivery replace SQS queues; producers and consumers are rewritten. |
| 8 | aws_sqs_queue.events | RabbitMQ | oss-self-hosted | medium | conceptual | Queues, acknowledgements and dead-letter exchanges cover SQS patterns; client code changes to AMQP. |
| 9 | aws_subnet.a | Scaleway | european-provider | low | conceptual | Scaleway VPC and Private Networks with Public Gateways map to VPCs, subnets and NAT. |
| 10 | aws_subnet.b | Scaleway | european-provider | low | conceptual | Scaleway VPC and Private Networks with Public Gateways map to VPCs, subnets and NAT. |
| 11 | aws.ebs (used by code, not declared) | Ceph | oss-self-hosted | low | conceptual | Ceph RBD provides replicated block volumes with a Kubernetes CSI driver; data is copied at file or block level. |
| 11 | aws.ebs (used by code, not declared) | Scaleway | european-provider | low | conceptual | Scaleway Block Storage volumes attach to instances and Kapsule clusters via CSI. |
| 12 | aws_cognito_user_pool_client.web | Keycloak | oss-self-hosted | high | protocol-compatible | OIDC/OAuth2 apps switch issuer; user import works, but passwords need reset or a lazy migration flow. |
| 12 | aws_cognito_user_pool_client.web | ZITADEL | oss-self-hosted | high | protocol-compatible | OIDC provider with user import APIs; Cognito triggers become actions and password hashes are not portable. |
| 13 | aws_lambda_event_source_mapping.events_queue | Knative | oss-self-hosted | medium | conceptual | Knative Serving runs HTTP containers with scale to zero; Eventing replaces event source mappings. |
| 13 | aws_lambda_event_source_mapping.events_queue | OpenFaaS | oss-self-hosted | medium | conceptual | Functions run as containers on Kubernetes; handlers and event triggers (queues, cron, HTTP) need adaptation. |
| 13 | aws_lambda_event_source_mapping.events_queue | Scaleway | european-provider | medium | conceptual | Scaleway Serverless Functions and Containers offer managed functions with HTTP, cron and queue triggers. |
| 14 | aws_lambda_event_source_mapping.orders_stream | Knative | oss-self-hosted | medium | conceptual | Knative Serving runs HTTP containers with scale to zero; Eventing replaces event source mappings. |
| 14 | aws_lambda_event_source_mapping.orders_stream | OpenFaaS | oss-self-hosted | medium | conceptual | Functions run as containers on Kubernetes; handlers and event triggers (queues, cron, HTTP) need adaptation. |
| 14 | aws_lambda_event_source_mapping.orders_stream | Scaleway | european-provider | medium | conceptual | Scaleway Serverless Functions and Containers offer managed functions with HTTP, cron and queue triggers. |
| 15 | aws.elb (used by code, not declared) | Envoy Proxy | oss-self-hosted | low | conceptual | Envoy or Envoy Gateway implements L4/L7 routing with the Kubernetes Gateway API. |
| 15 | aws.elb (used by code, not declared) | HAProxy | oss-self-hosted | low | conceptual | HAProxy covers L4 and L7 load balancing with health checks and stickiness. |
| 15 | aws.elb (used by code, not declared) | NGINX | oss-self-hosted | low | conceptual | NGINX (or ingress-nginx) handles L7 routing and TLS; listener rules translate to server and location blocks. |
| 15 | aws.elb (used by code, not declared) | Traefik Proxy | oss-self-hosted | low | conceptual | Traefik ingress replaces ALB Ingress; alb.ingress annotations become Traefik routers and middlewares. |
| 16 | aws_ssm_parameter.db | Kubernetes | oss-self-hosted | low | conceptual | ConfigMaps and Secrets provide configuration to workloads running on Kubernetes. |
| 16 | aws_ssm_parameter.db | OpenBao | oss-self-hosted | low | conceptual | KV secrets engine stores hierarchical configuration and secrets; lookups in code change. |
| 16 | aws_ssm_parameter.db | SOPS | oss-self-hosted | low | conceptual | Encrypted configuration files in Git replace static parameters; no runtime API. |
| 17 | action/aws-actions/amazon-ecr-login | Forgejo | oss-self-hosted | low | protocol-compatible | Forgejo package registry hosts OCI container images next to source code. |
| 17 | action/aws-actions/amazon-ecr-login | Gitea | oss-self-hosted | low | protocol-compatible | Gitea package registry hosts OCI container images next to source code. |
| 17 | action/aws-actions/amazon-ecr-login | Harbor | oss-self-hosted | low | protocol-compatible | OCI-compliant registry with replication, scanning and proxy cache; images move with skopeo or crane. |
| 17 | action/aws-actions/amazon-ecr-login | Scaleway | european-provider | low | protocol-compatible | Scaleway Container Registry is a managed OCI registry. |
| 18 | image/gcr.io/distroless/nodejs20 | Forgejo | oss-self-hosted | low | api-compatible | Built-in package registry supports OCI, npm, Maven and PyPI. |
| 18 | image/gcr.io/distroless/nodejs20 | Harbor | oss-self-hosted | low | api-compatible | OCI registry with replication; copy images with skopeo or crane and update image references. |
| 18 | image/gcr.io/distroless/nodejs20 | Scaleway | european-provider | low | api-compatible | Scaleway Container Registry stores OCI images. |
| 19 | image/public.ecr.aws/docker/library/node:20-alpine | Forgejo | oss-self-hosted | low | protocol-compatible | Forgejo package registry hosts OCI container images next to source code. |
| 19 | image/public.ecr.aws/docker/library/node:20-alpine | Gitea | oss-self-hosted | low | protocol-compatible | Gitea package registry hosts OCI container images next to source code. |
| 19 | image/public.ecr.aws/docker/library/node:20-alpine | Harbor | oss-self-hosted | low | protocol-compatible | OCI-compliant registry with replication, scanning and proxy cache; images move with skopeo or crane. |
| 19 | image/public.ecr.aws/docker/library/node:20-alpine | Scaleway | european-provider | low | protocol-compatible | Scaleway Container Registry is a managed OCI registry. |

[R] = recommended alternative

## Coverage

- Resource types recognized: 100%
- Resources recognized: 100%
- Weighted coverage: 100%

Gaps:

- Reference to an object outside the scanned inputs: `shop-aws//#networking.k8s.io/IngressClass/_/alb` in `k8s/ingress.yaml`

## Exceptions

No exceptions configured.

---

Exit cost, contracts, and an exit plan: Exit Readiness

Support: support@example.com

exitctl 0.1.0-dev · catalog 2026.10.1 · rules 1.1.0 · config 8d36fc36 (draft catalog)
