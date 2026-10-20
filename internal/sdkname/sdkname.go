// Package sdkname derives a cloud provider and service name from the naming
// conventions of official cloud SDK packages (@aws-sdk/client-*, google-cloud-*,
// azure-*, com.amazonaws:*, ...). It is pure name matching: it never consults
// the catalog, so the graph builder can report packages the catalog does not
// map yet.
package sdkname

import (
	"regexp"
	"strings"

	"github.com/cloudowski/exitctl/internal/model"
)

// Provider IDs, as used by catalog service IDs.
const (
	AWS   = "aws"
	GCP   = "gcp"
	Azure = "azure"
)

// Ref is a cloud SDK package recognized by name.
type Ref struct {
	Provider string
	Service  string // lowercase, '-' separated, e.g. "dynamodb-streams"
}

// shared names the packages of each provider that carry no service: core
// runtimes, credential helpers, transports and bills of materials.
var shared = map[string]map[string]bool{
	AWS: set("core", "sdk-core", "aws-core", "auth", "regions", "http-client-spi", "apache-client",
		"apache5-client", "netty-nio-client", "url-connection-client", "utils", "bom", "bom-internal",
		"arns", "protocol-core", "aws-json-protocol", "aws-query-protocol", "aws-xml-protocol",
		"aws-cbor-protocol", "annotations", "profiles", "metrics-spi", "json-utils", "endpoints-spi",
		"identity-spi", "retries", "retries-spi", "checksums", "checksums-spi", "crt-core",
		"aws-crt-client", "third-party-jackson-core", "third-party-jackson-dataformat-cbor",
		"auth-crt", "sdk-core-parent", "parent", "services", "codegen", "test-utils", "mapper-annotations"),
	GCP: set("core", "common", "testutils", "bom", "shared-config", "shared-dependencies",
		"promisify", "paginator", "projectify", "precise-date", "functions-framework", "auth",
		"longrunning", "civil", "compute-metadata", "metadata", "datastore-core"),
	Azure: set("core", "common", "core-http", "core-rest-pipeline", "core-auth", "core-client",
		"core-util", "core-lro", "core-paging", "core-tracing", "core-xml", "core-amqp", "core-sse",
		"abort-controller", "logger", "msal", "mgmt-core", "mgmt-resource", "azcore", "internal",
		"json", "xml", "client-sdk-parent", "sdk-bom", "identity-broker", "perf-test-core",
		"storage-common", "monitor-opentelemetry-exporter"),
}

func set(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

// Derive returns the provider and service a package name designates, or
// ok=false when the name does not follow a known cloud SDK convention or is a
// shared (non-service) package.
func Derive(eco model.Ecosystem, name string) (Ref, bool) {
	var provider, svc string
	switch eco {
	case model.EcoNPM:
		provider, svc = npm(name)
	case model.EcoPyPI:
		provider, svc = pypi(name)
	case model.EcoGo:
		provider, svc = golang(name)
	case model.EcoMaven:
		provider, svc = maven(name)
	case model.EcoNuGet:
		provider, svc = nuget(name)
	case model.EcoRubyGems:
		provider, svc = rubygems(name)
	case model.EcoComposer:
		provider, svc = composer(name)
	case model.EcoCargo:
		provider, svc = cargo(name)
	}
	svc = Normalize(svc)
	if provider == "" || svc == "" || shared[provider][svc] {
		return Ref{}, false
	}
	return Ref{Provider: provider, Service: svc}, true
}

// Normalize lowercases a service name and joins words with '-'
// ("DynamoDBv2" -> "dynamodbv2", "Cloud_Run" -> "cloud-run").
func Normalize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.NewReplacer("_", "-", ".", "-", " ", "-").Replace(s)
	return strings.Trim(s, "-")
}

func npm(name string) (string, string) {
	switch {
	case strings.HasPrefix(name, "@aws-sdk/client-"):
		return AWS, strings.TrimPrefix(name, "@aws-sdk/client-")
	case strings.HasPrefix(name, "@google-cloud/"):
		return GCP, strings.TrimPrefix(name, "@google-cloud/")
	case strings.HasPrefix(name, "@azure/"):
		return Azure, strings.TrimPrefix(name, "@azure/")
	}
	return "", ""
}

func pypi(name string) (string, string) {
	switch {
	case strings.HasPrefix(name, "google-cloud-"):
		return GCP, strings.TrimPrefix(name, "google-cloud-")
	case strings.HasPrefix(name, "azure-mgmt-"):
		return Azure, strings.TrimPrefix(name, "azure-mgmt-")
	case strings.HasPrefix(name, "azure-"):
		return Azure, strings.TrimPrefix(name, "azure-")
	case strings.HasPrefix(name, "types-aioboto3-"), strings.HasPrefix(name, "aioboto3-"):
		return "", ""
	case strings.HasPrefix(name, "mypy-boto3-"):
		return AWS, strings.TrimPrefix(name, "mypy-boto3-")
	}
	return "", ""
}

