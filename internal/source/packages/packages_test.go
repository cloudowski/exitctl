package packages

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudowski/exitctl/internal/model"
	"github.com/cloudowski/exitctl/internal/source"
	"github.com/cloudowski/exitctl/internal/walk"
)

const repo = "github.com/acme/shop"

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

func detect(t *testing.T, d source.Detector, files map[string]string) model.Facts {
	t.Helper()
	root := writeFiles(t, files)
	idx, err := walk.Walk(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	f, err := d.Detect(context.Background(), idx, source.Options{Repo: repo, Root: root})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

const packageJSON = `{
  "name": "web",
  "version": "1.0.0",
  "dependencies": {
    "@aws-sdk/client-dynamodb": "^3.500.0",
    "express": "4.18.2"
  },
  "devDependencies": {
    "jest": "^29.0.0"
  }
}
`

const packageLock = `{
  "name": "web",
  "lockfileVersion": 3,
  "requires": true,
  "packages": {
    "": {
      "name": "web",
      "version": "1.0.0",
      "dependencies": {
        "@aws-sdk/client-dynamodb": "^3.500.0",
        "express": "4.18.2"
      }
    },
    "node_modules/@aws-sdk/client-dynamodb": {
      "version": "3.501.0",
      "resolved": "https://registry.npmjs.org/@aws-sdk/client-dynamodb/-/client-dynamodb-3.501.0.tgz"
    },
    "node_modules/express": {
      "version": "4.18.2",
      "resolved": "https://registry.npmjs.org/express/-/express-4.18.2.tgz"
    },
    "node_modules/left-pad": {
      "version": "1.3.0",
      "resolved": "https://registry.npmjs.org/left-pad/-/left-pad-1.3.0.tgz"
    }
  }
}
`

const requirements = `# deps
flask==3.0.0
boto3>=1.34  # aws
-r other.txt
Requests_OAuthlib
`

const goMod = `module github.com/acme/shop/worker

go 1.22

require (
	cloud.google.com/go/pubsub v1.36.0
	github.com/google/uuid v1.6.0 // indirect
)
`

const pomXML = `<?xml version="1.0"?>
<project>
  <properties>
    <sdk.version>2.20.0</sdk.version>
  </properties>
  <dependencies>
    <dependency>
      <groupId>software.amazon.awssdk</groupId>
      <artifactId>dynamodb</artifactId>
      <version>${sdk.version}</version>
    </dependency>
  </dependencies>
  <build>
    <plugins>
      <plugin>
        <artifactId>maven-surefire-plugin</artifactId>
        <dependencies>
          <dependency><groupId>x</groupId><artifactId>ignored</artifactId></dependency>
        </dependencies>
      </plugin>
    </plugins>
  </build>
</project>
`

const pyproject = `[project]
name = "api"
dependencies = [
  "google-cloud-storage>=2.0",
  "httpx",
]

[tool.poetry.dependencies]
python = "^3.11"
redis = "^5.0"
`

const csproj = `<Project Sdk="Microsoft.NET.Sdk">
  <ItemGroup>
    <PackageReference Include="Azure.Storage.Blobs" Version="12.19.0" />
    <PackageReference Include="Newtonsoft.Json">
      <Version>13.0.3</Version>
    </PackageReference>
  </ItemGroup>
</Project>
`

type wantUse struct {
	comp, eco, name, version, path string
	line                           int
}

func TestDetectComponentsAndPackageUses(t *testing.T) {
	files := map[string]string{
		"package.json":            packageJSON,
		"package-lock.json":       packageLock,
		"svc/py/requirements.txt": requirements,
		"svc/go/go.mod":           goMod,
		"svc/java/pom.xml":        pomXML,
		"svc/api/pyproject.toml":  pyproject,
		"svc/net/app.csproj":      csproj,
		"svc/net/Program.cs":      "class P {}\n",
	}
	for _, tc := range []struct {
		name string
		det  source.Detector
	}{
		{"lockfiles", New()},
		{"reader-only", NewWith(nil)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := detect(t, tc.det, files)

			wantComps := map[string]string{
				repo + "//#component":         ".",
				repo + "//svc/api#component":  "svc/api",
				repo + "//svc/go#component":   "svc/go",
				repo + "//svc/java#component": "svc/java",
				repo + "//svc/net#component":  "svc/net",
				repo + "//svc/py#component":   "svc/py",
			}
			if len(f.RawNodes) != len(wantComps) {
				t.Fatalf("got %d nodes, want %d: %+v", len(f.RawNodes), len(wantComps), f.RawNodes)
			}
			for _, n := range f.RawNodes {
				if wantComps[n.ID] != n.NativeAddress {
					t.Errorf("unexpected node %s (%s)", n.ID, n.NativeAddress)
				}
				if n.NodeKind != model.NodeComponent || n.Type != model.TypeComponent || n.SourceKind != model.KindPackageManifest {
					t.Errorf("node %s: kind/type %s/%s/%s", n.ID, n.NodeKind, n.Type, n.SourceKind)
				}
				if len(n.Evidence) != 1 || n.Evidence[0].Line != 1 || n.Evidence[0].Repo != repo {
					t.Errorf("node %s evidence %+v", n.ID, n.Evidence)
				}
			}
			root := repo + "//#component"
			lockVersion := "^3.500.0"
			if tc.name == "lockfiles" {
				lockVersion = "3.501.0" // resolved from package-lock.json
			}
			want := []wantUse{
				{root, "npm", "@aws-sdk/client-dynamodb", lockVersion, "package.json", 5},
				{root, "npm", "express", "4.18.2", "package.json", 6},
				{root, "npm", "jest", "^29.0.0", "package.json", 9},
				{repo + "//svc/api#component", "pypi", "google-cloud-storage", "", "svc/api/pyproject.toml", 4},
				{repo + "//svc/api#component", "pypi", "httpx", "", "svc/api/pyproject.toml", 5},
				{repo + "//svc/api#component", "pypi", "redis", "^5.0", "svc/api/pyproject.toml", 10},
				{repo + "//svc/go#component", "go", "cloud.google.com/go/pubsub", "v1.36.0", "svc/go/go.mod", 6},
				{repo + "//svc/go#component", "go", "github.com/google/uuid", "v1.6.0", "svc/go/go.mod", 7},
				{repo + "//svc/java#component", "maven", "software.amazon.awssdk:dynamodb", "2.20.0", "svc/java/pom.xml", 9},
				{repo + "//svc/net#component", "nuget", "Azure.Storage.Blobs", "12.19.0", "svc/net/app.csproj", 3},
				{repo + "//svc/net#component", "nuget", "Newtonsoft.Json", "13.0.3", "svc/net/app.csproj", 4},
				{repo + "//svc/py#component", "pypi", "boto3", "", "svc/py/requirements.txt", 3},
				{repo + "//svc/py#component", "pypi", "flask", "3.0.0", "svc/py/requirements.txt", 2},
				{repo + "//svc/py#component", "pypi", "requests-oauthlib", "", "svc/py/requirements.txt", 5},
			}
			if len(f.PackageUses) != len(want) {
				for _, u := range f.PackageUses {
					t.Logf("%s %s %s %s %s:%d", u.Component, u.Ecosystem, u.Name, u.Version, u.Manifest.Path, u.Manifest.Line)
				}
				t.Fatalf("got %d package uses, want %d", len(f.PackageUses), len(want))
			}
			for i, w := range want {
				u := f.PackageUses[i]
				if u.Component != w.comp || string(u.Ecosystem) != w.eco || u.Name != w.name || u.Version != w.version ||
					u.Manifest.Path != w.path || u.Manifest.Line != w.line || u.Manifest.Repo != repo {
					t.Errorf("use %d = %s %s %s %q %s:%d, want %+v", i, u.Component, u.Ecosystem, u.Name, u.Version, u.Manifest.Path, u.Manifest.Line, w)
				}
				if u.Manifest.Excerpt == "" || u.Manifest.SourceKind != model.KindPackageManifest {
					t.Errorf("use %d evidence %+v", i, u.Manifest)
				}
			}
			// left-pad (transitive, lockfile only) must not appear.
			for _, u := range f.PackageUses {
				if u.Name == "left-pad" {
					t.Errorf("transitive lockfile package reported: %+v", u)
				}
			}
			srcs := map[string]model.SourceStatus{}
			for _, s := range f.Sources {
				srcs[s.Path] = s.Status
			}
			for _, p := range []string{"package.json", "svc/py/requirements.txt", "svc/go/go.mod", "svc/java/pom.xml", "svc/api/pyproject.toml", "svc/net/app.csproj"} {
				if srcs[p] != model.StatusParsed {
					t.Errorf("source %s status %q", p, srcs[p])
				}
			}
			if tc.name == "lockfiles" && srcs["package-lock.json"] != model.StatusParsed {
				t.Errorf("lockfile source missing: %v", srcs)
			}
		})
	}
}

func TestBrokenManifestIsFailedSource(t *testing.T) {
	f := detect(t, NewWith(nil), map[string]string{
		"a/package.json": "{ not json",
		"b/go.mod":       "module x\nrequire github.com/a/b v1.0.0\n",
	})
	var failed bool
	for _, s := range f.Sources {
		if s.Path == "a/package.json" {
			failed = s.Status == model.StatusFailed && s.Note == "could not be parsed as a dependency manifest"
		}
	}
	if !failed {
		t.Errorf("want failed source for a/package.json: %+v", f.Sources)
	}
	if len(f.RawNodes) != 2 {
		t.Errorf("both directories are components: %+v", f.RawNodes)
	}
	if len(f.PackageUses) != 1 || f.PackageUses[0].Name != "github.com/a/b" || f.PackageUses[0].Manifest.Line != 2 {
		t.Errorf("uses: %+v", f.PackageUses)
	}
}

func TestStandaloneLockfileProvidesPackages(t *testing.T) {
	// A component with only a NuGet lockfile: its direct packages are used.
	f := detect(t, NewWith(nil), map[string]string{
		"app/packages.lock.json": `{
  "version": 1,
  "dependencies": {
    "net8.0": {
      "AWSSDK.S3": { "type": "Direct", "resolved": "3.7.0" },
      "AWSSDK.Core": { "type": "Transitive", "resolved": "3.7.0" }
    }
  }
}
`,
	})
	if len(f.PackageUses) != 1 || f.PackageUses[0].Name != "AWSSDK.S3" || f.PackageUses[0].Manifest.Line != 5 || f.PackageUses[0].Version != "3.7.0" {
		t.Errorf("uses: %+v", f.PackageUses)
	}
}

func TestFindLine(t *testing.T) {
	lines := []string{`"@aws-sdk/client-dynamodb-streams": "1"`, `"@aws-sdk/client-dynamodb": "1"`, `<artifactId>s3</artifactId>`}
	for _, tc := range []struct {
		name string
		want int
	}{
		{"@aws-sdk/client-dynamodb", 2},
		{"software.amazon.awssdk:s3", 3},
		{"missing", 1},
	} {
		if got := FindLine(lines, tc.name); got != tc.want {
			t.Errorf("FindLine(%q) = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestParsePURL(t *testing.T) {
	for _, tc := range []struct{ in, typ, ns, name string }{
		{"pkg:npm/%40aws-sdk/client-s3@3.0.0", "npm", "@aws-sdk", "client-s3"},
		{"pkg:maven/software.amazon.awssdk/s3@2.0?type=jar", "maven", "software.amazon.awssdk", "s3"},
		{"pkg:golang/cloud.google.com/go/pubsub@v1", "golang", "cloud.google.com/go", "pubsub"},
	} {
		typ, ns, name, ok := parsePURL(tc.in)
		if !ok || typ != tc.typ || ns != tc.ns || name != tc.name {
			t.Errorf("parsePURL(%q) = %q %q %q", tc.in, typ, ns, name)
		}
	}
}

const cycloneDX = `{
  "bomFormat": "CycloneDX",
  "specVersion": "1.5",
  "version": 1,
  "components": [
    {"type": "library", "name": "boto3", "version": "1.34.0", "purl": "pkg:pypi/boto3@1.34.0"},
    {"type": "library", "name": "client-s3", "group": "@aws-sdk", "version": "3.0.0", "purl": "pkg:npm/%40aws-sdk/client-s3@3.0.0"}
  ]
}
`

func TestSBOMFileIsComponent(t *testing.T) {
	f := detect(t, New(), map[string]string{"dist-sbom/app.cdx.json": cycloneDX})
	if len(f.RawNodes) != 1 || f.RawNodes[0].ID != repo+"//dist-sbom#component" || f.RawNodes[0].Evidence[0].SourceKind != model.KindSBOM {
		t.Fatalf("nodes: %+v", f.RawNodes)
	}
	if len(f.PackageUses) != 2 {
		t.Fatalf("uses: %+v", f.PackageUses)
	}
	if u := f.PackageUses[0]; u.Name != "@aws-sdk/client-s3" || u.Ecosystem != model.EcoNPM || u.Manifest.Line != 7 || u.Manifest.SourceKind != model.KindSBOM {
		t.Errorf("use 0: %+v", u)
	}
	if u := f.PackageUses[1]; u.Name != "boto3" || u.Version != "1.34.0" || u.Manifest.Line != 6 {
		t.Errorf("use 1: %+v", u)
	}
	if len(f.Sources) != 1 || f.Sources[0].Kind != model.KindSBOM || f.Sources[0].Path != "dist-sbom/app.cdx.json" {
		t.Errorf("sources: %+v", f.Sources)
	}
}
