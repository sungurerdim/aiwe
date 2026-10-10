package core

import (
	"os"
	"strings"
)

// secretMarkers are the name parts that make a variable secret-shaped (DR-35).
func secretMarkers() []string {
	return []string{"TOKEN", "SECRET", "KEY", "PASSWORD"}
}

// CleanEnv returns a copy of parent for a child process without secret-shaped variables, the
// listed credentials, a git credential helper, or a variable that reaches a credential or a
// program outside the run (DR-35). An entry with an empty name is dropped too. Names compare
// without case, as Windows does. git is told to skip the system config, never to prompt, and to use an empty
// credential.helper, which clears every helper set in a config file.
func CleanEnv(parent, credentials []string) []string {
	child := make([]string, 0, len(parent)+5)
	for _, kv := range parent {
		name, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if name == "" || dropFromChild(strings.ToUpper(name), credentials) {
			continue
		}
		child = append(child, kv)
	}
	return append(child,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=credential.helper",
		"GIT_CONFIG_VALUE_0=",
	)
}

// dropFromChild reports whether the upper-case variable name must not reach a child.
func dropFromChild(upper string, credentials []string) bool {
	for _, m := range secretMarkers() {
		if strings.Contains(upper, m) {
			return true
		}
	}
	for _, c := range credentials {
		if strings.EqualFold(upper, c) {
			return true
		}
	}
	for _, r := range reachNames() {
		if upper == r {
			return true
		}
	}
	return strings.HasPrefix(upper, "GIT_CONFIG_KEY_") || strings.HasPrefix(upper, "GIT_CONFIG_VALUE_")
}

// reachNames are the upper-case variable names that let a child reach a credential or run a
// program outside the run, or that CleanEnv sets itself (DR-35, SPEC §6.5).
func reachNames() []string {
	return []string{
		"GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT", "GIT_CONFIG_NOSYSTEM", "GIT_TERMINAL_PROMPT",
		"GIT_ASKPASS", "SSH_ASKPASS", "SSH_AUTH_SOCK", "VSCODE_GIT_IPC_HANDLE",
		"GIT_SSH_COMMAND", "GIT_SSH", "GIT_EXEC_PATH", "GIT_PROXY_COMMAND",
	}
}

// processGetenv reads one variable of the process environment; this file is the only reader
// (SPEC §5.4).
func processGetenv(name string) string {
	return os.Getenv(name)
}

// processHome returns the user's home directory of this process, or "" when it is unknown.
func processHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}
