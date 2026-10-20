package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.yaml.in/yaml/v3"

	"github.com/cloudowski/exitctl/internal/catalog"
	"github.com/cloudowski/exitctl/schemas"
)

// FileName is the repository configuration file name.
const FileName = ".exitctl.yaml"

const schemaName = "config.schema.json"

// Discover finds and loads the configuration: the explicit path when given
// (relative paths resolve against cwd), else <scanRoot>/.exitctl.yaml, else
// <cwd>/.exitctl.yaml, else the defaults (research R18).
func Discover(scanRoot, cwd, explicit string) (Config, error) {
	if explicit != "" {
		p := explicit
		if !filepath.IsAbs(p) && cwd != "" {
			p = filepath.Join(cwd, p)
		}
		fi, err := os.Stat(p)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return Config{}, fmt.Errorf("config file %s not found", explicit)
			}
			return Config{}, fmt.Errorf("config file %s cannot be read", explicit)
		}
		if fi.IsDir() {
			return Config{}, fmt.Errorf("config file %s is a directory", explicit)
		}
		return loadAs(p, explicit)
	}
	for _, dir := range []string{scanRoot, cwd} {
		if dir == "" {
			continue
		}
		p := filepath.Join(dir, FileName)
		fi, err := os.Stat(p)
		if err != nil || fi.IsDir() {
			continue
		}
		return Load(p)
	}
	return Defaults(), nil
}

// Load reads, validates, and merges one configuration file onto the defaults.
func Load(path string) (Config, error) {
	return loadAs(path, path)
}

func loadAs(path, display string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("config file %s cannot be read", display)
	}
	cfg, err := Parse(display, data)
	if err != nil {
		return Config{}, err
	}
	cfg.Path = path
	return cfg, nil
}

// fileConfig is the shape of .exitctl.yaml for typed decoding.
type fileConfig struct {
	Version    int              `yaml:"version"`
	RepoName   string           `yaml:"repo_name"`
	Ignore     []string         `yaml:"ignore"`
	Helm       Helm             `yaml:"helm"`
	Scoring    *scoringOverride `yaml:"scoring"`
	Exceptions []Exception      `yaml:"exceptions"`
}

type scoringOverride struct {
	LevelWeights       map[catalog.Level]float64     `yaml:"level_weights"`
	DimensionWeights   map[catalog.Dimension]float64 `yaml:"dimension_weights"`
	GradeBands         []Band                        `yaml:"grade_bands"`
	CoverageThresholds *struct {
		Normal      *float64 `yaml:"normal"`
		Provisional *float64 `yaml:"provisional"`
	} `yaml:"coverage_thresholds"`
	TopAnchors     *int `yaml:"top_anchors"`
	GroupThreshold *int `yaml:"group_threshold"`
}

// Parse validates YAML configuration data and merges it onto the defaults.
// name identifies the file in error messages.
func Parse(name string, data []byte) (Config, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return Config{}, fmt.Errorf("config file %s is empty; it must contain at least \"version: 1\"", name)
		}
		return Config{}, fmt.Errorf("config file %s is not valid YAML: %s", name, strings.TrimPrefix(err.Error(), "yaml: "))
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err == nil {
		return Config{}, fmt.Errorf("config file %s must contain a single YAML document", name)
	} else if !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("config file %s is not valid YAML: %s", name, strings.TrimPrefix(err.Error(), "yaml: "))
	}

	lines := map[string]int{}
	val, err := toJSONValue(&doc, "", lines)
	if err != nil {
		return Config{}, fmt.Errorf("config file %s: %w", name, err)
	}
	if err := validateSchema(val, lines); err != nil {
		return Config{}, fmt.Errorf("config file %s is invalid:\n%s", name, err.Error())
	}

	var fc fileConfig
	if err := doc.Decode(&fc); err != nil {
		return Config{}, fmt.Errorf("config file %s is invalid: %s", name, strings.TrimPrefix(err.Error(), "yaml: "))
	}
	cfg := Defaults()
	cfg.Version = fc.Version
	cfg.RepoName = fc.RepoName
	cfg.Ignore = fc.Ignore
	cfg.Helm = fc.Helm
	cfg.Exceptions = fc.Exceptions
	cfg.Scoring = fc.Scoring.merge(DefaultScoring())
	probs := cfg.Scoring.problems()
	probs = append(probs, exceptionIDProblems(cfg.Exceptions)...)
	if len(probs) > 0 {
		return Config{}, fmt.Errorf("config file %s is invalid:\n  - %s", name, strings.Join(probs, "\n  - "))
	}
	return cfg, nil
}

