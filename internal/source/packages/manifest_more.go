package packages

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	"github.com/cloudowski/exitctl/internal/model"
)

// ---------------------------------------------------------------- ruby

var (
	reGemfile     = regexp.MustCompile(`^\s*gem\s*\(?\s*["']([^"']+)["'](?:\s*,\s*["']([^"']+)["'])?`)
	reGemfileLock = regexp.MustCompile(`^    ([A-Za-z0-9_.\-]+) \(([^)\s]+)[^)]*\)\s*$`)
)

// parseGemfile reads `gem "name", "version"` lines.
func parseGemfile(_ string, data []byte) ([]Pkg, error) {
	var out []Pkg
	for i, l := range splitLines(data) {
		if strings.HasPrefix(strings.TrimSpace(l), "#") {
			continue
		}
		if m := reGemfile.FindStringSubmatch(l); m != nil {
			out = append(out, Pkg{Ecosystem: model.EcoRubyGems, Name: m[1], Version: m[2], Line: i + 1})
		}
	}
	return out, nil
}

// parseGemfileLock reads the resolved gems of the GEM/GIT/PATH specs
// sections: gems are indented four spaces, their dependencies six.
func parseGemfileLock(_ string, data []byte) ([]Pkg, error) {
	var out []Pkg
	inSpecs := false
	for i, l := range splitLines(data) {
		switch {
		case strings.TrimSpace(l) == "specs:":
			inSpecs = true
		case l != "" && !strings.HasPrefix(l, " "):
			inSpecs = false
		case inSpecs:
			if m := reGemfileLock.FindStringSubmatch(l); m != nil {
				out = append(out, Pkg{Ecosystem: model.EcoRubyGems, Name: m[1], Version: m[2], Line: i + 1})
			}
		}
	}
	return out, nil
}

// ---------------------------------------------------------------- php

