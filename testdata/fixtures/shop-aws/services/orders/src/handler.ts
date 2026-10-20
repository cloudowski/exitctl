import { DynamoDBStreamEvent } from "aws-lambda";
import { AttributeValue } from "@aws-sdk/client-dynamodb";
// import { SQSClient } from "@aws-sdk/client-sqs";
import { putOrder } from "./repo";

export async function handler(event: DynamoDBStreamEvent) {
  for (const record of event.Records) {
    const image = record.dynamodb?.NewImage as Record<string, AttributeValue> | undefined;
    const id = image?.order_id?.S;
    if (id) {
      await putOrder({ order_id: id });
    }
  }
}
