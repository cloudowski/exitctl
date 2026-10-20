package packages

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/cloudowski/exitctl/internal/model"
)

// pkgSet renders parsed packages as sorted "eco name@version" strings.
func pkgSet(ps []Pkg) []string {
	var out []string
	for _, p := range ps {
		out = append(out, fmt.Sprintf("%s %s@%s", p.Ecosystem, p.Name, p.Version))
	}
	sort.Strings(out)
	return out
}

func wantPkgs(t *testing.T, label string, ps []Pkg, err error, want ...string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", label, err)
	}
	got := pkgSet(ps)
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("%s:\n got  %v\n want %v", label, got, want)
	}
}

func TestParseNPMLock(t *testing.T) {
	v3 := `{"lockfileVersion": 3, "packages": {
	  "": {"name": "app", "dependencies": {"@aws-sdk/client-s3": "^3.0.0"}},
	  "node_modules/@aws-sdk/client-s3": {"version": "3.500.0"},
	  "node_modules/left-pad": {"version": "1.3.0"},
	  "node_modules/left-pad/node_modules/@aws-sdk/client-s3": {"version": "3.100.0"},
	  "packages/lib": {"version": "1.0.0"},
	  "node_modules/lib": {"link": true, "resolved": "packages/lib"}
	}}`
	ps, err := parseNPMLock("", []byte(v3))
	wantPkgs(t, "v3", ps, err, "npm @aws-sdk/client-s3@3.500.0", "npm left-pad@1.3.0")

	v1 := `{"lockfileVersion": 1, "dependencies": {
	  "aws-sdk": {"version": "2.1.0", "dependencies": {"uuid": {"version": "3.0.0"}}},
	  "uuid": {"version": "8.3.2"}
	}}`
	ps, err = parseNPMLock("", []byte(v1))
	wantPkgs(t, "v1", ps, err, "npm aws-sdk@2.1.0", "npm uuid@8.3.2")
}

func TestParseYarnLock(t *testing.T) {
	classic := `# yarn lockfile v1


"@aws-sdk/client-s3@^3.0.0", "@aws-sdk/client-s3@^3.1.0":
  version "3.500.0"
  resolved "https://registry.yarnpkg.com/x"

lodash@^4.17.0:
  version "4.17.21"
`
	ps, err := parseYarnLock("", []byte(classic))
	wantPkgs(t, "classic", ps, err, "npm @aws-sdk/client-s3@3.500.0", "npm lodash@4.17.21")
	if ps[0].Line == 0 {
		t.Error("yarn entries should carry their line")
	}

	berry := `__metadata:
  version: 8

"@google-cloud/pubsub@npm:^4.0.0":
  version: 4.1.0
  resolution: "@google-cloud/pubsub@npm:4.1.0"

"app@workspace:.":
  version: 0.0.0-use.local
`
	ps, err = parseYarnLock("", []byte(berry))
	wantPkgs(t, "berry", ps, err, "npm @google-cloud/pubsub@4.1.0", "npm app@0.0.0-use.local")
}

func TestParsePnpmLock(t *testing.T) {
	for _, tc := range []struct{ label, doc string }{
		{"v5", "lockfileVersion: 5.4\npackages:\n  /@azure/storage-blob/12.17.0:\n    resolution: {}\n  /tslib/2.6.2_typescript@5.0.0:\n    resolution: {}\n"},
		{"v6", "lockfileVersion: '6.0'\npackages:\n  /@azure/storage-blob@12.17.0:\n    resolution: {}\n  /tslib@2.6.2(typescript@5.0.0):\n    resolution: {}\n"},
		{"v9", "lockfileVersion: '9.0'\npackages:\n  '@azure/storage-blob@12.17.0':\n    resolution: {}\n  tslib@2.6.2:\n    resolution: {}\n"},
	} {
		ps, err := parsePnpmLock("", []byte(tc.doc))
		wantPkgs(t, tc.label, ps, err, "npm @azure/storage-blob@12.17.0", "npm tslib@2.6.2")
	}
}

func TestParseBunLock(t *testing.T) {
	doc := `{
  // bun lockfile
  "lockfileVersion": 1,
  "workspaces": { "": { "name": "app", }, },
  "packages": {
    "@aws-sdk/client-sqs": ["@aws-sdk/client-sqs@3.600.0", "", {}, "sha512-x"],
    "app": ["app@workspace:."],
    "zod": ["zod@3.23.8", "", {}, "sha512-y"],
  },
}`
	ps, err := parseBunLock("", []byte(doc))
	wantPkgs(t, "bun", ps, err, "npm @aws-sdk/client-sqs@3.600.0", "npm zod@3.23.8")
}

