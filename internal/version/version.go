// Package version holds the versions that identify every exitctl result.
package version

// Tool is the exitctl version, set at build time via
// -ldflags "-X github.com/cloudowski/exitctl/internal/version.Tool=..."
var Tool = "0.1.0-dev"

// SupportEmail is shown at the bottom of reports. It is a build-time setting:
// -ldflags "-X github.com/cloudowski/exitctl/internal/version.SupportEmail=..."
var SupportEmail = ""

// RulesVersion is the version of the rule evaluation and scoring semantics.
// Bump it whenever rule evaluation or any scoring formula changes (research R19).
const RulesVersion = "1.1.0"

// ResultFormat identifies the result document format.
const ResultFormat = "exitctl.result/v1"

// SupportedCatalogSchemaMajors lists catalog schema majors this build can read.
var SupportedCatalogSchemaMajors = []int{1}
