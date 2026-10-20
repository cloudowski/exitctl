import { DynamoDBClient, GetItemCommand, PutItemCommand } from "@aws-sdk/client-dynamodb";

const client = new DynamoDBClient({});

export async function getOrder(id: string) {
  const out = await client.send(new GetItemCommand({ TableName: "orders", Key: { order_id: { S: id } } }));
  return out.Item;
}

export async function putOrder(id: string) {
  await client.send(new PutItemCommand({ TableName: "orders", Item: { order_id: { S: id } } }));
}
