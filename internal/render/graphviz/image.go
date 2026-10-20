package graphviz

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sync"

	gv "github.com/goccy/go-graphviz"
	"github.com/goccy/go-graphviz/gvc"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
)

// ErrRender is returned when Graphviz cannot render a graph. The message is
// neutral on purpose: library internals are never shown to users.
var ErrRender = errors.New("the graph image could not be rendered")

// renderMu serializes rendering: the WASM Graphviz instance is shared state.
var renderMu sync.Mutex

var sandboxOnce sync.Once

// sandbox detaches the WASM module from the host file system (by default it
// sees "/" through os.Open), so DOT attributes such as image= cannot read
// local files, and makes PNG text use the embedded Go Regular font instead of
// searching the host for font files, so images do not depend on the machine.
func sandbox() {
	sandboxOnce.Do(func() {
		gv.SetFileSystem(emptyFS{})
		gvc.SetFontLoader(embeddedFont)
	})
}

// embeddedFont returns the Go Regular face at the requested size.
func embeddedFont(_ context.Context, job *gvc.Job, f *gvc.TextFont) (font.Face, error) {
	ft, err := opentype.Parse(goregular.TTF)
	if err != nil {
		return nil, err
	}
	return opentype.NewFace(ft, &opentype.FaceOptions{Size: f.Size() * job.Zoom(), DPI: 72})
}

// emptyFS is a file system without files.
type emptyFS struct{}

func (emptyFS) Open(name string) (fs.File, error) {
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

// Image renders DOT source to an image. format is "png" or "svg".
func Image(ctx context.Context, dot []byte, format string) (out []byte, err error) {
	var f gv.Format
	switch format {
	case "png":
		f = gv.PNG
	case "svg":
		f = gv.SVG
	default:
		return nil, fmt.Errorf("unsupported image format %q (use png or svg)", format)
	}
	sandbox()
	renderMu.Lock()
	defer renderMu.Unlock()
	defer func() {
		if r := recover(); r != nil {
			out, err = nil, ErrRender
		}
	}()

	g, err := gv.New(ctx)
	if err != nil {
		return nil, ErrRender
	}
	defer func() { _ = g.Close() }()
	graph, err := gv.ParseBytes(dot)
	if err != nil || graph == nil {
		return nil, ErrRender
	}
	defer func() { _ = graph.Close() }()
	var buf bytes.Buffer
	if err := g.Render(ctx, graph, f, &buf); err != nil || buf.Len() == 0 {
		return nil, ErrRender
	}
	return buf.Bytes(), nil
}
