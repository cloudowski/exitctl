import boto3

ddb = boto3.client("dynamodb")
s3 = boto3.resource('s3')
# boto3.client("sqs")


def get_order(order_id):
    out = ddb.get_item(TableName="orders", Key={"order_id": {"S": order_id}})
    return out.get("Item")


def upload_invoice(order_id, body):
    s3.Object("shop-invoices", f"{order_id}.pdf").put(Body=body)
