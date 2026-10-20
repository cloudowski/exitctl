package result

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/cloudowski/exitctl/internal/version"
	"github.com/cloudowski/exitctl/schemas"
)

// IncompatibleError is returned when a result file has an unsupported format.
type IncompatibleError struct {
	Path   string
	Format string
}

func (e *IncompatibleError) Error() string {
	return fmt.Sprintf("%s has result format %q; this exitctl reads %s", e.Path, e.Format, version.ResultFormat)
}

// Save writes the document to path, creating parent directories. When the
// parent directory is named ".exitctl", a .gitignore containing "*" is
// created next to it so results are never committed by accident.
func Save(d *Document, path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if filepath.Base(dir) == ".exitctl" {
		gi := filepath.Join(dir, ".gitignore")
		if _, err := os.Stat(gi); os.IsNotExist(err) {
			if err := os.WriteFile(gi, []byte("*\n"), 0o644); err != nil {
				return err
			}
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := Encode(f, d); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// Load reads and validates a result document.
func Load(path string) (*Document, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no result at %s: run 'exitctl scan' first or pass --input", path)
		}
		return nil, err
	}
	return Decode(path, b)
}

// Decode parses and validates a result document. name is used in messages.
func Decode(name string, b []byte) (*Document, error) {
	var head struct {
		Format string `json:"format"`
	}
	if err := json.Unmarshal(b, &head); err != nil {
		return nil, fmt.Errorf("%s is not a valid exitctl result file", name)
	}
	if head.Format != version.ResultFormat {
		major := strings.SplitN(head.Format, "/", 2)
		if len(major) != 2 || major[0] != "exitctl.result" {
			return nil, fmt.Errorf("%s is not an exitctl result file", name)
		}
		return nil, &IncompatibleError{Path: name, Format: head.Format}
	}
	if err := Validate(b); err != nil {
		return nil, fmt.Errorf("%s does not match the result schema: %v", name, err)
	}
	var d Document
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, fmt.Errorf("%s: %v", name, err)
	}
	return &d, nil
}

var (
	schemaOnce sync.Once
	schema     *jsonschema.Schema
	schemaErr  error
)

func resultSchema() (*jsonschema.Schema, error) {
	schemaOnce.Do(func() {
		raw, err := schemas.FS.ReadFile("result.schema.json")
		if err != nil {
			schemaErr = err
			return
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			schemaErr = err
			return
		}
		c := jsonschema.NewCompiler()
		if err := c.AddResource("result.schema.json", doc); err != nil {
			schemaErr = err
			return
		}
		schema, schemaErr = c.Compile("result.schema.json")
	})
	return schema, schemaErr
}

// Validate checks encoded JSON against the embedded result schema.
func Validate(b []byte) error {
	s, err := resultSchema()
	if err != nil {
		return err
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		return err
	}
	return s.Validate(inst)
}
