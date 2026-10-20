package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/cloudowski/exitctl/internal/catalog"
)

func init() { register(newCatalogCmd) }

func newCatalogCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "catalog",
		Short: "Work with the knowledge catalog",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "validate [dir]",
		Short: "Validate a catalog directory (default: built-in, or --catalog)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := app.Opts.CatalogDir
			if len(args) == 1 {
				dir = args[0]
			}
			return app.runCatalogValidate(dir)
		},
	})
	return cmd
}

// runCatalogValidate prints every problem as "<file>:<line>  <entry-id>
// <problem>" (sorted by file, line), then entry counts per kind and status
// and the number of LLM-generated entries awaiting review (contracts/cli.md).
// Exit 0 when valid, 3 otherwise.
func (app *App) runCatalogValidate(dir string) error {
	fsys, name, err := catalogFS(dir)
	if err != nil {
		return err
	}
	cat, probs, err := catalog.LoadAndValidate(fsys)
	if err != nil {
		var se *catalog.SchemaError
		if errors.As(err, &se) {
			_, _ = fmt.Fprintf(app.Out, "%s  -  %s\n", se.File, se.Error())
			return Errorf(ExitInvalidInput, "%s is invalid: unsupported catalog schema", name)
		}
		return Errorf(ExitInvalidInput, "%s: %v", name, err)
	}
	for _, p := range probs {
		_, _ = fmt.Fprintln(app.Out, p.String())
	}
	if len(probs) > 0 {
		_, _ = fmt.Fprintln(app.Out)
	}
	writeCatalogCounts(app.Out, name, cat)
	if len(probs) > 0 {
		noun := "problems"
		if len(probs) == 1 {
			noun = "problem"
		}
		return Errorf(ExitInvalidInput, "%s is invalid: %d %s", name, len(probs), noun)
	}
	_, _ = fmt.Fprintf(app.Out, "\n%s is valid\n", name)
	return nil
}

// statusCounts counts entries of one kind by lifecycle status.
type statusCounts struct {
	kind                               string
	verified, draft, deprecated, other int
}

func (c *statusCounts) add(p catalog.Provenance, llm *int) {
	switch p.Status {
	case catalog.StatusVerified:
		c.verified++
	case catalog.StatusDraft:
		c.draft++
		if p.Origin == "llm" {
			*llm++
		}
	case catalog.StatusDeprecated:
		c.deprecated++
	default:
		c.other++
	}
}

func (c statusCounts) total() int { return c.verified + c.draft + c.deprecated + c.other }

func writeCatalogCounts(w io.Writer, name string, cat *catalog.Catalog) {
	m := cat.Manifest
	ver, schema := m.CatalogVersion, m.Schema
	if ver == "" {
		ver = "-"
	}
	if schema == "" {
		schema = "-"
	}
	_, _ = fmt.Fprintf(w, "%s  version %s (schema %s)\n\n", name, ver, schema)

	llm := 0
	rows := []statusCounts{{kind: "services"}, {kind: "traits"}, {kind: "alternatives"}, {kind: "neutral"}}
	for _, s := range cat.Services {
		rows[0].add(s.Provenance, &llm)
	}
	for _, t := range cat.Traits {
		rows[1].add(t.Provenance, &llm)
	}
	for _, a := range cat.Alternatives {
		rows[2].add(a.Provenance, &llm)
	}
	for _, n := range cat.Neutrals {
		rows[3].add(n.Provenance, &llm)
	}
	const rowFmt = "%-13s %9v %6v %11v %6v\n"
	_, _ = fmt.Fprintf(w, rowFmt, "kind", "verified", "draft", "deprecated", "total")
	for _, r := range rows {
		_, _ = fmt.Fprintf(w, rowFmt, r.kind, r.verified, r.draft, r.deprecated, r.total())
	}
	_, _ = fmt.Fprintf(w, "%-13s %34d\n", "providers", len(cat.Providers))
	_, _ = fmt.Fprintf(w, "%-13s %34d\n", "categories", len(cat.Categories))
	_, _ = fmt.Fprintf(w, "\nLLM-generated awaiting review: %d\n", llm)
}
