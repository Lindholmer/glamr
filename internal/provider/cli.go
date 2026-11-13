package provider

import (
	"context"
	"os/exec"
)

// cliCommand builds an `api` invocation of a forge CLI (glab or gh). Both accept
// `--hostname` after `api`; without a host the CLI's default/authenticated host is used.
func cliCommand(ctx context.Context, bin, host string, args ...string) *exec.Cmd {
	if host != "" && len(args) > 0 && args[0] == "api" {
		args = append([]string{"api", "--hostname", host}, args[1:]...)
	}
	return exec.CommandContext(ctx, bin, args...)
}

// cliError is a failed CLI call that keeps its stdout, where glab and gh print the API's
// error body (see commandError).
type cliError struct {
	err    error
	stdout []byte
}

func (e *cliError) Error() string { return e.err.Error() }
func (e *cliError) Unwrap() error { return e.err }

// runCLI runs cmd and returns its stdout. On failure the error keeps stdout for commandError.
func runCLI(cmd *exec.Cmd) ([]byte, error) {
	output, err := cmd.Output()
	if err != nil {
		return output, &cliError{err: err, stdout: output}
	}
	return output, nil
}
