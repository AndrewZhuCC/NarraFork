package agentbasic

import (
	"os"
)

// BuildMinimalBashEnv builds the minimal environment used by the Bash tool's
// "fresh shell env" mode, mirroring the TS buildMinimalEnv (server/lib/agent/
// shell.ts:332-348). Only essential variables are kept so that the login shell's
// profile scripts repopulate the rest from a clean slate. PATH is retained so the
// shell binary and basic utilities can still be found during initialization.
//
// The result is in exec.Cmd.Env form ("KEY=VALUE" entries). HOME falls back to
// os.UserHomeDir() (matching the TS getHome()), and TERM/LANG default to the same
// values TS uses when the parent process does not set them.
func BuildMinimalBashEnv() []string {
	home := os.Getenv("HOME")
	if home == "" {
		if h, err := os.UserHomeDir(); err == nil {
			home = h
		}
	}
	term := os.Getenv("TERM")
	if term == "" {
		term = "xterm-256color"
	}
	lang := os.Getenv("LANG")
	if lang == "" {
		lang = "en_US.UTF-8"
	}
	env := []string{
		"HOME=" + home,
		"TERM=" + term,
		"LANG=" + lang,
		"USER=" + os.Getenv("USER"),
	}
	// PATH is essential for the shell to find itself and basic utilities.
	if pathVal := os.Getenv("PATH"); pathVal != "" {
		env = append(env, "PATH="+pathVal)
	}
	return env
}
