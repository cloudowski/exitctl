// Package redact hides secret-looking values in attributes and evidence
// excerpts before they reach a result file (research R17).
package redact

import (
	"math"
	"regexp"
	"strings"
	"unicode"

	"github.com/cloudowski/exitctl/internal/model"
)

// Mask replaces secret matches in evidence lines.
const Mask = "***"

// Single-token sensitive key words (matched as whole key tokens).
var sensitiveTokens = map[string]bool{
	"password":    true,
	"passwords":   true,
	"passwd":      true,
	"secret":      true,
	"secrets":     true,
	"token":       true,
	"tokens":      true,
	"privatekey":  true,
	"apikey":      true,
	"credential":  true,
	"credentials": true,
	"sas":         true,
}

// Multi-token sensitive key phrases (matched as consecutive key tokens).
var sensitivePhrases = [][]string{
	{"private", "key"},
	{"api", "key"},
	{"access", "key"},
	{"secret", "key"},
	{"connection", "string"},
	{"conn", "str"},
	{"client", "secret"},
}

// Unambiguous words also matched as substrings of the key with separators
// removed (e.g. "dbpassword", "MasterUserPassword").
var sensitiveSubstrings = []string{
	"password", "passwd", "privatekey", "apikey", "accesskey", "secretkey",
	"connectionstring", "clientsecret", "credentials",
}

// Keys whose last token is one of these name a reference to a secret (an
// ARN, an ID, a name) or a property of it, not the secret itself.
var referenceSuffixes = map[string]bool{
	"arn": true, "arns": true, "id": true, "ids": true, "name": true, "names": true,
	"ref": true, "type": true, "version": true, "length": true, "enabled": true,
}

var (
	reAWSKey = regexp.MustCompile(`AKIA[0-9A-Z]{16}`)
	rePEM    = regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----`)
	reJWT    = regexp.MustCompile(`eyJ[\w-]+\.[\w-]+\.[\w-]+`)
	reToken  = regexp.MustCompile(`[A-Za-z0-9+/=_\-]{24,}`)
	// key = value / key: value / "key": "value" assignments in a line.
	reAssign = regexp.MustCompile(`(["']?)([A-Za-z0-9_.\-]+)(["']?)(\s*[:=]\s*)("[^"]*"|'[^']*'|[^\s,;}\]]+)`)
)

// SensitiveKey reports whether an attribute key names a secret. Only the
// last path segment of the key counts (e.g. "env.DB_PASSWORD").
func SensitiveKey(key string) bool {
	seg := lastSegment(key)
	if seg == "" {
		return false
	}
	toks := tokens(seg)
	if len(toks) == 0 {
		return false
	}
	if referenceSuffixes[toks[len(toks)-1]] && len(toks) > 1 {
		return false
	}
	for _, t := range toks {
		if sensitiveTokens[t] {
			return true
		}
	}
	for _, p := range sensitivePhrases {
		for i := 0; i+len(p) <= len(toks); i++ {
			match := true
			for j := range p {
				if toks[i+j] != p[j] {
					match = false
					break
				}
			}
			if match {
				return true
			}
		}
	}
	joined := strings.Join(toks, "")
	for _, s := range sensitiveSubstrings {
		if strings.Contains(joined, s) {
			return true
		}
	}
	return false
}

// Value returns model.Redacted when key names a secret or when a string value
// looks like one; maps and lists are redacted element by element. Non-string
// scalars (numbers, booleans, nil) and sentinels are returned unchanged.
func Value(key string, v any) any {
	return value(key, v, false)
}

func value(key string, v any, forced bool) any {
	if model.IsUnknown(v) || model.IsRedacted(v) {
		return v
	}
	sensitive := forced || SensitiveKey(key)
	switch x := v.(type) {
	case string:
		if x == "" {
			return x
		}
		if sensitive || LooksSecret(x) {
			return model.Redacted
		}
		return x
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = value(k, e, sensitive)
		}
		return out
	case map[string]string:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = value(k, e, sensitive)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = value(key, e, sensitive)
		}
		return out
	case []string:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = value(key, e, sensitive)
		}
		return out
	default:
		return v
	}
}

// LooksSecret reports whether s contains a known secret pattern (AWS access
// key, PEM private key header, JWT) or a high-entropy token.
func LooksSecret(s string) bool {
	if reAWSKey.MatchString(s) || rePEM.MatchString(s) || reJWT.MatchString(s) {
		return true
	}
	for _, t := range reToken.FindAllString(s, -1) {
		if highEntropy(t) {
			return true
		}
	}
	return false
}

// Line redacts an evidence excerpt: known secret patterns and high-entropy
// tokens are replaced by "***", and so are literal values assigned to a
// sensitive key (e.g. `password = "hunter2"` becomes `password = "***"`).
func Line(s string) string {
	s = rePEM.ReplaceAllString(s, Mask)
	s = reJWT.ReplaceAllString(s, Mask)
	s = reAWSKey.ReplaceAllString(s, Mask)
	s = reToken.ReplaceAllStringFunc(s, func(t string) string {
		if highEntropy(t) {
			return Mask
		}
		return t
	})
	s = reAssign.ReplaceAllStringFunc(s, func(m string) string {
		g := reAssign.FindStringSubmatch(m)
		key, val := g[2], g[5]
		if !SensitiveKey(key) || val == Mask || !literalValue(val) {
			return m
		}
		q := ""
		if len(val) >= 2 && (val[0] == '"' || val[0] == '\'') {
			q = val[:1]
		}
		return g[1] + key + g[3] + g[4] + q + Mask + q
	})
	return s
}

// literalValue reports whether an assigned value is a literal worth hiding
// (not an expression, reference, template, or plain boolean/number).
func literalValue(v string) bool {
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') {
		inner := v[1 : len(v)-1]
		if inner == "" || inner == Mask {
			return false
		}
		return !strings.HasPrefix(inner, "${") && !strings.HasPrefix(inner, "{{")
	}
	switch strings.ToLower(v) {
	case "true", "false", "null", "~", "yes", "no", "|", ">", "|-", ">-":
		return false
	}
	if strings.IndexFunc(v, func(r rune) bool { return !unicode.IsDigit(r) && r != '.' && r != '-' }) < 0 {
		return false
	}
	for _, p := range []string{"var.", "local.", "data.", "module.", "each.", "self.", "${", "{{", "!", "[", "{", "(", "&", "*"} {
		if strings.HasPrefix(v, p) {
			return false
		}
	}
	return true
}

// highEntropy reports whether a token looks like a generated secret: at
// least 24 characters, Shannon entropy of at least 4.0 bits per character,
// letters and digits mixed, and not shaped like an identifier. Identifier
// shape: words joined by '_', '-' or '.' where no word reaches 12 characters,
// or a single-case token shorter than 32 characters.
func highEntropy(t string) bool {
	if len(t) < 24 {
		return false
	}
	var digit, lower, upper bool
	for i := 0; i < len(t); i++ {
		c := t[i]
		switch {
		case c >= '0' && c <= '9':
			digit = true
		case c >= 'a' && c <= 'z':
			lower = true
		case c >= 'A' && c <= 'Z':
			upper = true
		}
	}
	if !digit || (!lower && !upper) {
		return false
	}
	if (!lower || !upper) && len(t) < 32 {
		return false
	}
	words := strings.FieldsFunc(t, func(r rune) bool { return r == '_' || r == '-' || r == '.' })
	if len(words) > 1 {
		longest := 0
		for _, w := range words {
			longest = max(longest, len(w))
		}
		if longest < 12 {
			return false
		}
	}
	return entropy(t) >= 4.0
}

func entropy(s string) float64 {
	var counts [256]int
	for i := 0; i < len(s); i++ {
		counts[s[i]]++
	}
	n := float64(len(s))
	h := 0.0
	for _, c := range counts {
		if c == 0 {
			continue
		}
		p := float64(c) / n
		h -= p * math.Log2(p)
	}
	return h
}

func lastSegment(key string) string {
	parts := strings.FieldsFunc(key, func(r rune) bool {
		return r == '.' || r == '/' || r == '[' || r == ']'
	})
	for i := len(parts) - 1; i >= 0; i-- {
		p := strings.Trim(parts[i], `"'`)
		if p != "" && strings.IndexFunc(p, func(r rune) bool { return !unicode.IsDigit(r) }) >= 0 {
			return p
		}
	}
	return ""
}

