package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/cloudowski/exitctl/internal/config"
	"github.com/cloudowski/exitctl/internal/engine"
	"github.com/cloudowski/exitctl/internal/result"
)

func init() { register(newMergeCmd) }

func newMergeCmd(app *App) *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "merge <result.json>... -o <merged.json>",
		Short: "Merge results from several repositories and re-evaluate them",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runMerge(args, output)
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "", "merged result file (required)")
	return cmd
}

func (app *App) runMerge(paths []string, output string) error {
	if output == "" {
		return Errorf(ExitInvalidInput, "merge needs -o <merged.json>")
	}
	docs, err := result.LoadAll(paths)
	if err != nil {
		return Errorf(ExitInvalidInput, "%v", err)
	}
	ix, err := app.loadIndex()
	if err != nil {
		return err
	}
	cwd, _ := os.Getwd()
	cfg, err := config.Discover("", cwd, app.Opts.ConfigPath)
	if err != nil {
		return Errorf(ExitInvalidInput, "%v", err)
	}
	facts, ins, err := result.MergeFacts(docs, result.CatalogResolver(ix))
	if err != nil {
		return Errorf(ExitInvalidInput, "%v", err)
	}
	inputs := make([]engine.Input, 0, len(ins))
	for _, in := range ins {
		v := in.Versions
		inputs = append(inputs, engine.Input{Repo: in.Repo, AsOf: in.AsOf, Versions: &v})
	}
	an := engine.Analyze(facts, ix, cfg, inputs)
	if err := result.Save(an.Doc, output); err != nil {
		return Errorf(ExitInvalidInput, "cannot write %s: %v", output, err)
	}
	_, _ = fmt.Fprintf(app.Err, "merged %d results into %s\n", len(docs), output)
	return app.emitResult(an.Doc, ix, scanFlags{noStore: true, format: "text"})
}
