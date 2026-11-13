package provider

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// installFakeGlab puts a fake `glab` executable first on PATH for the duration of the test.
// script is the body of a POSIX shell script; every invocation's arguments are appended,
// one invocation per line (newlines inside arguments flattened), to the returned log file.
func installFakeGlab(t *testing.T, script string) (logPath string) {
	t.Helper()
	return installFakeCLI(t, "glab", script)
}

// installFakeGh is installFakeGlab for the GitHub CLI.
func installFakeGh(t *testing.T, script string) (logPath string) {
	t.Helper()
	return installFakeCLI(t, "gh", script)
}

func installFakeCLI(t *testing.T, name, script string) (logPath string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake glab requires a POSIX shell")
	}

	dir := t.TempDir()
	logPath = filepath.Join(dir, "glab.log")
	// One write per call (O_APPEND), so concurrent invocations don't interleave log lines
	content := "#!/bin/sh\n" +
		"line=$(printf '%s\\n' \"$*\" | tr '\\n' ' ')\n" +
		"printf '%s\\n' \"$line\" >> \"$GLAB_LOG\"\n" +
		script + "\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GLAB_LOG", logPath)
	return logPath
}

// glabCalls returns the logged invocations of a fake CLI.
func glabCalls(t *testing.T, logPath string) []string {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	return strings.Split(strings.TrimRight(string(data), "\n"), "\n")
}