// exceptionIDProblems reports exception IDs that appear more than once
// (data-model.md 4.6: unique within the file), in file order. Other
// exception problems (missing reason or owner, no match, expiry) are not
// configuration errors: the exception is reported as invalid or expired and
// ignored (scoring.ApplyExceptions).
func exceptionIDProblems(excs []Exception) []string {
	var out []string
	seen := map[string]bool{}
	for i, e := range excs {
		if seen[e.ID] {
			out = append(out, fmt.Sprintf("exceptions[%d]: id %q appears more than once; exception ids must be unique", i, e.ID))
		}
		seen[e.ID] = true
	}
	return out
}

// merge applies overrides onto base (a fresh copy owned by the caller).
func (o *scoringOverride) merge(base Scoring) Scoring {
	if o == nil {
		return base
	}
	for k, v := range o.LevelWeights {
		base.LevelWeights[k] = v
	}
	for k, v := range o.DimensionWeights {
		base.DimensionWeights[k] = v
	}
	if o.GradeBands != nil {
		bands := append([]Band(nil), o.GradeBands...)
		sort.SliceStable(bands, func(i, j int) bool {
			if bands[i].Min != bands[j].Min {
				return bands[i].Min < bands[j].Min
			}
			return bands[i].Max < bands[j].Max
		})
		base.GradeBands = bands
	}
	if t := o.CoverageThresholds; t != nil {
		if t.Normal != nil {
			base.CoverageThresholds.Normal = *t.Normal
		}
		if t.Provisional != nil {
			base.CoverageThresholds.Provisional = *t.Provisional
		}
	}
	if o.TopAnchors != nil {
		base.TopAnchors = *o.TopAnchors
	}
	if o.GroupThreshold != nil {
		base.GroupThreshold = *o.GroupThreshold
	}
	return base
}

// Validate runs the semantic checks of data-model.md 4.1 on an effective
// scoring configuration and returns all problems as one error.
func (s Scoring) Validate() error {
	if p := s.problems(); len(p) > 0 {
		return errors.New("invalid scoring configuration:\n  - " + strings.Join(p, "\n  - "))
	}
	return nil
}

func (s Scoring) problems() []string {
	var out []string
	unit := func(v float64) bool { return !math.IsNaN(v) && v >= 0 && v <= 1 }
	lk := make([]string, 0, len(s.LevelWeights))
	for k := range s.LevelWeights {
		lk = append(lk, string(k))
	}
	sort.Strings(lk)
	for _, k := range lk {
		if v := s.LevelWeights[catalog.Level(k)]; !unit(v) {
			out = append(out, fmt.Sprintf("scoring.level_weights.%s is %s; weights must be between 0 and 1", k, fmtFloat(v)))
		}
	}
	dk := make([]string, 0, len(s.DimensionWeights))
	for k := range s.DimensionWeights {
		dk = append(dk, string(k))
	}
	sort.Strings(dk)
	positive := false
	for _, k := range dk {
		v := s.DimensionWeights[catalog.Dimension(k)]
		if !unit(v) {
			out = append(out, fmt.Sprintf("scoring.dimension_weights.%s is %s; weights must be between 0 and 1", k, fmtFloat(v)))
		}
		if v > 0 {
			positive = true
		}
	}
	if !positive {
		out = append(out, "scoring.dimension_weights: at least one dimension weight must be greater than 0")
	}
	out = append(out, bandProblems(s.GradeBands)...)
	t := s.CoverageThresholds
	if !(t.Provisional >= 0 && t.Provisional <= t.Normal && t.Normal <= 1) {
		out = append(out, fmt.Sprintf("scoring.coverage_thresholds must satisfy 0 <= provisional <= normal <= 1 (provisional is %s, normal is %s)",
			fmtFloat(t.Provisional), fmtFloat(t.Normal)))
	}
	if s.TopAnchors < 1 {
		out = append(out, fmt.Sprintf("scoring.top_anchors is %d; it must be at least 1", s.TopAnchors))
	}
	if s.GroupThreshold < 1 {
		out = append(out, fmt.Sprintf("scoring.group_threshold is %d; it must be at least 1", s.GroupThreshold))
	}
	return out
}