// parseComposerJSON reads require and require-dev, skipping the platform
// requirements (php, ext-*, lib-*).
func parseComposerJSON(_ string, data []byte) ([]Pkg, error) {
	var doc struct {
		Require    map[string]any `json:"require"`
		RequireDev map[string]any `json:"require-dev"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	lines := splitLines(data)
	var out []Pkg
	add := func(m map[string]any) {
		names := make([]string, 0, len(m))
		for n := range m {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			if !strings.Contains(n, "/") { // php, ext-json, lib-curl, composer-plugin-api
				continue
			}
			v, _ := m[n].(string)
			out = append(out, Pkg{Ecosystem: model.EcoComposer, Name: strings.ToLower(n), Version: v, Line: jsonKeyLine(lines, n)})
		}
	}
	add(doc.Require)
	add(doc.RequireDev)
	return out, nil
}

// parseComposerLock reads the resolved packages and packages-dev.
func parseComposerLock(_ string, data []byte) ([]Pkg, error) {
	var doc struct {
		Packages    []struct{ Name, Version string } `json:"packages"`
		PackagesDev []struct{ Name, Version string } `json:"packages-dev"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	lines := splitLines(data)
	var out []Pkg
	for _, list := range [][]struct{ Name, Version string }{doc.Packages, doc.PackagesDev} {
		for _, p := range list {
			out = append(out, Pkg{Ecosystem: model.EcoComposer, Name: strings.ToLower(p.Name),
				Version: strings.TrimPrefix(p.Version, "v"), Line: FindLine(lines, p.Name)})
		}
	}
	return out, nil
}

// ---------------------------------------------------------------- rust

var (
	reCargoDepSection = regexp.MustCompile(`^(?:workspace\.|target\..+\.)?(?:dev-|build-)?dependencies$`)
	reCargoDepTable   = regexp.MustCompile(`^(?:workspace\.|target\..+\.)?(?:dev-|build-)?dependencies\.([A-Za-z0-9_\-]+)$`)
	reCargoInlineKey  = regexp.MustCompile(`\b(version|package)\s*=\s*["']([^"']+)["']`)
)

// parseCargoToml reads the dependency tables of Cargo.toml: `name = "1"`,
// `name = { version = "1", package = "real" }`, and `[dependencies.name]`.
func parseCargoToml(_ string, data []byte) ([]Pkg, error) {
	var out []Pkg
	section := ""
	cur := -1 // package declared by a [dependencies.name] table
	for i, raw := range splitLines(data) {
		l := strings.TrimSpace(raw)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		if strings.HasPrefix(l, "[") {
			m := reTOMLSection.FindStringSubmatch(l)
			if m == nil {
				continue
			}
			section = strings.ReplaceAll(m[1], " ", "")
			cur = -1
			if t := reCargoDepTable.FindStringSubmatch(section); t != nil {
				out = append(out, Pkg{Ecosystem: model.EcoCargo, Name: t[1], Line: i + 1})
				cur = len(out) - 1
			}
			continue
		}
		m := reTOMLKey.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		key, val := strings.Trim(m[1], `"'`), strings.TrimSpace(m[2])
		switch {
		case cur >= 0:
			switch key {
			case "version":
				if ss := tomlStrings(val); len(ss) > 0 {
					out[cur].Version = ss[0]
				}
			case "package":
				if ss := tomlStrings(val); len(ss) > 0 {
					out[cur].Name = ss[0]
				}
			}
		case reCargoDepSection.MatchString(section):
			p := Pkg{Ecosystem: model.EcoCargo, Name: key, Line: i + 1}
			if strings.HasPrefix(val, "{") {
				for _, kv := range reCargoInlineKey.FindAllStringSubmatch(val, -1) {
					if kv[1] == "version" {
						p.Version = kv[2]
					} else {
						p.Name = kv[2]
					}
				}
			} else if ss := tomlStrings(val); len(ss) > 0 {
				p.Version = ss[0]
			}
			out = append(out, p)
		}
	}
	return out, nil
}

// parseCargoLock reads the [[package]] name/version pairs. Crate names keep
// their spelling.
func parseCargoLock(_ string, data []byte) ([]Pkg, error) {
	var out []Pkg
	inPkg := false
	cur := -1
	for i, raw := range splitLines(data) {
		l := strings.TrimSpace(raw)
		if strings.HasPrefix(l, "[") {
			inPkg = l == "[[package]]"
			cur = -1
			continue
		}
		m := reTOMLKey.FindStringSubmatch(l)
		if !inPkg || m == nil {
			continue
		}
		ss := tomlStrings(m[2])
		if len(ss) == 0 {
			continue
		}
		switch m[1] {
		case "name":
			out = append(out, Pkg{Ecosystem: model.EcoCargo, Name: ss[0], Line: i + 1})
			cur = len(out) - 1
		case "version":
			if cur >= 0 {
				out[cur].Version = ss[0]
			}
		}
	}
	return out, nil
}

// ---------------------------------------------------------------- scala

// reSbtDep matches "group" % "artifact" % "version" and the %% / %%% forms.
var reSbtDep = regexp.MustCompile(`"([A-Za-z0-9_.\-]+)"\s*%{1,3}\s*"([A-Za-z0-9_.\-]+)"(?:\s*%\s*"([^"]+)")?`)

// parseSbt reads library dependencies from build.sbt. The Scala binary
// version suffix of %% artifacts is not part of the name, which matches the
// Maven coordinates the catalog lists.
func parseSbt(_ string, data []byte) ([]Pkg, error) {
	var out []Pkg
	for i, l := range splitLines(data) {
		if strings.HasPrefix(strings.TrimSpace(l), "//") {
			continue
		}
		for _, m := range reSbtDep.FindAllStringSubmatch(l, -1) {
			out = append(out, Pkg{Ecosystem: model.EcoMaven, Name: m[1] + ":" + m[2], Version: m[3], Line: i + 1})
		}
	}
	return out, nil
}
