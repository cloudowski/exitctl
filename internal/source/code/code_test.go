package code

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudowski/exitctl/internal/catalog"
	"github.com/cloudowski/exitctl/internal/model"
	"github.com/cloudowski/exitctl/internal/source"
	"github.com/cloudowski/exitctl/internal/source/packages"
	"github.com/cloudowski/exitctl/internal/testutil"
	"github.com/cloudowski/exitctl/internal/walk"
)

const repo = "github.com/acme/shop"

func verified() catalog.Provenance {
	return catalog.Provenance{Origin: "maintainer", Author: "test", ReviewedBy: "test", ReviewedOn: "2026-10-06", Status: catalog.StatusVerified}
}

func testIndex() *catalog.Index {
	cat := &catalog.Catalog{Services: []catalog.Service{
		{
			ID: "aws.dynamodb", Provider: "aws", Name: "Amazon DynamoDB", Category: "database",
			SDK: []catalog.SdkPattern{
				{Ecosystem: "npm", Packages: []string{"@aws-sdk/client-dynamodb"}, Imports: []string{`["']@aws-sdk/client-dynamodb["']`}},
				{Ecosystem: "pypi", Usage: []string{`boto3\.(client|resource)\(\s*["']dynamodb["']`}, Gate: []string{"boto3"}},
			},
			Provenance: verified(),
		},
		{
			ID: "aws.s3", Provider: "aws", Name: "Amazon S3", Category: "storage",
			SDK: []catalog.SdkPattern{
				{Ecosystem: "npm", Packages: []string{"@aws-sdk/client-s3"}, Imports: []string{`["']@aws-sdk/client-s3["']`}},
				{Ecosystem: "pypi", Usage: []string{`boto3\.(client|resource)\(\s*["']s3["']`}, Gate: []string{"boto3"}},
			},
			Provenance: verified(),
		},
		{
			ID: "aws.sqs", Provider: "aws", Name: "Amazon SQS", Category: "messaging",
			SDK: []catalog.SdkPattern{
				{Ecosystem: "pypi", Usage: []string{`boto3\.(client|resource)\(\s*["']sqs["']`}, Gate: []string{"boto3"}},
			},
			Provenance: verified(),
		},
	}}
	return catalog.NewIndex(cat, catalog.IndexOptions{})
}

