# app-only fixture: expected results

A Python app that talks to AWS through `boto3` with no IaC at all. Validates implied
service nodes: services detected only from code are added to the graph as implied
nodes (no declared resource). The golden files under `testdata/golden/` are the source of
truth for exact output.

## Expected

- Package use: `boto3` (pypi, `1.35.0`) from `requirements.txt`; `flask` is neutral.
- Code use in `app/repo.py`: `boto3.client("dynamodb")` (line 3) and
  `boto3.resource('s3')` (line 4), with both quote styles.
- Implied service nodes: Amazon DynamoDB and Amazon S3.
- No SQS: line 5 `# boto3.client("sqs")` is a comment and must not count.
- No IaC: zero Terraform, Kubernetes, Helm, or CloudFormation resources; coverage reflects
  code-only sources.
- Anchors and lever: decided by the golden file.

## Files

| Path | Exercises |
| ---- | --------- |
| `requirements.txt` | pinned `boto3==1.35.0`, unpinned neutral `flask` |
| `app/__init__.py` | neutral Python module (no cloud usage) |
| `app/repo.py` | `import boto3`, client and resource calls, commented-out call |
