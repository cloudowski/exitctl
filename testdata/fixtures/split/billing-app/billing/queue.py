import json

import boto3

sqs = boto3.client("sqs")


def publish_invoice(queue_url, invoice):
    sqs.send_message(QueueUrl=queue_url, MessageBody=json.dumps(invoice))
