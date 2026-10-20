package catalog

import yaml "go.yaml.in/yaml/v3"

// migrations upgrades a decoded catalog file from schema major N to N+1
// (FR-011). Each function is pure over the decoded YAML tree and must also
// update the file's `schema` value. The registry is empty in v0.1 because
// schema major 1 is the first published major.
var migrations = map[int]func(*yaml.Node) error{}