// tokens splits a key into lower-case words on separators and camelCase
// boundaries: "MasterUserPassword" -> [master user password].
func tokens(s string) []string {
	var out []string
	var cur []rune
	rs := []rune(s)
	flush := func() {
		if len(cur) > 0 {
			out = append(out, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	for i, r := range rs {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			flush()
			continue
		}
		if unicode.IsUpper(r) && len(cur) > 0 {
			prev := rs[i-1]
			nextLower := i+1 < len(rs) && unicode.IsLower(rs[i+1])
			if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextLower) {
				flush()
			}
		}
		cur = append(cur, r)
	}
	flush()
	return out
}

// reSecretType matches resource types whose value attributes hold secrets
// (SSM parameters, secret versions, Key Vault secrets, Vault KV, ...).
var reSecretType = regexp.MustCompile(`(?i)(secret|password|credential|ssm_parameter|ssm::parameter|key_vault|keyvault|vault_kv|vault_generic)`)

// secretValueKeys are attribute names that carry the payload of a
// secret-bearing resource (compared case-insensitively, without '_').
var secretValueKeys = map[string]bool{
	"value": true, "secretstring": true, "secretbinary": true, "secretdata": true,
	"secretvalue": true, "data": true, "datajson": true, "plaintext": true, "content": true,
	"stringdata": true, "insecurevalue": true, "payload": true,
}

// SecretBearing reports whether a resource type stores secret material.
func SecretBearing(resourceType string) bool { return reSecretType.MatchString(resourceType) }

// ResourceValue is Value with resource context: the payload attributes of
// secret-bearing resource types are always redacted, whatever they contain.
func ResourceValue(resourceType, key string, v any) any {
	if SecretBearing(resourceType) && secretValueKeys[strings.ToLower(strings.ReplaceAll(key, "_", ""))] {
		return value(key, v, true)
	}
	return Value(key, v)
}

// ResourceLine is Line with resource context: in secret-bearing resources,
// literals assigned to payload keys are masked too.
func ResourceLine(resourceType, s string) string {
	s = Line(s)
	if !SecretBearing(resourceType) {
		return s
	}
	return reAssign.ReplaceAllStringFunc(s, func(m string) string {
		g := reAssign.FindStringSubmatch(m)
		key, val := g[2], g[5]
		if !secretValueKeys[strings.ToLower(strings.ReplaceAll(key, "_", ""))] || val == Mask || !literalValue(val) {
			return m
		}
		q := ""
		if len(val) >= 2 && (val[0] == '"' || val[0] == '\'') {
			q = val[:1]
		}
		return g[1] + key + g[3] + g[4] + q + Mask + q
	})
}
