package sdkname

import (
	"testing"

	"github.com/cloudowski/exitctl/internal/model"
)

func TestDerive(t *testing.T) {
	tests := []struct {
		eco      model.Ecosystem
		name     string
		provider string
		service  string
	}{
		{model.EcoNPM, "@aws-sdk/client-dynamodb", "aws", "dynamodb"},
		{model.EcoNPM, "@google-cloud/pubsub", "gcp", "pubsub"},
		{model.EcoNPM, "@azure/service-bus", "azure", "service-bus"},
		{model.EcoNPM, "@azure/core-auth", "", ""},
		{model.EcoNPM, "@aws-sdk/types", "", ""},
		{model.EcoNPM, "express", "", ""},
		{model.EcoPyPI, "google-cloud-bigquery", "gcp", "bigquery"},
		{model.EcoPyPI, "google-cloud-core", "", ""},
		{model.EcoPyPI, "azure-mgmt-compute", "azure", "compute"},
		{model.EcoPyPI, "azure-core", "", ""},
		{model.EcoPyPI, "boto3", "", ""},
		{model.EcoGo, "github.com/aws/aws-sdk-go-v2/service/kinesis", "aws", "kinesis"},
		{model.EcoGo, "github.com/aws/aws-sdk-go/service/sqs", "aws", "sqs"},
		{model.EcoGo, "cloud.google.com/go/spanner", "gcp", "spanner"},
		{model.EcoGo, "cloud.google.com/go/pubsub/v2", "gcp", "pubsub"},
		{model.EcoGo, "github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus", "azure", "servicebus"},
		{model.EcoGo, "github.com/Azure/azure-sdk-for-go/sdk/azcore", "", ""},
		{model.EcoMaven, "software.amazon.awssdk:kinesis", "aws", "kinesis"},
		{model.EcoMaven, "software.amazon.awssdk:sdk-core", "", ""},
		{model.EcoMaven, "com.amazonaws:aws-java-sdk-sns", "aws", "sns"},
		{model.EcoMaven, "com.google.cloud:google-cloud-spanner", "gcp", "spanner"},
		{model.EcoMaven, "com.azure:azure-messaging-servicebus", "azure", "messaging-servicebus"},
		{model.EcoMaven, "com.azure:azure-core", "", ""},
		{model.EcoNuGet, "AWSSDK.Kinesis", "aws", "kinesis"},
		{model.EcoNuGet, "Google.Cloud.Storage.V1", "gcp", "storage"},
		{model.EcoNuGet, "Azure.Messaging.ServiceBus", "azure", "messaging-servicebus"},
		{model.EcoNuGet, "Azure.Core", "", ""},
		{model.EcoRubyGems, "aws-sdk-kinesis", "aws", "kinesis"},
		{model.EcoRubyGems, "aws-sdk-core", "", ""},
		{model.EcoRubyGems, "google-cloud-storage", "gcp", "storage"},
		{model.EcoComposer, "google/cloud-pubsub", "gcp", "pubsub"},
		{model.EcoCargo, "aws-sdk-s3", "aws", "s3"},
		{model.EcoCargo, "google-cloud-pubsub", "gcp", "pubsub"},
		{model.EcoCargo, "serde", "", ""},
	}
	for _, tc := range tests {
		ref, ok := Derive(tc.eco, tc.name)
		if ok != (tc.provider != "") || ref.Provider != tc.provider || ref.Service != tc.service {
			t.Errorf("Derive(%s, %q) = %+v, %v; want %s/%s", tc.eco, tc.name, ref, ok, tc.provider, tc.service)
		}
	}
}

func TestBotoClient(t *testing.T) {
	for line, want := range map[string]string{
		`ddb = boto3.client("dynamodb")`:           "dynamodb",
		`s3 = session.resource('s3', region='x')`:  "s3",
		`c = boto3.client(service_name="kinesis")`: "kinesis",
		`x = something.get_client(name)`:           "",
		`boto3.client(name)`:                       "",
	} {
		got := ""
		if m := BotoClient.FindStringSubmatch(line); m != nil {
			got = m[1]
		}
		if got != want {
			t.Errorf("%q: got %q, want %q", line, got, want)
		}
	}
}