func TestJSONCKeepsStrings(t *testing.T) {
	got := string(jsonc([]byte(`{"a": "http://x/*y*/,]", /* c */ "b": [1,], // d
}`)))
	if !strings.Contains(got, `"http://x/*y*/,]"`) || strings.Contains(got, "c */") || strings.Contains(got, "1,]") {
		t.Fatalf("jsonc = %q", got)
	}
}

func TestParsePythonLocks(t *testing.T) {
	pipfile := `{"_meta": {}, "default": {"boto3": {"version": "==1.34.0"}, "Google_Cloud-Storage": {"version": "==2.14.0"}},
	  "develop": {"pytest": {"version": "==8.0.0"}}}`
	ps, err := parsePipfileLock("", []byte(pipfile))
	wantPkgs(t, "Pipfile.lock", ps, err, "pypi boto3@1.34.0", "pypi google-cloud-storage@2.14.0", "pypi pytest@8.0.0")

	uv := "version = 1\n\n[[package]]\nname = \"boto3\"\nversion = \"1.35.0\"\nsource = { registry = \"https://pypi.org/simple\" }\n\n[[package]]\nname = \"Azure_Identity\"\nversion = \"1.17.0\"\n"
	parse := lockParserFor("uv.lock")
	ps, err = parse("", []byte(uv))
	wantPkgs(t, "uv.lock", ps, err, "pypi boto3@1.35.0", "pypi azure-identity@1.17.0")
	if lockParserFor("pdm.lock") == nil {
		t.Error("pdm.lock has no parser")
	}
}

func TestParseGoSumNamesOnly(t *testing.T) {
	sum := "github.com/aws/aws-sdk-go-v2 v1.24.0 h1:abc=\ngithub.com/aws/aws-sdk-go-v2 v1.24.0/go.mod h1:def=\ngithub.com/aws/aws-sdk-go-v2 v1.20.0/go.mod h1:ghi=\n"
	ps, err := parseGoSum("", []byte(sum))
	if err != nil || len(ps) != 3 || ps[0].Name != "github.com/aws/aws-sdk-go-v2" || ps[0].Version != "" {
		t.Fatalf("go.sum = %+v %v (versions must come from go.mod)", ps, err)
	}
}

func TestParseGradleAndDeps(t *testing.T) {
	gradle := "# This is a Gradle generated file\ncom.amazonaws:aws-java-sdk-s3:1.12.600=compileClasspath,runtimeClasspath\nempty=annotationProcessor\n"
	ps, err := parseGradleLock("", []byte(gradle))
	wantPkgs(t, "gradle", ps, err, "maven com.amazonaws:aws-java-sdk-s3@1.12.600")

	deps := `{"libraries": {"AWSSDK.S3/3.7.300": {"type": "package"}, "MyApp/1.0.0": {"type": "project"}}}`
	ps, err = parseDepsJSON("", []byte(deps))
	wantPkgs(t, "deps.json", ps, err, "nuget AWSSDK.S3@3.7.300")
}

func TestParseSBOMFormats(t *testing.T) {
	want := []string{"npm @aws-sdk/client-s3@3.500.0", "pypi google-cloud-pubsub@2.0.0", "maven com.azure:azure-storage-blob@12.25.0"}
	for _, tc := range []struct{ label, doc string }{
		{"cyclonedx json", `{"bomFormat": "CycloneDX", "components": [
  {"name": "client-s3", "purl": "pkg:npm/%40aws-sdk/client-s3@3.500.0"},
  {"name": "google-cloud-pubsub", "purl": "pkg:pypi/google_cloud_pubsub@2.0.0"},
  {"name": "azure-storage-blob", "purl": "pkg:maven/com.azure/azure-storage-blob@12.25.0?type=jar"}]}`},
		{"cyclonedx xml", `<bom xmlns="http://cyclonedx.org/schema/bom/1.5"><components>
  <component type="library"><purl>pkg:npm/%40aws-sdk/client-s3@3.500.0</purl></component>
  <component type="library"><purl>pkg:pypi/google-cloud-pubsub@2.0.0</purl></component>
  <component type="library"><purl>pkg:maven/com.azure/azure-storage-blob@12.25.0</purl></component>
</components></bom>`},
		{"spdx json", `{"spdxVersion": "SPDX-2.3", "packages": [
  {"name": "x", "externalRefs": [{"referenceType": "purl", "referenceLocator": "pkg:npm/%40aws-sdk/client-s3@3.500.0"}]},
  {"name": "y", "externalRefs": [{"referenceType": "purl", "referenceLocator": "pkg:pypi/google-cloud-pubsub@2.0.0"}]},
  {"name": "z", "externalRefs": [{"referenceType": "purl", "referenceLocator": "pkg:maven/com.azure/azure-storage-blob@12.25.0"}]}]}`},
		{"spdx tag-value", "SPDXVersion: SPDX-2.3\nPackageName: x\nExternalRef: PACKAGE-MANAGER purl pkg:npm/%40aws-sdk/client-s3@3.500.0\n" +
			"ExternalRef: PACKAGE-MANAGER purl pkg:pypi/google-cloud-pubsub@2.0.0\nExternalRef: PACKAGE-MANAGER purl pkg:maven/com.azure/azure-storage-blob@12.25.0\n"},
		{"syft json", `{"artifacts": [{"purl": "pkg:npm/%40aws-sdk/client-s3@3.500.0"}, {"purl": "pkg:pypi/google-cloud-pubsub@2.0.0"},
  {"purl": "pkg:maven/com.azure/azure-storage-blob@12.25.0"}, {"purl": "pkg:deb/debian/libc6@2.36"}]}`},
	} {
		ps, err := parseSBOM("", []byte(tc.doc))
		wantPkgs(t, tc.label, dedupPkgs(ps), err, want...)
	}
}

