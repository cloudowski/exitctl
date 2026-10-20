// Package cli implements the exitctl commands (contracts/cli.md).
package cli

import (
	"fmt"
	"io"
	"os"
	"runtime/debug"

	"github.com/spf13/cobra"
)

// GlobalOptions are flags shared by all commands.
type GlobalOptions struct {
	CatalogDir    string
	ConfigPath    string
	InputPath     string
	NoColor       bool
	Emoji         string
	Verbose       bool
	Format        string
	IncludeDrafts bool
}

// DefaultInput is where scan stores its last result.
const DefaultInput = ".exitctl/last-result.json"

// App holds the streams and options of one invocation (testable without os).
type App struct {
	Out  io.Writer
	Err  io.Writer
	Opts GlobalOptions
}

// NewRootCmd builds the command tree.
func NewRootCmd(app *App) *cobra.Command {
	root := &cobra.Command{
		Use:           "exitctl",
		Short:         "Measure cloud vendor lock-in, offline and read-only",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(app.Out)
	root.SetErr(app.Err)
	f := root.PersistentFlags()
	f.StringVar(&app.Opts.CatalogDir, "catalog", "", "use a local catalog directory instead of the built-in catalog")
	f.StringVar(&app.Opts.ConfigPath, "config", "", "path to .exitctl.yaml (default: scan root, then current directory)")
	f.StringVar(&app.Opts.InputPath, "input", DefaultInput, "result file for explain, graph, coverage, report")
	f.BoolVar(&app.Opts.NoColor, "no-color", false, "disable color (also NO_COLOR)")
	f.StringVar(&app.Opts.Emoji, "emoji", "auto", "emoji markers: auto, always, never")
	f.BoolVarP(&app.Opts.Verbose, "verbose", "v", false, "print skipped and failed source details to stderr")
	f.BoolVar(&app.Opts.IncludeDrafts, "include-drafts", false, "score draft catalog entries (development only; results are marked unverified)")
	_ = f.MarkHidden("include-drafts")
	if os.Getenv("NO_COLOR") != "" {
		app.Opts.NoColor = true
	}
	for _, add := range subcommands {
		root.AddCommand(add(app))
	}
	return root
}

// subcommands is filled by init functions of command files.
var subcommands []func(*App) *cobra.Command

func register(f func(*App) *cobra.Command) { subcommands = append(subcommands, f) }

// Run executes exitctl with args and returns the exit code.
func Run(args []string, out, errw io.Writer) (code int) {
	app := &App{Out: out, Err: errw}
	defer func() {
		if r := recover(); r != nil {
			_, _ = fmt.Fprintln(errw, "exitctl: internal error, please report it")
			if app.Opts.Verbose {
				_, _ = fmt.Fprintf(errw, "%v\n%s", r, debug.Stack())
			}
			code = ExitInternal
		}
	}()
	root := NewRootCmd(app)
	root.SetArgs(args)
	err := root.Execute()
	if err != nil {
		_, _ = fmt.Fprintln(errw, "exitctl:", err)
	}
	return codeOf(err)
}

// Execute runs exitctl with os.Args and returns the exit code.
func Execute() int { return Run(os.Args[1:], os.Stdout, os.Stderr) }
