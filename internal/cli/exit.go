package cli

import (
	"errors"
	"fmt"
)

// Exit codes (contracts/cli.md "Exit codes").
const (
	ExitOK           = 0
	ExitNothing      = 2 // nothing to scan / nothing scored
	ExitInvalidInput = 3 // bad flags, invalid config, invalid catalog, incompatible result file
	ExitInternal     = 4
)

// ExitError carries an exit code and a user-facing message.
type ExitError struct {
	Code int
	Msg  string
}

func (e *ExitError) Error() string { return e.Msg }

// Errorf returns an ExitError with a formatted message.
func Errorf(code int, format string, a ...any) error {
	return &ExitError{Code: code, Msg: fmt.Sprintf(format, a...)}
}

// codeOf maps an error to an exit code. Cobra flag errors are invalid input.
func codeOf(err error) int {
	if err == nil {
		return ExitOK
	}
	var ee *ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	return ExitInvalidInput
}
