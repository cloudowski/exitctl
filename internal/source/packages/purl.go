package packages

import (
	"strings"

	"github.com/cloudowski/exitctl/internal/model"
)

// purlPackage maps a package URL to an ecosystem, an exitctl package name,
// and a version. Only the ecosystems exitctl matches are returned.
func purlPackage(purl string) (model.Ecosystem, string, string, bool) {
	typ, ns, name, ok := parsePURL(purl)
	if !ok {
		return "", "", "", false
	}
	version := purlVersion(purl)
	switch typ {
	case "npm":
		if ns != "" {
			return model.EcoNPM, ns + "/" + name, version, true
		}
		return model.EcoNPM, name, version, true
	case "pypi":
		return model.EcoPyPI, normalizePyPI(name), version, true
	case "golang":
		if ns != "" {
			return model.EcoGo, ns + "/" + name, version, true
		}
		return model.EcoGo, name, version, true
	case "maven":
		if ns != "" {
			return model.EcoMaven, ns + ":" + name, version, true
		}
		return model.EcoMaven, name, version, true
	case "nuget":
		return model.EcoNuGet, name, version, true
	case "gem":
		return model.EcoRubyGems, name, version, true
	case "composer":
		if ns != "" {
			return model.EcoComposer, strings.ToLower(ns + "/" + name), version, true
		}
		return model.EcoComposer, strings.ToLower(name), version, true
	case "cargo":
		return model.EcoCargo, name, version, true
	}
	return "", "", "", false
}

// parsePURL splits "pkg:type/namespace/name@version?qualifiers#subpath".
func parsePURL(purl string) (typ, ns, name string, ok bool) {
	if !strings.HasPrefix(purl, "pkg:") {
		return "", "", "", false
	}
	s := purl[len("pkg:"):]
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		s = s[:i]
	}
	if i := strings.LastIndex(s, "@"); i > 0 {
		s = s[:i]
	}
	segs := strings.Split(strings.Trim(s, "/"), "/")
	if len(segs) < 2 {
		return "", "", "", false
	}
	for i, sg := range segs {
		segs[i] = unescapePURL(sg)
	}
	typ = strings.ToLower(segs[0])
	name = segs[len(segs)-1]
	ns = strings.Join(segs[1:len(segs)-1], "/")
	return typ, ns, name, name != ""
}

// purlVersion returns the unescaped version of a package URL, "" if none.
func purlVersion(purl string) string {
	s := purl
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		s = s[:i]
	}
	i := strings.LastIndex(s, "@")
	if i < 0 || strings.Contains(s[i:], "/") {
		return ""
	}
	return unescapePURL(s[i+1:])
}

func unescapePURL(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			if v, ok := hexByte(s[i+1], s[i+2]); ok {
				b.WriteByte(v)
				i += 2
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func hexByte(a, b byte) (byte, bool) {
	h := func(c byte) (byte, bool) {
		switch {
		case c >= '0' && c <= '9':
			return c - '0', true
		case c >= 'a' && c <= 'f':
			return c - 'a' + 10, true
		case c >= 'A' && c <= 'F':
			return c - 'A' + 10, true
		}
		return 0, false
	}
	x, ok1 := h(a)
	y, ok2 := h(b)
	return x<<4 | y, ok1 && ok2
}