func TestPURLVersion(t *testing.T) {
	for purl, want := range map[string]string{
		"pkg:npm/%40aws-sdk/client-s3@3.500.0":   "3.500.0",
		"pkg:maven/a/b@1.0?type=jar":             "1.0",
		"pkg:golang/github.com/x/y@v1.2.3#sub":   "v1.2.3",
		"pkg:npm/left-pad":                       "",
		"pkg:npm/%40scope/name@1.0.0-beta%2B001": "1.0.0-beta+001",
	} {
		if got := purlVersion(purl); got != want {
			t.Errorf("purlVersion(%s) = %q, want %q", purl, got, want)
		}
	}
}

func TestParseSetupPy(t *testing.T) {
	src := "from setuptools import setup\nsetup(\n    name=\"app\",\n    install_requires=[\n        \"boto3>=1.34\",\n        'google-cloud-storage==2.14.0',\n    ],\n)\n"
	ps, err := parseSetupPy("", []byte(src))
	wantPkgs(t, "setup.py", ps, err, "pypi boto3@", "pypi google-cloud-storage@2.14.0")
}

// TestLockfilesNearManifestsOnly: lockfiles count only next to a manifest
// (as before), SBOMs anywhere, and unreadable files become failed sources.
func TestLockfilesNearManifestsOnly(t *testing.T) {
	f := detect(t, New(), map[string]string{
		"svc/package.json":      `{"dependencies": {"@aws-sdk/client-sqs": "^3.0.0"}}`,
		"svc/package-lock.json": `{"lockfileVersion": 3, "packages": {"node_modules/@aws-sdk/client-sqs": {"version": "3.600.0"}}}`,
		"svc/yarn.lock":         "this is not a yarn lockfile {",
		"orphan/yarn.lock":      "lodash@^4.0.0:\n  version \"4.17.21\"\n",
		"broken/pnpm-lock.yaml": "",
		"broken/package.json":   `{"dependencies": {}}`,
		"sbom/app.spdx.json":    `{"spdxVersion": "SPDX-2.3", "packages": [{"externalRefs": [{"referenceLocator": "pkg:pypi/boto3@1.34.0"}]}]}`,
	})
	comps := map[string]bool{}
	for _, n := range f.RawNodes {
		comps[n.NativeAddress] = true
	}
	if comps["orphan"] || !comps["svc"] || !comps["sbom"] {
		t.Fatalf("components = %v", comps)
	}
	var sqs, boto *model.PackageUse
	for i := range f.PackageUses {
		switch f.PackageUses[i].Name {
		case "@aws-sdk/client-sqs":
			sqs = &f.PackageUses[i]
		case "boto3":
			boto = &f.PackageUses[i]
		case "lodash":
			t.Error("orphan lockfile was read")
		}
	}
	if sqs == nil || sqs.Version != "3.600.0" {
		t.Errorf("sqs = %+v (want the resolved lockfile version)", sqs)
	}
	if boto == nil || boto.Manifest.SourceKind != model.KindSBOM || boto.Manifest.Line != 1 {
		t.Errorf("boto3 from SBOM = %+v", boto)
	}
	for _, s := range f.Sources {
		if s.Path == "orphan/yarn.lock" {
			t.Error("orphan lockfile listed as a source")
		}
	}
	// The empty pnpm lockfile parses to nothing; it is neither a source nor a failure.
	if _, err := (LockReader{}).Packages(context.Background(), t.TempDir(), []string{"missing/yarn.lock"}); err == nil {
		t.Error("missing lockfile not reported")
	}
}
