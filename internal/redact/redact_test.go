package redact

import (
	"reflect"
	"testing"

	"github.com/cloudowski/exitctl/internal/model"
)

// Fake secrets assembled at runtime so scanners do not flag this file.
var (
	fakeAWS = "AKIA" + "ABCDEFGHIJKLMNOP"
	fakeJWT = "eyJ" + "hbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0In0.c2lnbmF0dXJlX2hlcmU"
	fakePEM = "-----BEGIN " + "RSA PRIVATE KEY-----"
	fakeHex = "Zx9" + "Q2mP7vL0kR4tW8yB3nC6hF1jD5sG"
)

func TestSensitiveKey(t *testing.T) {
	yes := []string{
		"password", "master_password", "MasterUserPassword", "db_passwd", "client_secret",
		"secret", "secret_string", "token", "access_token", "private_key", "privateKey",
		"api_key", "apiKey", "APIKey", "access_key", "secret_key", "connection_string",
		"ConnectionString", "conn_str", "credentials", "sas", "sas_token",
		"spec.env.DB_PASSWORD", "env[0].SECRET", "dbpassword",
	}
	no := []string{
		"name", "kms_key_id", "secret_arn", "password_length", "token_id", "region",
		"tags.Name", "description", "", "sasl_mechanism", "tokenizer",
	}
	for _, k := range yes {
		if !SensitiveKey(k) {
			t.Errorf("SensitiveKey(%q) = false, want true", k)
		}
	}
	for _, k := range no {
		if SensitiveKey(k) {
			t.Errorf("SensitiveKey(%q) = true, want false", k)
		}
	}
}

func TestValue(t *testing.T) {
	cases := []struct {
		name string
		key  string
		in   any
		want any
	}{
		{"sensitive key", "master_password", "hunter2", model.Redacted},
		{"empty sensitive", "password", "", ""},
		{"plain", "name", "orders", "orders"},
		{"aws key value", "user_data", "export K=" + fakeAWS, model.Redacted},
		{"jwt value", "header", "Bearer " + fakeJWT, model.Redacted},
		{"pem value", "content", fakePEM + "\nMIIE...", model.Redacted},
		{"high entropy", "value", fakeHex, model.Redacted},
		{"bool kept", "password_enabled", true, true},
		{"number kept", "password", 12, 12},
		{"nil kept", "password", nil, nil},
		{"unknown kept", "password", model.Unknown, model.Unknown},
		{"arn kept", "role", "arn:aws:iam::123456789012:role/my-very-long-role-name-prod", "arn:aws:iam::123456789012:role/my-very-long-role-name-prod"},
		{"identifier kept", "name", "aws_dynamodb_table_orders_production_v2", "aws_dynamodb_table_orders_production_v2"},
		{"git sha kept", "uses", "actions/checkout@8f4b7f84864484a7bf31766abe9204da3cbe65b3", "actions/checkout@8f4b7f84864484a7bf31766abe9204da3cbe65b3"},
		{"uuid kept", "id", "123e4567-e89b-12d3-a456-426614174000", "123e4567-e89b-12d3-a456-426614174000"},
		{
			"nested map", "environment",
			map[string]any{"DB_PASSWORD": "x", "REGION": "eu-west-1", "list": []any{"a", fakeAWS}},
			map[string]any{"DB_PASSWORD": model.Redacted, "REGION": "eu-west-1", "list": []any{"a", model.Redacted}},
		},
		{
			"sensitive parent", "credentials",
			map[string]any{"user": "bob", "n": 1},
			map[string]any{"user": model.Redacted, "n": 1},
		},
		{"string list", "secrets", []string{"a", "b"}, []any{model.Redacted, model.Redacted}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Value(c.key, c.in)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("Value(%q, %v) = %#v, want %#v", c.key, c.in, got, c.want)
			}
		})
	}
}

func TestLine(t *testing.T) {
	cases := []struct{ in, want string }{
		{`resource "aws_sqs_queue" "q" {`, `resource "aws_sqs_queue" "q" {`},
		{`  access_key = "` + fakeAWS + `"`, `  access_key = "***"`},
		{`Authorization: Bearer ` + fakeJWT, `Authorization: Bearer ***`},
		{fakePEM, `***`},
		{`token: ` + fakeHex, `token: ***`},
		{`value = "` + fakeHex + `"`, `value = "***"`},
		{`  master_password = "hunter2"`, `  master_password = "***"`},
		{`password: hunter2`, `password: ***`},
		{`"DbPassword": "s3cret",`, `"DbPassword": "***",`},
		{`  password = var.db_password`, `  password = var.db_password`},
		{`  password = "${var.db_password}"`, `  password = "${var.db_password}"`},
		{`  manage_master_user_password = true`, `  manage_master_user_password = true`},
		{`  secret_arn = "arn:aws:secretsmanager:eu-west-1:1:secret:x"`, `  secret_arn = "arn:aws:secretsmanager:eu-west-1:1:secret:x"`},
		{`uses: actions/checkout@8f4b7f84864484a7bf31766abe9204da3cbe65b3`, `uses: actions/checkout@8f4b7f84864484a7bf31766abe9204da3cbe65b3`},
		{`image: 123456789012.dkr.ecr.eu-west-1.amazonaws.com/app:1.2.3`, `image: 123456789012.dkr.ecr.eu-west-1.amazonaws.com/app:1.2.3`},
	}
	for _, c := range cases {
		if got := Line(c.in); got != c.want {
			t.Errorf("Line(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

func TestResourceValueSecretBearing(t *testing.T) {
	if !model.IsRedacted(ResourceValue("aws_ssm_parameter", "value", "hunter2")) {
		t.Fatal("ssm parameter value must be redacted")
	}
	if !model.IsRedacted(ResourceValue("AWS::SecretsManager::Secret", "SecretString", "x")) {
		t.Fatal("CFN secret string must be redacted")
	}
	if ResourceValue("aws_sqs_queue", "name", "events") != "events" {
		t.Fatal("normal attributes must be kept")
	}
	if got := ResourceLine("azurerm_key_vault_secret", `  value = "hunter2"`); got != `  value = "***"` {
		t.Fatalf("got %q", got)
	}
}
