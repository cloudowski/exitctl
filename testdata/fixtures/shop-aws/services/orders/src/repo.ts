import { DynamoDBClient } from "@aws-sdk/client-dynamodb";
import { DynamoDBDocumentClient, GetCommand, PutCommand } from "@aws-sdk/lib-dynamodb";

const client = DynamoDBDocumentClient.from(new DynamoDBClient({}));

export async function getOrder(id: string) {
  const out = await client.send(new GetCommand({ TableName: "orders", Key: { order_id: id } }));
  return out.Item;
}

export async function putOrder(order: { order_id: string }) {
  await client.send(new PutCommand({ TableName: "orders", Item: order }));
}
