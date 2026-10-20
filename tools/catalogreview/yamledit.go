package main

import (
	"bytes"
	"fmt"
	"os"

	yaml "go.yaml.in/yaml/v3"
)

// doc is one catalog file edited through the yaml.v3 node tree, so that key
// order, flow style, and quoting of untouched values are preserved.
type doc struct {
	path string
	orig []byte
	root yaml.Node
}

func loadDoc(path string) (*doc, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	d := &doc{path: path, orig: b}
	if err := yaml.Unmarshal(b, &d.root); err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	if d.top() == nil {
		return nil, fmt.Errorf("%s: not a YAML mapping", path)
	}
	return d, nil
}

// top returns the top-level mapping of the file.
func (d *doc) top() *yaml.Node {
	if d.root.Kind != yaml.DocumentNode || len(d.root.Content) == 0 || d.root.Content[0].Kind != yaml.MappingNode {
		return nil
	}
	return d.root.Content[0]
}

// encode writes the document in the catalog's style: 2-space indent with
// sequences not indented under their key.
func (d *doc) encode() ([]byte, error) {
	var buf bytes.Buffer
	e := yaml.NewEncoder(&buf)
	e.SetIndent(2)
	e.CompactSeqIndent()
	if err := e.Encode(&d.root); err != nil {
		return nil, err
	}
	if err := e.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func mapGet(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// mapSet replaces the value of key, or inserts the key after the key named
// after (appending when after is "" or missing).
func mapSet(m *yaml.Node, key string, val *yaml.Node, after string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = val
			return
		}
	}
	k := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	pos := len(m.Content)
	if after != "" {
		for i := 0; i+1 < len(m.Content); i += 2 {
			if m.Content[i].Value == after {
				pos = i + 2
			}
		}
	}
	m.Content = append(m.Content[:pos], append([]*yaml.Node{k, val}, m.Content[pos:]...)...)
}

func mapDel(m *yaml.Node, key string) bool {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return true
		}
	}
	return false
}

func scalar(m *yaml.Node, key string) string {
	if v := mapGet(m, key); v != nil && v.Kind == yaml.ScalarNode {
		return v.Value
	}
	return ""
}

func strNode(s string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
}

// quotedNode is free text, always single-quoted so that commas, colons, or
// leading symbols can never change the YAML structure (inside flow mappings
// an unquoted ", " would start a new key).
func quotedNode(s string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s, Style: yaml.SingleQuotedStyle}
}

// dateNode is a quoted date, so YAML keeps it a string (not a timestamp).
func dateNode(s string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s, Style: yaml.SingleQuotedStyle}
}

func boolNode(b bool) *yaml.Node {
	v := "false"
	if b {
		v = "true"
	}
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: v}
}

// approveMappings records the reviewer as approver of every alternative
// mapping of a service that has no approver yet (DM 1.8: approved_by is
// required for verified mappings).
func approveMappings(top *yaml.Node, reviewer string) {
	seq := mapGet(top, "alternatives")
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return
	}
	for _, it := range seq.Content {
		if it.Kind == yaml.MappingNode && scalar(it, "approved_by") == "" {
			mapSet(it, "approved_by", strNode(reviewer), "")
		}
	}
}

// mapping returns the alternative mapping item with the given ref in a
// service document, or nil.
func mapping(top *yaml.Node, ref string) *yaml.Node {
	seq := mapGet(top, "alternatives")
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return nil
	}
	for _, it := range seq.Content {
		if scalar(it, "ref") == ref {
			return it
		}
	}
	return nil
}
