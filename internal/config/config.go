// Package config loads and validates .exitctl.yaml and holds the scoring
// configuration (data-model.md 4.1 and Part 6).
package config

import "github.com/cloudowski/exitctl/internal/catalog"

// Band maps an index range to a grade.
type Band struct {
	Grade string `yaml:"grade" json:"grade"`
	Min   int    `yaml:"min" json:"min"`
	Max   int    `yaml:"max" json:"max"`
}

// Thresholds decide grade confidence from weighted coverage.
type Thresholds struct {
	Normal      float64 `yaml:"normal" json:"normal"`
	Provisional float64 `yaml:"provisional" json:"provisional"`
}

// Scoring is the effective scoring configuration.
type Scoring struct {
	LevelWeights       map[catalog.Level]float64     `yaml:"level_weights" json:"level_weights"`
	DimensionWeights   map[catalog.Dimension]float64 `yaml:"dimension_weights" json:"dimension_weights"`
	GradeBands         []Band                        `yaml:"grade_bands" json:"grade_bands"`
	CoverageThresholds Thresholds                    `yaml:"coverage_thresholds" json:"coverage_thresholds"`
	TopAnchors         int                           `yaml:"top_anchors" json:"top_anchors"`
	GroupThreshold     int                           `yaml:"group_threshold" json:"group_threshold"`
}

// Selector of an exception: exactly one of Finding, Resource, Service, Trait,
// or the combination Resource + Trait.
type Selector struct {
	Finding  string `yaml:"finding,omitempty" json:"finding,omitempty"`
	Resource string `yaml:"resource,omitempty" json:"resource,omitempty"`
	Service  string `yaml:"service,omitempty" json:"service,omitempty"`
	Trait    string `yaml:"trait,omitempty" json:"trait,omitempty"`
}

// Exception accepts known lock-in (applied in user story 7).
type Exception struct {
	ID      string   `yaml:"id" json:"id"`
	Select  Selector `yaml:"select" json:"select"`
	Reason  string   `yaml:"reason" json:"reason"`
	Owner   string   `yaml:"owner" json:"owner"`
	Expires string   `yaml:"expires,omitempty" json:"expires,omitempty"`
}

// Helm options.
type Helm struct {
	ValuesFiles map[string][]string `yaml:"values_files" json:"values_files"`
}

// Config is the effective configuration (defaults merged with the file).
type Config struct {
	Version    int         `yaml:"version"`
	RepoName   string      `yaml:"repo_name"`
	Ignore     []string    `yaml:"ignore"`
	Helm       Helm        `yaml:"helm"`
	Scoring    Scoring     `yaml:"-"`
	Exceptions []Exception `yaml:"exceptions"`
	// Path is the file the config was loaded from ("" = defaults only).
	Path string `yaml:"-"`
}

// Defaults returns the default configuration (data-model.md 4.1).
func Defaults() Config {
	return Config{Version: 1, Scoring: DefaultScoring()}
}

// DefaultScoring returns the default scoring configuration.
func DefaultScoring() Scoring {
	return Scoring{
		LevelWeights: map[catalog.Level]float64{
			catalog.LevelNone: 0, catalog.LevelLow: 0.25, catalog.LevelMedium: 0.5,
			catalog.LevelHigh: 0.8, catalog.LevelCritical: 1.0,
		},
		DimensionWeights: map[catalog.Dimension]float64{
			catalog.DimData: 1.0, catalog.DimTechnical: 0.9, catalog.DimOperational: 0.7,
			catalog.DimSkills: 0.5, catalog.DimEconomic: 0,
		},
		GradeBands: []Band{
			{"A", 0, 9}, {"B", 10, 24}, {"C", 25, 39}, {"D", 40, 54}, {"E", 55, 100},
		},
		CoverageThresholds: Thresholds{Normal: 0.90, Provisional: 0.70},
		TopAnchors:         5,
		GroupThreshold:     50,
	}
}

// Grade returns the grade for an index using the bands.
func (s Scoring) Grade(index int) string {
	for _, b := range s.GradeBands {
		if index >= b.Min && index <= b.Max {
			return b.Grade
		}
	}
	return ""
}
