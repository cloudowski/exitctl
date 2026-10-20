package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/cloudowski/exitctl/internal/render/graphviz"
	"github.com/cloudowski/exitctl/internal/render/terminal"
	"github.com/cloudowski/exitctl/internal/result"
)

func init() { register(newGraphCmd) }

type graphFlags struct {
	blastRadius bool
	waves       bool
	format      string
	output      string
	expand      bool
}

func newGraphCmd(app *App) *cobra.Command {
	var f graphFlags
	cmd := &cobra.Command{
		Use:   "graph [resource]",
		Short: "Show or export the dependency graph",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runGraph(cmd.Context(), args, f)
		},
	}
	fl := cmd.Flags()
	fl.BoolVar(&f.blastRadius, "blast-radius", false, "with a resource: show only its dependents tree")
	fl.BoolVar(&f.waves, "waves", false, "label nodes and groups with their migration wave")
	fl.StringVar(&f.format, "format", "tree", "output format: tree, json, dot, svg, png")
	fl.StringVarP(&f.output, "output", "o", "", "output file (required for png; default exitctl-graph.<ext> for svg)")
	fl.BoolVar(&f.expand, "expand", false, "disable type collapse and container grouping")
	return cmd
}

func (app *App) runGraph(ctx context.Context, args []string, f graphFlags) error {
	if ctx == nil {
		ctx = context.Background()
	}
	switch f.format {
	case "tree", "json", "dot", "svg", "png":
	default:
		return Errorf(ExitInvalidInput, "unknown --format %q (use tree, json, dot, svg, or png)", f.format)
	}
	if f.blastRadius && len(args) == 0 {
		return Errorf(ExitInvalidInput, "--blast-radius needs a resource")
	}
	if f.format == "png" && f.output == "" {
		return Errorf(ExitInvalidInput, "--format png needs -o <file>")
	}
	doc, err := app.loadInput()
	if err != nil {
		return err
	}
	var target string
	if len(args) == 1 {
		n, _, err := resolveTarget(doc, args[0])
		if err != nil {
			return err
		}
		target = n.ID
	}

	var buf bytes.Buffer
	switch f.format {
	case "tree":
		t := terminal.TreeOptions{Expand: f.expand, Waves: f.waves}
		if target != "" {
			t.BlastRadius = target
		}
		return terminal.Tree(app.Out, doc, t, app.termOptions(nil, false))
	case "json":
		g := *doc
		g.Kind = result.KindGraph
		if err := result.Encode(&buf, &g); err != nil {
			return err
		}
	default:
		if target != "" {
			return Errorf(ExitInvalidInput, "a resource can only be used with --format tree")
		}
		if err := graphviz.DOT(&buf, doc, graphviz.Options{Expand: f.expand, Waves: f.waves}); err != nil {
			return err
		}
		if f.format != "dot" {
			img, err := graphviz.Image(ctx, buf.Bytes(), f.format)
			if err != nil {
				return Errorf(ExitInternal, "%v", err)
			}
			buf.Reset()
			buf.Write(img)
			if f.output == "" {
				f.output = "exitctl-graph." + f.format
			}
		}
	}
	if f.output == "" {
		_, err := app.Out.Write(buf.Bytes())
		return err
	}
	if err := os.WriteFile(f.output, buf.Bytes(), 0o644); err != nil {
		return Errorf(ExitInvalidInput, "cannot write %s: %v", f.output, err)
	}
	_, _ = fmt.Fprintln(app.Out, "wrote", f.output)
	return nil
}
