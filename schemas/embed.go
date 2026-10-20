// Package schemas embeds the published JSON schemas (result, catalog entry, config).
package schemas

import "embed"

// FS holds result.schema.json, catalog-entry.schema.json and config.schema.json.
//
//go:embed *.json
var FS embed.FS
