package packages

import (
	"testing"

	"github.com/cloudowski/exitctl/internal/model"
)

func names(ps []Pkg) map[string]string {
	m := map[string]string{}
	for _, p := range ps {
		m[p.Name] = p.Version
	}
	return m
}

func expect(t *testing.T, got []Pkg, eco model.Ecosystem, want map[string]string) {
	t.Helper()
	for _, p := range got {
		if p.Ecosystem != eco {
			t.Errorf("%s: ecosystem %s, want %s", p.Name, p.Ecosystem, eco)
		}
		if p.Line < 1 {
			t.Errorf("%s: no line", p.Name)
		}
	}
	g := names(got)
	if len(g) != len(want) {
		t.Errorf("got %v, want %v", g, want)
		return
	}
	for n, v := range want {
		if gv, ok := g[n]; !ok || gv != v {
			t.Errorf("%s: got %q (present %v), want %q", n, gv, ok, v)
		}
	}
}

func TestParseGemfile(t *testing.T) {
	ps, err := parseGemfile("Gemfile", []byte("source 'https://rubygems.org'\n# gem 'old'\ngem 'rails', '~> 7.1'\ngem \"aws-sdk-s3\"\n  gem('pg', '1.5')\n"))
	if err != nil {
		t.Fatal(err)
	}
	expect(t, ps, model.EcoRubyGems, map[string]string{"rails": "~> 7.1", "aws-sdk-s3": "", "pg": "1.5"})
}

func TestParseGemfileLock(t *testing.T) {
	lock := "GEM\n  remote: https://rubygems.org/\n  specs:\n    aws-sdk-s3 (1.140.0)\n      aws-sdk-core (~> 3)\n    rails (7.1.3)\n\nPLATFORMS\n  ruby\n\nDEPENDENCIES\n  rails\n"
	ps, _ := parseGemfileLock("Gemfile.lock", []byte(lock))
	expect(t, ps, model.EcoRubyGems, map[string]string{"aws-sdk-s3": "1.140.0", "rails": "7.1.3"})
}

func TestParseComposer(t *testing.T) {
	ps, err := parseComposerJSON("composer.json", []byte(`{
  "require": {"php": ">=8.1", "ext-json": "*", "Google/cloud-pubsub": "^1.0", "aws/aws-sdk-php": "^3.0"},
  "require-dev": {"phpunit/phpunit": "^10"}
}`))
	if err != nil {
		t.Fatal(err)
	}
	expect(t, ps, model.EcoComposer, map[string]string{"google/cloud-pubsub": "^1.0", "aws/aws-sdk-php": "^3.0", "phpunit/phpunit": "^10"})

	ps, err = parseComposerLock("composer.lock", []byte(`{"packages":[{"name":"aws/aws-sdk-php","version":"3.300.1"}],"packages-dev":[{"name":"phpunit/phpunit","version":"v10.5.0"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	expect(t, ps, model.EcoComposer, map[string]string{"aws/aws-sdk-php": "3.300.1", "phpunit/phpunit": "10.5.0"})
}

func TestParseCargo(t *testing.T) {
	toml := `[package]
name = "demo"
version = "0.1.0"

[dependencies]
serde = "1.0"
aws-sdk-s3 = { version = "1.20", features = ["rt-tokio"] }
tokio_alias = { package = "tokio", version = "1" }

[dependencies.google-cloud-pubsub]
version = "0.25"

[dev-dependencies]
mockall = "0.12"

[target.'cfg(unix)'.dependencies]
libc = "0.2"
`
	ps, err := parseCargoToml("Cargo.toml", []byte(toml))
	if err != nil {
		t.Fatal(err)
	}
	expect(t, ps, model.EcoCargo, map[string]string{"serde": "1.0", "aws-sdk-s3": "1.20", "tokio": "1",
		"google-cloud-pubsub": "0.25", "mockall": "0.12", "libc": "0.2"})

	ps, _ = parseCargoLock("Cargo.lock", []byte("[[package]]\nname = \"aws-sdk-s3\"\nversion = \"1.20.0\"\n\n[[package]]\nname = \"tokio_util\"\nversion = \"0.7.0\"\n"))
	expect(t, ps, model.EcoCargo, map[string]string{"aws-sdk-s3": "1.20.0", "tokio_util": "0.7.0"})
}

func TestParseSbt(t *testing.T) {
	sbt := `libraryDependencies ++= Seq(
  "software.amazon.awssdk" % "s3" % "2.25.0",
  "org.typelevel" %% "cats-core" % "2.10.0",
  // "com.old" % "gone" % "1"
)
libraryDependencies += "com.amazonaws" % "aws-java-sdk-sqs" % "1.12.0"
`
	ps, _ := parseSbt("build.sbt", []byte(sbt))
	expect(t, ps, model.EcoMaven, map[string]string{"software.amazon.awssdk:s3": "2.25.0",
		"org.typelevel:cats-core": "2.10.0", "com.amazonaws:aws-java-sdk-sqs": "1.12.0"})
}

func TestPURLNewEcosystems(t *testing.T) {
	for purl, want := range map[string]struct {
		eco  model.Ecosystem
		name string
	}{
		"pkg:gem/aws-sdk-s3@1.0":           {model.EcoRubyGems, "aws-sdk-s3"},
		"pkg:composer/Aws/aws-sdk-php@3.0": {model.EcoComposer, "aws/aws-sdk-php"},
		"pkg:cargo/aws-sdk-s3@1.0":         {model.EcoCargo, "aws-sdk-s3"},
	} {
		eco, name, _, ok := purlPackage(purl)
		if !ok || eco != want.eco || name != want.name {
			t.Errorf("%s: got %s %q %v", purl, eco, name, ok)
		}
	}
}
