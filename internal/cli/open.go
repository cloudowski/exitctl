package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// openFile opens a local file with the OS default handler. This is the only
// os/exec use in exitctl (depguard excludes this file). It only accepts an
// existing regular file path, never a URL.
func openFile(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	st, err := os.Stat(abs)
	if err != nil || !st.Mode().IsRegular() {
		return fmt.Errorf("%s is not a local file", path)
	}
	var name string
	switch runtime.GOOS {
	case "darwin":
		name = "open"
	case "linux":
		name = "xdg-open"
	default:
		return fmt.Errorf("--open is not supported on %s; open %s manually", runtime.GOOS, abs)
	}
	cmd := exec.Command(name, abs)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("cannot open %s: %v", abs, err)
	}
	return cmd.Process.Release()
}
