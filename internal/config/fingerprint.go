package config

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
)

// canonical renders the scoring configuration as canonical JSON: sorted keys,
// fixed float formatting, no whitespace.
func (s Scoring) canonical() string {
	var b strings.Builder
	num := func(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
	writeMap := func(m map[string]float64) {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(strconv.Quote(k) + ":" + num(m[k]))
		}
		b.WriteByte('}')
	}
	lw := map[string]float64{}
	for k, v := range s.LevelWeights {
		lw[string(k)] = v
	}
	dw := map[string]float64{}
	for k, v := range s.DimensionWeights {
		dw[string(k)] = v
	}
	b.WriteString(`{"coverage_thresholds":{"normal":` + num(s.CoverageThresholds.Normal) +
		`,"provisional":` + num(s.CoverageThresholds.Provisional) + `},"dimension_weights":`)
	writeMap(dw)
	b.WriteString(`,"grade_bands":[`)
	for i, band := range s.GradeBands {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"grade":` + strconv.Quote(band.Grade) + `,"max":` + strconv.Itoa(band.Max) +
			`,"min":` + strconv.Itoa(band.Min) + `}`)
	}
	b.WriteString(`],"group_threshold":` + strconv.Itoa(s.GroupThreshold) + `,"level_weights":`)
	writeMap(lw)
	b.WriteString(`,"top_anchors":` + strconv.Itoa(s.TopAnchors) + `}`)
	return b.String()
}

// Fingerprint returns the first 8 hex characters of the SHA-256 of the
// canonical scoring configuration (research R19).
func (s Scoring) Fingerprint() string {
	sum := sha256.Sum256([]byte(s.canonical()))
	return hex.EncodeToString(sum[:])[:8]
}

// Custom reports whether the scoring configuration differs from the defaults.
func (s Scoring) Custom() bool {
	return s.canonical() != DefaultScoring().canonical()
}