func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// run walks root and runs the packages and code detectors like source.RunAll.
func run(t *testing.T, root string) model.Facts {
	t.Helper()
	idx, err := walk.Walk(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	opts := source.Options{Repo: repo, Root: root, Catalog: testIndex()}
	return source.RunAll(context.Background(), []source.Detector{packages.NewWith(nil)}, []source.Detector{New()}, idx, opts)
}

type want struct {
	comp, kind, service, match, path string
	line                             int
}

func check(t *testing.T, got []model.CodeUse, ws []want) {
	t.Helper()
	if len(got) != len(ws) {
		for _, u := range got {
			t.Logf("%s %s %s %q %s:%d %q", u.Component, u.PatternKind, u.Service, u.Match, u.Evidence.Path, u.Evidence.Line, u.Evidence.Excerpt)
		}
		t.Fatalf("got %d code uses, want %d", len(got), len(ws))
	}
	for i, w := range ws {
		u := got[i]
		if u.Component != w.comp || u.PatternKind != w.kind || u.Service != w.service || u.Match != w.match ||
			u.Evidence.Path != w.path || u.Evidence.Line != w.line || u.Evidence.Repo != repo {
			t.Errorf("use %d = %s %s %s %q %s:%d, want %+v", i, u.Component, u.PatternKind, u.Service, u.Match, u.Evidence.Path, u.Evidence.Line, w)
		}
		if u.Evidence.Excerpt == "" {
			t.Errorf("use %d has empty excerpt", i)
		}
	}
}

func TestDetectCodeUses(t *testing.T) {
	root := writeFiles(t, map[string]string{
		"web/package.json": `{
  "dependencies": {
    "@aws-sdk/client-dynamodb": "^3.0.0"
  }
}
`,
		"web/src/a.ts": `import { DynamoDBClient } from "@aws-sdk/client-dynamodb";
export const c = new DynamoDBClient({});
`,
		"web/src/b.ts": `// import { X } from '@aws-sdk/client-dynamodb';
/*
 * import { Y } from '@aws-sdk/client-dynamodb';
 */
const z = 1;
const { QueryCommand } = require('@aws-sdk/client-dynamodb');
`,
		"web/src/notes.py": "import '@aws-sdk/client-dynamodb'\n", // wrong language for npm
		// Nested component: its files belong to it, not to web.
		"web/tools/package.json": `{ "dependencies": { "left-pad": "1.0.0" } }`,
		"web/tools/x.js":         "import '@aws-sdk/client-dynamodb';\n",

		"py/requirements.txt": "boto3==1.35.0\nflask\n",
		"py/app/repo.py": `import boto3

ddb = boto3.client("dynamodb")
s3 = boto3.resource('s3')
# boto3.client("sqs")
`,
		// Service-specific package never imported: fallback at the manifest.
		"s3only/package.json": `{
  "dependencies": {
    "@aws-sdk/client-s3": "3.1.0"
  }
}
`,
		"s3only/index.js": "console.log('hi');\n",
		// Gate package alone, no usage: nothing.
		"gateonly/requirements.txt": "boto3\n",
		"gateonly/main.py":          "import boto3\n",
	})
	f := run(t, root)
	check(t, f.CodeUses, []want{
		{repo + "//py#component", "usage", "aws.dynamodb", `boto3.client("dynamodb"`, "py/app/repo.py", 3},
		{repo + "//py#component", "usage", "aws.s3", `boto3.resource('s3'`, "py/app/repo.py", 4},
		{repo + "//s3only#component", "import", "aws.s3", "@aws-sdk/client-s3", "s3only/package.json", 3},
		{repo + "//web#component", "import", "aws.dynamodb", `"@aws-sdk/client-dynamodb"`, "web/src/a.ts", 1},
		{repo + "//web#component", "import", "aws.dynamodb", `'@aws-sdk/client-dynamodb'`, "web/src/b.ts", 6},
	})
	for _, u := range f.CodeUses {
		if u.Evidence.Path == "s3only/package.json" {
			if u.Evidence.SourceKind != model.KindPackageManifest {
				t.Errorf("fallback evidence kind %s", u.Evidence.SourceKind)
			}
		} else if u.Evidence.SourceKind != model.KindCode {
			t.Errorf("code evidence kind %s", u.Evidence.SourceKind)
		}
	}
}

func TestAppOnlyFixture(t *testing.T) {
	root := filepath.Join(testutil.RepoRoot(t), "testdata", "fixtures", "app-only")
	f := run(t, root)
	check(t, f.CodeUses, []want{
		{repo + "//#component", "usage", "aws.dynamodb", `boto3.client("dynamodb"`, "app/repo.py", 3},
		{repo + "//#component", "usage", "aws.s3", `boto3.resource('s3'`, "app/repo.py", 4},
	})
	if f.CodeUses[0].Evidence.Excerpt != `ddb = boto3.client("dynamodb")` {
		t.Errorf("excerpt %q", f.CodeUses[0].Evidence.Excerpt)
	}
}

func TestRedactsAndBoundsLines(t *testing.T) {
	long := "x = '" + strings.Repeat("a", 5000) + "' + boto3.client('dynamodb')\n"
	root := writeFiles(t, map[string]string{
		"requirements.txt": "boto3\n",
		"a.py": long + `boto3.client("dynamodb", aws_secret_access_key="AKIAIOSFODNN7EXAMPLE")
`,
	})
	f := run(t, root)
	if len(f.CodeUses) != 1 || f.CodeUses[0].Evidence.Line != 2 {
		t.Fatalf("uses: %+v", f.CodeUses)
	}
	if strings.Contains(f.CodeUses[0].Evidence.Excerpt, "AKIAIOSFODNN7EXAMPLE") {
		t.Errorf("excerpt not redacted: %q", f.CodeUses[0].Evidence.Excerpt)
	}
}

func TestNoCatalogOrPackages(t *testing.T) {
	root := writeFiles(t, map[string]string{"a.py": "boto3.client('dynamodb')\n"})
	idx, err := walk.Walk(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	f, err := New().Detect(context.Background(), idx, source.Options{Repo: repo, Root: root, Catalog: testIndex()})
	if err != nil || len(f.CodeUses) != 0 {
		t.Errorf("want nothing without packages: %+v %v", f.CodeUses, err)
	}
	f, err = New().Detect(context.Background(), idx, source.Options{Repo: repo, Root: root,
		Packages: []model.PackageUse{{Component: repo + "//#component", Ecosystem: model.EcoPyPI, Name: "boto3"}}})
	if err != nil || len(f.CodeUses) != 0 {
		t.Errorf("want nothing without catalog: %+v %v", f.CodeUses, err)
	}
}

func TestGlobMatch(t *testing.T) {
	for _, tc := range []struct {
		pat, name string
		want      bool
	}{
		{"@aws-sdk/client-*", "@aws-sdk/client-s3", true},
		{"boto3", "boto3", true},
		{"google-cloud-*", "google-cloud-storage", true},
		{"boto3", "boto3-stubs", false},
		{"[", "[", true}, // invalid glob, exact match
	} {
		if got := globMatch(tc.pat, tc.name); got != tc.want {
			t.Errorf("globMatch(%q, %q) = %v", tc.pat, tc.name, got)
		}
	}
}

func newLangIndex() *catalog.Index {
	cat := &catalog.Catalog{Services: []catalog.Service{{
		ID: "aws.sqs", Provider: "aws", Name: "Amazon SQS", Category: "messaging",
		SDK: []catalog.SdkPattern{
			{Ecosystem: "rubygems", Packages: []string{"aws-sdk-sqs"}, Imports: []string{`^\s*require\s+["']aws-sdk-sqs["']`}},
			{Ecosystem: "composer", Packages: []string{"aws/aws-sdk-php"}, Imports: []string{`\bAws\\Sqs\\SqsClient\b`}},
			{Ecosystem: "cargo", Packages: []string{"aws-sdk-sqs"}, Imports: []string{`\baws_sdk_sqs::`}},
			{Ecosystem: "maven", Packages: []string{"software.amazon.awssdk:sqs"}, Imports: []string{`^\s*import\s+software\.amazon\.awssdk\.services\.sqs\.`}},
		},
		Provenance: verified(),
	}}}
	return catalog.NewIndex(cat, catalog.IndexOptions{})
}

func TestDetectNewLanguages(t *testing.T) {
	root := writeFiles(t, map[string]string{
		"rb/Gemfile": "gem 'aws-sdk-sqs'\n",
		"rb/app.rb": `=begin
require 'aws-sdk-sqs'
=end
# require 'aws-sdk-sqs'
require 'aws-sdk-sqs'
`,
		"php/composer.json": `{"require": {"aws/aws-sdk-php": "^3"}}`,
		"php/q.php": `<?php
// use Aws\Sqs\SqsClient;
# use Aws\Sqs\SqsClient;
/* use Aws\Sqs\SqsClient; */
use Aws\Sqs\SqsClient;
`,
		"rs/Cargo.toml": "[dependencies]\naws-sdk-sqs = \"1\"\n",
		"rs/src/main.rs": `// use aws_sdk_sqs::Client;
use aws_sdk_sqs::Client;
`,
		"sc/build.sbt": `libraryDependencies += "software.amazon.awssdk" % "sqs" % "2.25.0"` + "\n",
		"sc/src/Q.scala": `// import software.amazon.awssdk.services.sqs.SqsClient
import software.amazon.awssdk.services.sqs.SqsClient
`,
	})
	idx, err := walk.Walk(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	opts := source.Options{Repo: repo, Root: root, Catalog: newLangIndex()}
	f := source.RunAll(context.Background(), []source.Detector{packages.NewWith(nil)}, []source.Detector{New()}, idx, opts)

	lines := map[string]int{}
	for _, u := range f.CodeUses {
		if u.Service != "aws.sqs" {
			t.Errorf("unexpected service %q", u.Service)
		}
		lines[u.Evidence.Path] = u.Evidence.Line
	}
	for path, line := range map[string]int{"rb/app.rb": 5, "php/q.php": 5, "rs/src/main.rs": 2, "sc/src/Q.scala": 2} {
		if lines[path] != line {
			t.Errorf("%s: hit at line %d, want %d (all: %v)", path, lines[path], line, lines)
		}
	}
	if len(f.CodeUses) != 4 {
		t.Errorf("got %d code uses, want 4: %+v", len(f.CodeUses), f.CodeUses)
	}
}

func TestDetectBotoServiceNames(t *testing.T) {
	root := writeFiles(t, map[string]string{
		"py/requirements.txt": "boto3==1.35.0\n",
		"py/app.py": `import boto3
a = boto3.client("kinesis")
b = boto3.client("dynamodb")
# c = boto3.client("glue")
`,
	})
	f := run(t, root)
	var names []string
	for _, u := range f.CodeUses {
		switch u.PatternKind {
		case model.CodeKindSDKName:
			names = append(names, u.Match)
			if u.Service != "" {
				t.Errorf("sdk-name use has service %q", u.Service)
			}
		}
	}
	// dynamodb is mapped by a catalog pattern on its line and not repeated.
	if len(names) != 1 || names[0] != "kinesis" {
		t.Errorf("sdk-name matches = %v, want [kinesis]", names)
	}
}
