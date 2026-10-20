package pulumi

import (
	"strings"
	"unicode"
)

// providerPrefix maps a Pulumi provider package to the prefix of the
// Terraform provider it is bridged from. Other providers (kubernetes,
// azure-native, ...) are not mapped.
var providerPrefix = map[string]string{"aws": "aws", "gcp": "google", "azure": "azurerm"}

// overrides maps "provider:module.Class" to the Terraform type when it does
// not follow the <prefix>_<module>_<class> rule.
var overrides = map[string]string{
	"aws:s3.BucketObjectv2":         "aws_s3_object",
	"aws:lb.LoadBalancer":           "aws_lb",
	"aws:alb.LoadBalancer":          "aws_alb",
	"aws:rds.Instance":              "aws_db_instance",
	"aws:rds.SubnetGroup":           "aws_db_subnet_group",
	"aws:rds.ParameterGroup":        "aws_db_parameter_group",
	"aws:rds.OptionGroup":           "aws_db_option_group",
	"aws:rds.Proxy":                 "aws_db_proxy",
	"aws:rds.EventSubscription":     "aws_db_event_subscription",
	"aws:rds.Snapshot":              "aws_db_snapshot",
	"gcp:serviceaccount.Account":    "google_service_account",
	"gcp:cloudrun.Service":          "google_cloud_run_service",
	"gcp:cloudrunv2.Service":        "google_cloud_run_v2_service",
	"gcp:cloudfunctionsv2.Function": "google_cloudfunctions2_function",
	"gcp:organizations.Project":     "google_project",
}

// ec2Bare lists the ec2 module classes whose Terraform type has no ec2
// prefix (aws_instance, aws_vpc, ...).
var ec2Bare = set("Instance", "Vpc", "VpcEndpoint", "VpcPeeringConnection", "Subnet", "SecurityGroup",
	"SecurityGroupRule", "RouteTable", "RouteTableAssociation", "Route", "InternetGateway", "NatGateway",
	"Eip", "EipAssociation", "KeyPair", "LaunchTemplate", "NetworkInterface", "NetworkAcl", "NetworkAclRule",
	"VpnGateway", "CustomerGateway", "Ami", "SpotInstanceRequest", "PlacementGroup", "DefaultVpc",
	"DefaultSecurityGroup", "DefaultSubnet")

// azureBare lists the azure (classic) modules that Terraform names without
// the module (azurerm_resource_group, azurerm_linux_virtual_machine).
var azureBare = set("core", "compute", "network", "appservice", "containerservice")

// moduleRename maps Pulumi module names to the module part of the Terraform
// type.
var moduleRename = map[string]string{"apigateway": "api_gateway"}

func set(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

// tfType derives the Terraform resource type of a Pulumi resource class.
// ok is false for providers that are not bridged from Terraform ones.
func tfType(provider, module, class string) (string, bool) {
	prefix, ok := providerPrefix[provider]
	if !ok || class == "" {
		return "", false
	}
	module = strings.ToLower(strings.TrimRight(module, "_"))
	if t, ok := overrides[provider+":"+module+"."+class]; ok {
		return t, true
	}
	class = strings.TrimSuffix(class, "V2") // BucketV2, BucketPolicyV2 ...
	snake := snakeCase(class)
	switch {
	case provider == "aws" && module == "ec2" && ec2Bare[class]:
		return prefix + "_" + snake, true
	case provider == "azure" && azureBare[module]:
		return prefix + "_" + snake, true
	case module == "":
		return prefix + "_" + snake, true
	}
	if m, ok := moduleRename[module]; ok {
		module = m
	}
	return prefix + "_" + module + "_" + snake, true
}

// snakeCase turns CamelCase into snake_case, keeping acronyms together
// ("DBInstance" -> "db_instance", "RolePolicyAttachment" -> "role_policy_attachment").
func snakeCase(s string) string {
	rs := []rune(s)
	var b strings.Builder
	for i, r := range rs {
		if unicode.IsUpper(r) && i > 0 {
			prevLower := unicode.IsLower(rs[i-1]) || unicode.IsDigit(rs[i-1])
			nextLower := i+1 < len(rs) && unicode.IsLower(rs[i+1])
			if prevLower || (unicode.IsUpper(rs[i-1]) && nextLower) {
				b.WriteByte('_')
			}
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}
