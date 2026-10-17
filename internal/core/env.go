package core

import (
	"os"
	"strings"
)

// secretMarkers are the name parts that make a variable secret-shaped (DR-10).
func secretMarkers() []string {
	return []string{"TOKEN", "SECRET", "KEY", "PASSWORD"}
}

// secretSuffixes are the name endings that make a variable secret-shaped (DR-10, SPEC §6.5):
// a token, a password, or a DSN, whose key always rides in the user part of its value.
func secretSuffixes() []string {
	return []string{"_PAT", "_PASS", "_PASSWD", "_DSN"}
}

// CleanEnv returns a copy of parent for a child process without secret-shaped variables, the
// listed credentials, a value that carries a credential URL, connection URI or proxy, a git
// credential helper, or a variable that reaches a credential or a program outside the run
// (DR-10). An entry with an empty name is dropped too. Names compare without case, as Windows
// does. git is told to skip the system config, to read the null device as its global config, so
// no user-level setting such as core.askPass applies, never to prompt, and to use an empty
// credential.helper, which clears every helper set in a config file.
func CleanEnv(parent, credentials []string) []string {
	child := make([]string, 0, len(parent)+6)
	for _, kv := range parent {
		name, value, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		upper := strings.ToUpper(name)
		if name == "" || dropFromChild(upper, credentials) || urlWithPassword(upper, value) {
			continue
		}
		child = append(child, kv)
	}
	return append(child,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=credential.helper",
		"GIT_CONFIG_VALUE_0=",
	)
}

// dropFromChild reports whether the upper-case variable name must not reach a child.
func dropFromChild(upper string, credentials []string) bool {
	if secretName(upper) {
		return true
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

// secretName reports whether the upper-case variable name is secret-shaped: it holds a secret
// marker, ends in a secret suffix, or is a known credential variable (DR-10, SPEC §6.5).
func secretName(upper string) bool {
	for _, m := range secretMarkers() {
		if strings.Contains(upper, m) {
			return true
		}
	}
	for _, s := range secretSuffixes() {
		if strings.HasSuffix(upper, s) {
			return true
		}
	}
	return upper == "NPM_CONFIG__AUTH" || upper == "GOOGLE_APPLICATION_CREDENTIALS"
}

// urlWithPassword reports whether an upper-case variable name may carry a credential in its
// value: a URL, a connection URI or a proxy, whose value holds user info with a password or a
// token as its only user part (DR-10, SPEC §6.5). CleanEnv and config redaction share the check.
// The password check is fail-closed, so a passwordless URL with a ":" and a later "@" drops too.
func urlWithPassword(upper, value string) bool {
	if !credentialValueName(upper) {
		return false
	}
	rest := afterScheme(value)
	if _, afterColon, ok := strings.Cut(rest, ":"); ok && strings.Contains(afterColon, "@") {
		return true
	}
	user, _, ok := strings.Cut(rest, "@")
	return ok && tokenUser(user)
}

// credentialValueName reports whether an upper-case variable name may carry a credential in its
// value: a URL, a connection URI, or a proxy, every name ending in PROXY, which covers
// HTTPS_PROXY and GOPROXY (DR-10, SPEC §6.5).
func credentialValueName(upper string) bool {
	return strings.HasSuffix(upper, "_URL") || strings.HasSuffix(upper, "_URI") ||
		strings.HasSuffix(upper, "PROXY")
}

// afterScheme drops the scheme of a value: the text up to and including the first ":" that "//"
// follows, and every "/" or "\" after it. So "https://u@h" and "mongodb+srv://u@h" come back as
// "u@h". Any other value comes back unchanged, so its first ":" still counts as user info: a
// schemeless "svc:pw@host", "svc:/pw@host" or "svc:\pw@host" drops, and so does an
// "https:/svc:pw@h" or "https:\svc:pw@h", which both readings drop (SPEC §6.5).
func afterScheme(value string) string {
	colon := strings.Index(value, ":")
	if colon < 0 || !strings.HasPrefix(value[colon+1:], "//") {
		return value
	}
	return strings.TrimLeft(value[colon+1:], "/\\")
}

// tokenUser reports whether a URL user part is a token, not a user name: it starts with a known
// token prefix (DR-10, SPEC §6.5). A user part with a password never reaches this check.
func tokenUser(user string) bool {
	for _, p := range tokenPrefixes() {
		if strings.HasPrefix(user, p) {
			return true
		}
	}
	return false
}

// tokenPrefixes are the prefixes that mark the user part of a URL as a credential rather than a
// user name (DR-10, SPEC §6.5).
func tokenPrefixes() []string {
	return []string{
		"ghp_", "gho_", "ghu_", "ghs_", "ghr_", "github_pat_",
		"glpat-", "glft-", "xoxb-", "xoxp-", "sk-", "npm_", "AKIA",
	}
}

// reachNames are the upper-case variable names that let a child reach a credential or run a
// program outside the run, or that CleanEnv sets itself (DR-10, SPEC §6.5).
func reachNames() []string {
	return []string{
		"GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT", "GIT_CONFIG_NOSYSTEM", "GIT_CONFIG_GLOBAL",
		"GIT_TERMINAL_PROMPT",
		"GIT_ASKPASS", "SSH_ASKPASS", "SSH_AUTH_SOCK", "VSCODE_GIT_IPC_HANDLE",
		"GIT_SSH_COMMAND", "GIT_SSH", "GIT_EXEC_PATH", "GIT_PROXY_COMMAND",
	}
}

// LookupEnv reads one variable of the process environment; it is the env lookup passed to
// ResolveConfig and ResolveRoots, so this file stays the only reader (SPEC §5.3). An unset
// variable reads as "".
func LookupEnv(name string) string {
	return os.Getenv(name)
}

// Environ returns a copy of the whole process environment as "name=value" pairs; with LookupEnv it
// keeps this file the only reader (SPEC §5.3).
func Environ() []string {
	return os.Environ()
}

// processHome returns the user's home directory of this process, or "" when it is unknown.
func processHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}
