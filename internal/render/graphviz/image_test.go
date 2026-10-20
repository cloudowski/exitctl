package graphviz

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"testing"
)

func sampleDOT(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := DOT(&buf, groupedDoc(), Options{Waves: true}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestImagePNG(t *testing.T) {
	out, err := Image(context.Background(), sampleDOT(t), "png")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(out, []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatalf("not a PNG: % x", out[:min(8, len(out))])
	}
}

func TestImageSVG(t *testing.T) {
	out, err := Image(context.Background(), sampleDOT(t), "svg")
	if err != nil {
		t.Fatal(err)
	}
	dec := xml.NewDecoder(bytes.NewReader(out))
	dec.Strict = false
	sawSVG := false
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("SVG does not parse as XML: %v", err)
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "svg" {
			sawSVG = true
		}
	}
	if !sawSVG {
		t.Fatal("no <svg> element")
	}
}

func TestImageErrors(t *testing.T) {
	if _, err := Image(context.Background(), []byte("digraph {"), "svg"); !errors.Is(err, ErrRender) {
		t.Errorf("broken DOT: err = %v, want ErrRender", err)
	}
	if _, err := Image(context.Background(), sampleDOT(t), "gif"); err == nil {
		t.Error("unsupported format accepted")
	}
}