func bandProblems(in []Band) []string {
	if len(in) == 0 {
		return []string{"scoring.grade_bands must not be empty"}
	}
	bands := append([]Band(nil), in...)
	sort.SliceStable(bands, func(i, j int) bool { return bands[i].Min < bands[j].Min })
	var out []string
	seen := map[string]bool{}
	for _, b := range bands {
		if seen[b.Grade] {
			out = append(out, fmt.Sprintf("scoring.grade_bands: grade %s appears more than once", b.Grade))
		}
		seen[b.Grade] = true
		if b.Min > b.Max {
			out = append(out, fmt.Sprintf("scoring.grade_bands: band %s has min %d greater than max %d", b.Grade, b.Min, b.Max))
		}
	}
	if bands[0].Min != 0 {
		out = append(out, fmt.Sprintf("scoring.grade_bands must cover 0-100 but the lowest band %s starts at %d", bands[0].Grade, bands[0].Min))
	}
	for i := 1; i < len(bands); i++ {
		prev, cur := bands[i-1], bands[i]
		if cur.Min != prev.Max+1 {
			out = append(out, fmt.Sprintf("scoring.grade_bands are not contiguous: band %s ends at %d but band %s starts at %d", prev.Grade, prev.Max, cur.Grade, cur.Min))
		}
	}
	last := bands[len(bands)-1]
	if last.Max != 100 {
		out = append(out, fmt.Sprintf("scoring.grade_bands must cover 0-100 but the highest band %s ends at %d", last.Grade, last.Max))
	}
	return out
}

