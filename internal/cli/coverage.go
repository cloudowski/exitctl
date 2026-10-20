package cli

import (
	"encoding/json"

	"github.com/spf13/cobra"

	"github.com/cloudowski/exitctl/internal/render/terminal"
)

func init() { register(newCoverageCmd) }

func newCoverageCmd(app *App) *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "coverage",
		Short: "List unknown types, unresolved inputs, unverified matches, and parse failures",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runCoverage(format)
		},
	}
	cmd.Flags().StringVar(&format, "format", "text", "output format: text or json")
	return cmd
}

func (app *App) runCoverage(format string) error {
	if format != "text" && format != "json" {
		return Errorf(ExitInvalidInput, "unknown --format %q (use text or json)", format)
	}
	doc, err := app.loadInput()
	if err != nil {
		return err
	}
	v := terminal.BuildCoverage(doc)
	if format == "json" {
		enc := json.NewEncoder(app.Out)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	return terminal.Coverage(app.Out, v, app.termOptions(nil, false))
}
