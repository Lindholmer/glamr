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