var goAWS = regexp.MustCompile(`^github\.com/aws/aws-sdk-go(?:-v2)?/service/([^/]+)`)

func golang(name string) (string, string) {
	if m := goAWS.FindStringSubmatch(name); m != nil {
		return AWS, m[1]
	}
	if rest, ok := strings.CutPrefix(name, "cloud.google.com/go/"); ok {
		// cloud.google.com/go/pubsub/v2 -> pubsub
		return GCP, strings.SplitN(rest, "/", 2)[0]
	}
	if rest, ok := strings.CutPrefix(name, "github.com/Azure/azure-sdk-for-go/sdk/"); ok {
		// .../sdk/messaging/azservicebus -> servicebus; .../sdk/azcore -> shared
		parts := strings.Split(rest, "/")
		last := parts[len(parts)-1]
		if len(parts) > 1 && isVersionSuffix(last) {
			last = parts[len(parts)-2]
		}
		return Azure, strings.TrimPrefix(last, "az")
	}
	return "", ""
}

func isVersionSuffix(s string) bool {
	return len(s) > 1 && s[0] == 'v' && strings.Trim(s[1:], "0123456789") == ""
}

func maven(name string) (string, string) {
	group, artifact, ok := strings.Cut(name, ":")
	if !ok {
		return "", ""
	}
	switch group {
	case "software.amazon.awssdk":
		return AWS, artifact
	case "com.amazonaws":
		if rest, ok := strings.CutPrefix(artifact, "aws-java-sdk-"); ok {
			return AWS, rest
		}
	case "com.google.cloud":
		if rest, ok := strings.CutPrefix(artifact, "google-cloud-"); ok {
			return GCP, rest
		}
	case "com.azure":
		if rest, ok := strings.CutPrefix(artifact, "azure-"); ok {
			return Azure, rest
		}
	case "com.azure.resourcemanager":
		if rest, ok := strings.CutPrefix(artifact, "azure-resourcemanager-"); ok {
			return Azure, rest
		}
	}
	return "", ""
}

func nuget(name string) (string, string) {
	switch {
	case strings.HasPrefix(name, "AWSSDK."):
		return AWS, strings.TrimPrefix(name, "AWSSDK.")
	case strings.HasPrefix(name, "Google.Cloud."):
		// Google.Cloud.Storage.V1 -> Storage
		rest := strings.TrimPrefix(name, "Google.Cloud.")
		parts := strings.Split(rest, ".")
		if n := len(parts); n > 1 && isVersionSuffix(strings.ToLower(parts[n-1])) {
			parts = parts[:n-1]
		}
		return GCP, strings.Join(parts, ".")
	case strings.HasPrefix(name, "Azure.ResourceManager."):
		return Azure, strings.TrimPrefix(name, "Azure.ResourceManager.")
	case strings.HasPrefix(name, "Azure."):
		return Azure, strings.TrimPrefix(name, "Azure.")
	}
	return "", ""
}

func rubygems(name string) (string, string) {
	switch {
	case strings.HasPrefix(name, "aws-sdk-"):
		return AWS, strings.TrimPrefix(name, "aws-sdk-")
	case strings.HasPrefix(name, "google-cloud-"):
		return GCP, strings.TrimPrefix(name, "google-cloud-")
	case strings.HasPrefix(name, "azure-"):
		return Azure, strings.TrimPrefix(name, "azure-")
	}
	return "", ""
}

func composer(name string) (string, string) {
	switch {
	case strings.HasPrefix(name, "google/cloud-"):
		return GCP, strings.TrimPrefix(name, "google/cloud-")
	case strings.HasPrefix(name, "microsoft/azure-"):
		return Azure, strings.TrimPrefix(name, "microsoft/azure-")
	}
	return "", ""
}

func cargo(name string) (string, string) {
	switch {
	case strings.HasPrefix(name, "aws-sdk-"):
		return AWS, strings.TrimPrefix(name, "aws-sdk-")
	case strings.HasPrefix(name, "google-cloud-"):
		return GCP, strings.TrimPrefix(name, "google-cloud-")
	case strings.HasPrefix(name, "azure_"):
		return Azure, strings.TrimPrefix(name, "azure_")
	}
	return "", ""
}

// BotoClient matches a boto3 client/resource call; group 1 is the service
// name ('boto3.client("dynamodb")' -> "dynamodb").
var BotoClient = regexp.MustCompile(`\b(?:client|resource)\(\s*(?:service_name\s*=\s*)?["']([a-z0-9-]+)["']`)
