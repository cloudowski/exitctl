package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/cloudowski/exitctl/internal/version"
)

func init() { register(newVersionCmd) }

// versionJSON is the --format json shape of version.
type versionJSON struct {
	Tool          string `json:"tool"`
	Catalog       string `json:"catalog"`
	CatalogSchema string `json:"catalog_schema"`
	Rules         string `json:"rules"`
	ResultFormat  string `json:"result_format"`
}

func newVersionCmd(app *App) *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the tool, catalog, rules, and result format versions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runVersion(format)
		},
	}
	cmd.Flags().StringVar(&format, "format", "text", "output format: text or json")
	return cmd
}

func (app *App) runVersion(format string) error {
	if format != "text" && format != "json" {
		return Errorf(ExitInvalidInput, "unknown --format %q (use text or json)", format)
	}
	ix, err := app.loadIndex()
	if err != nil {
		return err
	}
	m := ix.Catalog().Manifest
	v := versionJSON{Tool: version.Tool, Catalog: m.CatalogVersion, CatalogSchema: m.Schema,
		Rules: version.RulesVersion, ResultFormat: version.ResultFormat}
	if format == "json" {
		enc := json.NewEncoder(app.Out)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	_, _ = fmt.Fprintf(app.Out, "exitctl        %s\n", v.Tool)
	_, _ = fmt.Fprintf(app.Out, "catalog        %s (schema %s)\n", v.Catalog, v.CatalogSchema)
	_, _ = fmt.Fprintf(app.Out, "rules          %s\n", v.Rules)
	_, _ = fmt.Fprintf(app.Out, "result format  %s\n", v.ResultFormat)
	return nil
}
