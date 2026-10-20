package cli

import (
	"fmt"
	"io/fs"
	"os"
	"strings"

	catalogdata "github.com/cloudowski/exitctl/catalog"
	"github.com/cloudowski/exitctl/internal/catalog"
	"github.com/cloudowski/exitctl/internal/render/terminal"
	"github.com/cloudowski/exitctl/internal/result"
)

// loadIndex loads the built-in or --catalog catalog, validates it, and
// builds the index. Any validation problem makes the catalog unusable.
func (app *App) loadIndex() (*catalog.Index, error) {
	fsys, name, err := catalogFS(app.Opts.CatalogDir)
	if err != nil {
		return nil, err
	}
	cat, probs, err := catalog.LoadAndValidate(fsys)
	if err != nil {
		return nil, Errorf(ExitInvalidInput, "%s: %v", name, err)
	}
	if len(probs) > 0 {
		var b strings.Builder
		fmt.Fprintf(&b, "%s is invalid (%d problems); run 'exitctl catalog validate' for details", name, len(probs))
		if app.Opts.Verbose {
			for _, p := range probs {
				b.WriteString("\n  " + p.String())
			}
		}
		return nil, Errorf(ExitInvalidInput, "%s", b.String())
	}
	return catalog.NewIndex(cat, catalog.IndexOptions{IncludeDrafts: app.Opts.IncludeDrafts}), nil
}

// catalogFS returns the catalog tree to load: the built-in catalog when dir
// is empty, else os.DirFS(dir). name is used in messages.
func catalogFS(dir string) (fsys fs.FS, name string, err error) {
	if dir == "" {
		return catalogdata.FS, "built-in catalog", nil
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return nil, "", Errorf(ExitInvalidInput, "catalog directory %s not found", dir)
	}
	return os.DirFS(dir), dir, nil
}

// termOptions returns terminal rendering options for stdout, with display
// names for services and alternatives when an index is available.
func (app *App) termOptions(ix *catalog.Index, all bool) terminal.Options {
	o := terminal.DetectOptions(app.Opts.NoColor, app.Opts.Emoji, app.Out)
	o.All = all
	if ix != nil {
		o.Names = map[string]string{}
		for _, s := range ix.Catalog().Services {
			o.Names[s.ID] = s.Name
		}
		for _, a := range ix.Catalog().Alternatives {
			o.Names[a.ID] = a.Name
		}
	}
	return o
}

// loadInput loads the --input result file (default: last scan).
func (app *App) loadInput() (*result.Document, error) {
	d, err := result.Load(app.Opts.InputPath)
	if err != nil {
		if _, ok := err.(*result.IncompatibleError); ok {
			return nil, Errorf(ExitInvalidInput, "%v", err)
		}
		if os.IsNotExist(err) {
			return nil, Errorf(ExitInvalidInput, "no result found at %s: run 'exitctl scan' first", app.Opts.InputPath)
		}
		return nil, Errorf(ExitInvalidInput, "%v", err)
	}
	return d, nil
}
