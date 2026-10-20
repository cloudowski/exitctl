package config

import (
	"strings"
	"testing"
)

func TestExceptionsLoad(t *testing.T) {
	// An empty reason is not a configuration error: the exception is
	// reported as invalid and ignored when scanning (spec US7 scenario 3).
	cfg, err := Parse(".exitctl.yaml", []byte(`version: 1
exceptions:
  - id: keep-cognito
    select: {service: aws.cognito}
    reason: shared identity
    owner: platform
    expires: 2027-01-01
  - id: no-reason
    select: {resource: aws_sqs_queue.events, trait: proprietary-api}
    reason: ""
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Exceptions) != 2 || cfg.Exceptions[0].Expires != "2027-01-01" || cfg.Exceptions[1].Owner != "" {
		t.Fatalf("got %+v", cfg.Exceptions)
	}
}

func TestExceptionsRejected(t *testing.T) {
	cases := []struct{ name, doc, want string }{
		{"duplicate id", "version: 1\nexceptions:\n  - {id: a, select: {service: s}, reason: r, owner: o}\n  - {id: a, select: {trait: t}, reason: r, owner: o}\n", `id "a" appears more than once`},
		{"bad id", "version: 1\nexceptions:\n  - {id: Bad_ID, select: {service: s}, reason: r, owner: o}\n", "/exceptions/0/id"},
		{"two selectors", "version: 1\nexceptions:\n  - {id: a, select: {service: s, trait: t}, reason: r, owner: o}\n", "/exceptions/0/select"},
		{"bad date", "version: 1\nexceptions:\n  - {id: a, select: {service: s}, reason: r, owner: o, expires: 2026-13-01}\n", "/exceptions/0/expires"},
	}
	for _, c := range cases {
		_, err := Parse(".exitctl.yaml", []byte(c.doc))
		if err == nil {
			t.Errorf("%s: accepted", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %q does not mention %q", c.name, err, c.want)
		}
	}
}
