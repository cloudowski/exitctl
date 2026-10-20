package cli

import (
	"bytes"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/cloudowski/exitctl/internal/catalog"
	"github.com/cloudowski/exitctl/internal/render/html"
	"github.com/cloudowski/exitctl/internal/render/markdown"
	"github.com/cloudowski/exitctl/internal/result"
	"github.com/cloudowski/exitctl/internal/version"
)

func init() { register(newReportCmd) }

func newReportCmd(app *App) *cobra.Command {
	var format, output string
	var open bool
	cmd := &cobra.Command{
		Use:   "report",
		Short: "Write a shareable report (HTML, Markdown, or JSON)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runReport(format, output, open)
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&format, "format", "html", "report format: html, md, or json")
	fl.StringVarP(&output, "output", "o", "", "output file (default exitctl-report.<ext>)")
	fl.BoolVar(&open, "open", false, "open the written report with the default application")
	return cmd
}

func (app *App) runReport(format, output string, open bool) error {
	var ext string
	switch format {
	case "html":
		ext = "html"
	case "md", "markdown":
		format, ext = "md", "md"
	case "json":
		ext = "json"
	default:
		return Errorf(ExitInvalidInput, "unknown --format %q (use html, md, or json)", format)
	}
	doc, err := app.loadInput()
	if err != nil {
		return err
	}
	// Display names are a nicety: reports still render without a usable catalog.
	var names map[string]string
	if ix, err := app.loadIndex(); err == nil {
		names = displayNames(ix)
	}
	var buf bytes.Buffer
	switch format {
	case "html":
		err = html.Render(&buf, doc, html.Options{Names: names, SupportEmail: version.SupportEmail})
	case "md":
		err = markdown.Render(&buf, doc, markdown.Options{Names: names, SupportEmail: version.SupportEmail})
	default:
		err = result.Encode(&buf, doc)
	}
	if err != nil {
		return Errorf(ExitInternal, "cannot render the report: %v", err)
	}
	if output == "" {
		output = "exitctl-report." + ext
	}
	if err := os.WriteFile(output, buf.Bytes(), 0o644); err != nil {
		return Errorf(ExitInvalidInput, "cannot write %s: %v", output, err)
	}
	_, _ = fmt.Fprintln(app.Out, "wrote", output)
	if open {
		if err := openFile(output); err != nil {
			_, _ = fmt.Fprintln(app.Err, "exitctl:", err)
		}
	}
	return nil
}

// displayNames maps service, alternative, and trait IDs to display names.
func displayNames(ix *catalog.Index) map[string]string {
	c := ix.Catalog()
	m := make(map[string]string, len(c.Services)+len(c.Alternatives)+len(c.Traits))
	for _, s := range c.Services {
		m[s.ID] = s.Name
	}
	for _, a := range c.Alternatives {
		m[a.ID] = a.Name
	}
	for _, t := range c.Traits {
		m[t.ID] = t.Name
	}
	return m
}
