package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rogpeppe/go-internal/testscript"

	"github.com/cloudowski/exitctl/internal/testutil"
)

// TestMain lets testscript run "exitctl" in-process.
func TestMain(m *testing.M) {
	testscript.Main(m, map[string]func(){
		"exitctl": func() { os.Exit(Execute()) },
	})
}

// TestScript runs the CLI scenarios in testdata/script (contracts/cli.md).
// $FIXTURES points at testdata/fixtures, $CATALOGS at testdata/catalogs.
func TestScript(t *testing.T) {
	root := testutil.RepoRoot(t)
	testscript.Run(t, testscript.Params{
		Dir: filepath.Join("testdata", "script"),
		Setup: func(env *testscript.Env) error {
			env.Setenv("FIXTURES", filepath.Join(root, "testdata", "fixtures"))
			env.Setenv("CATALOGS", filepath.Join(root, "testdata", "catalogs"))
			env.Setenv("NO_COLOR", "")
			env.Setenv("LANG", "en_US.UTF-8")
			return nil
		},
	})
}
