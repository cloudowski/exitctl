// Package catalogdata embeds the built-in exitctl knowledge catalog (data
// only; loading and validation live in internal/catalog).
package catalogdata

import "embed"

// FS holds the built-in catalog tree: catalog.yaml, providers.yaml,
// categories.yaml, NEUTRALITY.md and the traits, services, alternatives and
// neutral directories.
//
//go:embed catalog.yaml providers.yaml categories.yaml NEUTRALITY.md all:traits all:services all:alternatives all:neutral
var FS embed.FS
