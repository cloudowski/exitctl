# multi-cloud fixture: expected results

Exercises providers other than AWS (FR-004) and Helm chart rendering with Config Connector
CRDs (FR-020). The golden files under `testdata/golden/` are the source of truth for exact
output; this file documents intent.

## Expected inventory

| Source | Resource | Expected service |
| ------ | -------- | ---------------- |
| `terraform/gcp.tf` | `google_pubsub_topic.orders` | GCP Pub/Sub |
| `terraform/gcp.tf` | `google_pubsub_subscription.orders` (references the topic) | GCP Pub/Sub |
| `terraform/gcp.tf` | `google_spanner_instance.main` | Cloud Spanner |
| `terraform/gcp.tf` | `google_spanner_database.db` (references the instance) | Cloud Spanner |
| `terraform/azure.tf` | `azurerm_resource_group.rg` | neutral or low (container only) |
| `terraform/azure.tf` | `azurerm_cosmosdb_account.acc` | Azure Cosmos DB |
| `terraform/azure.tf` | `azurerm_servicebus_namespace.bus` | Azure Service Bus |
| `terraform/azure.tf` | `azurerm_servicebus_queue.q` (references the namespace via `namespace_id`) | Azure Service Bus |
| `terraform/oci.tf` | `oci_database_autonomous_database.adb` (`compartment_id = var.compartment_id`, unknown value) | OCI Autonomous Database |
| `charts/app` (rendered) | `StorageBucket` `shop-assets` (`storage.cnrm.cloud.google.com/v1beta1`) | Google Cloud Storage via Config Connector |
| `charts/app` (rendered) | Deployment with `replicas: 2` from `values.yaml` | neutral workload |
| `cfn/stack.yaml` | `AWS::SQS::Queue` `OrdersQueue` | Amazon SQS |
| `cfn/stack.yaml` | `AWS::SNS::Topic` `OrdersTopic` | Amazon SNS |
| `cfn/stack.yaml` | `AWS::SNS::Subscription` `OrdersSubscription` (`!Ref` topic, `!GetAtt` queue) | Amazon SNS |

## Notes

- The Helm chart is local (`charts/app/Chart.yaml`, apiVersion v2), so it must be rendered
  with default values and must not appear as an unresolved chart gap. `templates/_helpers.tpl`
  provides `app.fullname` and `app.labels`.
- The StorageBucket name comes from `{{ .Values.bucketName }}` and must render as
  `shop-assets`.
- CloudFormation short-form tags (`!Ref`, `!GetAtt`) must parse and produce edges from the
  subscription to the topic and the queue.
- Anchors and lever: decided by the golden file. Spanner and Cosmos DB are expected near
  the top because of high data-plane lock-in.
- Multiple providers are present, so per-provider breakdowns should list gcp, azure, oci,
  and aws.