func fmtFloat(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// toJSONValue converts a YAML node into JSON-compatible values (maps with
// string keys, slices, strings, int64, float64, bool, nil) and records the
// line of every JSON pointer for error messages. Timestamps stay strings.
func toJSONValue(n *yaml.Node, ptr string, lines map[string]int) (any, error) {
	if n.Kind == yaml.DocumentNode {
		if len(n.Content) == 0 {
			return nil, nil
		}
		return toJSONValue(n.Content[0], ptr, lines)
	}
	if n.Kind == yaml.AliasNode && n.Alias != nil {
		return toJSONValue(n.Alias, ptr, lines)
	}
	if _, ok := lines[ptr]; !ok {
		lines[ptr] = n.Line
	}
	switch n.Kind {
	case yaml.MappingNode:
		m := make(map[string]any, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			if k.Kind != yaml.ScalarNode {
				return nil, fmt.Errorf("line %d: mapping keys must be plain strings", k.Line)
			}
			if _, dup := m[k.Value]; dup {
				return nil, fmt.Errorf("line %d: duplicate key %q", k.Line, k.Value)
			}
			cp := ptr + "/" + escapePtr(k.Value)
			lines[cp] = k.Line
			v, err := toJSONValue(n.Content[i+1], cp, lines)
			if err != nil {
				return nil, err
			}
			m[k.Value] = v
		}
		return m, nil
	case yaml.SequenceNode:
		s := make([]any, 0, len(n.Content))
		for i, c := range n.Content {
			v, err := toJSONValue(c, ptr+"/"+strconv.Itoa(i), lines)
			if err != nil {
				return nil, err
			}
			s = append(s, v)
		}
		return s, nil
	case yaml.ScalarNode:
		switch n.ShortTag() {
		case "!!null":
			return nil, nil
		case "!!bool":
			var b bool
			if err := n.Decode(&b); err != nil {
				return n.Value, nil
			}
			return b, nil
		case "!!int":
			var i int64
			if err := n.Decode(&i); err == nil {
				return i, nil
			}
			var f float64
			if err := n.Decode(&f); err == nil {
				return f, nil
			}
			return n.Value, nil
		case "!!float":
			var f float64
			if err := n.Decode(&f); err != nil {
				return n.Value, nil
			}
			if math.IsInf(f, 0) || math.IsNaN(f) {
				return nil, fmt.Errorf("line %d: %s is not a finite number", n.Line, n.Value)
			}
			return f, nil
		default:
			return n.Value, nil
		}
	}
	return nil, fmt.Errorf("line %d: unsupported YAML node", n.Line)
}

func escapePtr(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}

var (
	schemaOnce sync.Once
	schemaVal  *jsonschema.Schema
	schemaErr  error
)

func configSchema() (*jsonschema.Schema, error) {
	schemaOnce.Do(func() {
		f, err := schemas.FS.Open(schemaName)
		if err != nil {
			schemaErr = fmt.Errorf("embedded %s missing: %w", schemaName, err)
			return
		}
		defer func() { _ = f.Close() }()
		doc, err := jsonschema.UnmarshalJSON(f)
		if err != nil {
			schemaErr = fmt.Errorf("embedded %s is not valid JSON: %w", schemaName, err)
			return
		}
		c := jsonschema.NewCompiler()
		c.AssertFormat()
		if err := c.AddResource(schemaName, doc); err != nil {
			schemaErr = err
			return
		}
		schemaVal, schemaErr = c.Compile(schemaName)
	})
	return schemaVal, schemaErr
}

// validateSchema validates v against config.schema.json and turns errors
// into "  - line N: at /path: message" lines, sorted and de-duplicated.
func validateSchema(v any, lines map[string]int) error {
	sch, err := configSchema()
	if err != nil {
		return fmt.Errorf("  - internal error: %v", err)
	}
	// Round-trip through JSON so the validator sees the same value types as
	// for a JSON document.
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("  - %v", err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("  - %v", err)
	}
	err = sch.Validate(inst)
	if err == nil {
		return nil
	}
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return fmt.Errorf("  - %v", err)
	}
	type msg struct {
		line int
		text string
	}
	var msgs []msg
	seen := map[string]bool{}
	var collect func(u jsonschema.OutputUnit)
	collect = func(u jsonschema.OutputUnit) {
		for _, c := range u.Errors {
			collect(c)
		}
		if u.Error == nil || len(u.Errors) > 0 {
			return
		}
		loc := u.InstanceLocation
		where := loc
		if where == "" {
			where = "(top level)"
		}
		text := u.Error.String()
		line := lineFor(loc, lines)
		full := fmt.Sprintf("at %s: %s", where, text)
		if line > 0 {
			full = fmt.Sprintf("line %d: %s", line, full)
		}
		if !seen[full] {
			seen[full] = true
			msgs = append(msgs, msg{line, full})
		}
	}
	out := ve.BasicOutput()
	collect(*out)
	if len(msgs) == 0 {
		return fmt.Errorf("  - %s", strings.TrimSpace(ve.Error()))
	}
	sort.SliceStable(msgs, func(i, j int) bool {
		if msgs[i].line != msgs[j].line {
			return msgs[i].line < msgs[j].line
		}
		return msgs[i].text < msgs[j].text
	})
	parts := make([]string, len(msgs))
	for i, m := range msgs {
		parts[i] = "  - " + m.text
	}
	return errors.New(strings.Join(parts, "\n"))
}

func lineFor(ptr string, lines map[string]int) int {
	for {
		if l, ok := lines[ptr]; ok {
			return l
		}
		i := strings.LastIndex(ptr, "/")
		if i < 0 {
			return 0
		}
		ptr = ptr[:i]
	}
}
