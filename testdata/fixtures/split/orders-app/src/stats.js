// The table name sits on the SDK import line so that exitctl links this
// component to the declared table with resource-level confidence.
const { DynamoDBClient, ScanCommand } = require("@aws-sdk/client-dynamodb"), table = { TableName: "orders" };

const client = new DynamoDBClient({});

async function countOrders() {
  const out = await client.send(new ScanCommand({ ...table, Select: "COUNT" }));
  return out.Count;
}

module.exports = { countOrders };
