package core_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sungurerdim/aiwe/internal/core"
)

// envCleanLookup returns the value of name in env and whether it is present; names compare
// without case, as Windows does.
func envCleanLookup(env []string, name string) (string, bool) {
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if ok && strings.EqualFold(k, name) {
			return v, true
		}
	}
	return "", false
}

// envCleanHelpers returns every credential.helper value git reads from the GIT_CONFIG_COUNT,
// GIT_CONFIG_KEY_<n> and GIT_CONFIG_VALUE_<n> variables of env.
func envCleanHelpers(t *testing.T, env []string) []string {
	t.Helper()
	countText, ok := envCleanLookup(env, "GIT_CONFIG_COUNT")
	if !ok {
		return nil
	}
	count, err := strconv.Atoi(countText)
	if err != nil {
		t.Fatalf("GIT_CONFIG_COUNT=%q is not a number", countText)
	}
	var helpers []string
	for i := range count {
		key, _ := envCleanLookup(env, "GIT_CONFIG_KEY_"+strconv.Itoa(i))
		if strings.EqualFold(key, "credential.helper") {
			value, _ := envCleanLookup(env, "GIT_CONFIG_VALUE_"+strconv.Itoa(i))
			helpers = append(helpers, value)
		}
	}
	return helpers
}

func TestCleanEnv(t *testing.T) {
	parent := []string{
		"PATH=/usr/local/bin:/usr/bin:/bin",
		"LANG=tr_TR.UTF-8",
		"GOPATH=/home/zeynep/go",
		"PROJECT_NOTE=TOKEN rotation is due on 2026-11-01",
		"USER_DISPLAY=Zeynep Çağlar",
		"GH_TOKEN=ghp_7Hq2xKfLm9Pz3RtWvYb4Nc8Jd1Ae5Gs6Uo0I",
		"GITHUB_TOKEN=ghs_Qw3Er5Ty7Ui9Op1As3Df5Gh7Jk9Lz2Xc4V",
		"github_token=ghs_lowercase0Name1Windows2Reads3It4",
		"AWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI/K7MDENG/bPxRfiCYzEXAMPLEKEY",
		"B_API_KEY=sk-proj-4f9a2c7e1b8d3f6a0e5c9b2d7f1a4e8c",
		"PGPASSWORD=correct-horse-battery-staple",
		"NPM_CONFIG_Secret=npm_cfg_9d8c7b6a",
		"MISTRAL_API_AUTH=mst-2b4d6f8h0j2l4n6p8r0t",
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=credential.helper",
		"GIT_CONFIG_VALUE_0=manager",
		"GIT_CONFIG_PARAMETERS='credential.helper'='store'",
	}
	credentials := []string{"MISTRAL_API_AUTH"}
	child := core.CleanEnv(parent, credentials)

	tests := []struct {
		name     string
		variable string
		// want is the value the child must carry; dropped means the variable is absent.
		want    string
		dropped bool
	}{
		{name: "PATH lost from the child", variable: "PATH", want: "/usr/local/bin:/usr/bin:/bin"},
		{name: "locale lost from the child", variable: "LANG", want: "tr_TR.UTF-8"},
		{name: "non-secret variable lost from the child", variable: "GOPATH", want: "/home/zeynep/go"},
		{name: "variable dropped because its value, not its name, holds a marker",
			variable: "PROJECT_NOTE", want: "TOKEN rotation is due on 2026-11-01"},
		{name: "Unicode value changed on the way to the child", variable: "USER_DISPLAY", want: "Zeynep Çağlar"},
		{name: "tracker token GH_TOKEN kept", variable: "GH_TOKEN", dropped: true},
		{name: "tracker token GITHUB_TOKEN kept", variable: "GITHUB_TOKEN", dropped: true},
		{name: "lower-case token name kept, which Windows reads as GITHUB_TOKEN", variable: "github_token", dropped: true},
		{name: "name with SECRET and KEY kept", variable: "AWS_SECRET_ACCESS_KEY", dropped: true},
		{name: "name with KEY kept", variable: "B_API_KEY", dropped: true},
		{name: "name with PASSWORD kept", variable: "PGPASSWORD", dropped: true},
		{name: "mixed-case marker Secret kept", variable: "NPM_CONFIG_Secret", dropped: true},
		{name: "listed credential without a marker in its name kept", variable: "MISTRAL_API_AUTH", dropped: true},
		{name: "git credential helper from GIT_CONFIG_PARAMETERS kept", variable: "GIT_CONFIG_PARAMETERS", dropped: true},
		{name: "system git config, which can name a credential helper, still read",
			variable: "GIT_CONFIG_NOSYSTEM", want: "1"},
		{name: "git allowed to ask for credentials on a terminal", variable: "GIT_TERMINAL_PROMPT", want: "0"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := envCleanLookup(child, tc.variable)
			if tc.dropped {
				if ok {
					t.Errorf("child keeps %s=%q; want it dropped", tc.variable, got)
				}
				return
			}
			if !ok || got != tc.want {
				t.Errorf("child %s = %q (present %v); want %q", tc.variable, got, ok, tc.want)
			}
		})
	}
	t.Run("git credential helper from the parent environment still active", func(t *testing.T) {
		helpers := envCleanHelpers(t, child)
		if len(helpers) == 0 {
			t.Fatalf("child sets no credential.helper; want an environment-set empty helper, got env %q", child)
		}
		for _, h := range helpers {
			if h != "" {
				t.Errorf("child credential.helper values %q; want only the empty value", helpers)
				break
			}
		}
	})
	t.Run("secret value present anywhere in the child", func(t *testing.T) {
		for _, secret := range []string{"ghp_7Hq2xKfLm9Pz3RtWvYb4Nc8Jd1Ae5Gs6Uo0I", "correct-horse-battery-staple",
			"mst-2b4d6f8h0j2l4n6p8r0t", "manager", "store"} {
			for _, kv := range child {
				if strings.Contains(kv, secret) {
					t.Errorf("child entry %q holds the parent secret %q", kv, secret)
				}
			}
		}
	})
	t.Run("parent slice changed by the call", func(t *testing.T) {
		if v, _ := envCleanLookup(parent, "GH_TOKEN"); v != "ghp_7Hq2xKfLm9Pz3RtWvYb4Nc8Jd1Ae5Gs6Uo0I" {
			t.Errorf("parent GH_TOKEN = %q after CleanEnv; want the parent left unchanged", v)
		}
	})
	t.Run("process environment secret kept by the child", func(t *testing.T) {
		t.Setenv("GH_TOKEN", "ghp_processEnv0Value1Read2From3OS4x")
		got := core.CleanEnv(os.Environ(), nil)
		if v, ok := envCleanLookup(got, "GH_TOKEN"); ok {
			t.Errorf("child of os.Environ keeps GH_TOKEN=%q; want it dropped", v)
		}
	})
}

// envExact reports whether env holds a variable named exactly name, with this exact case.
func envExact(env []string, name string) (string, bool) {
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, name+"="); ok {
			return v, true
		}
	}
	return "", false
}

func TestCleanEnvGitReach(t *testing.T) {
	// Each variable lets a child reach a credential or run a program outside the run: an askpass
	// program, the ssh agent, the editor's git bridge, an ssh or proxy command, git's own programs.
	parent := []struct{ name, value string }{
		{"GIT_ASKPASS", "/usr/lib/git-core/git-gui--askpass"},
		{"SSH_ASKPASS", "/usr/bin/ssh-askpass"},
		{"SSH_AUTH_SOCK", "/tmp/ssh-XXXXk3Jd9q/agent.2841"},
		{"VSCODE_GIT_IPC_HANDLE", "/run/user/1000/vscode-git-6e1f2a9c4b.sock"},
		{"GIT_SSH_COMMAND", "ssh -i /home/zeynep/.ssh/id_ed25519_deploy"},
		{"GIT_SSH", "/usr/bin/plink"},
		{"GIT_EXEC_PATH", "/home/zeynep/.local/libexec/git-core"},
		{"GIT_PROXY_COMMAND", "/home/zeynep/bin/corp-proxy"},
		{"git_askpass", `C:\Program Files\Git\mingw64\bin\git-askpass.exe`},
		{"Ssh_Auth_Sock", `\.\pipe\openssh-ssh-agent`},
		{"vscode_git_ipc_handle", `\.\pipe\vscode-git-6e1f2a9c4b-sock`},
		{"Git_Ssh_Command", `ssh -i C:\Users\zeynep\.ssh\id_ed25519_deploy`},
		{"git_exec_path", `C:\Users\zeynep\tools\git-core`},
		{"Git_Proxy_Command", `C:\Users\zeynep\bin\corp-proxy.exe`},
	}
	env := []string{"PATH=/usr/local/bin:/usr/bin:/bin"}
	for _, p := range parent {
		env = append(env, p.name+"="+p.value)
	}
	child := core.CleanEnv(env, nil)
	for _, p := range parent {
		t.Run(p.name+" passed to the child, so git can reach a credential outside the run", func(t *testing.T) {
			if v, ok := envExact(child, p.name); ok {
				t.Errorf("child keeps %s=%q; want it dropped (SPEC §6.5)", p.name, v)
			}
		})
	}
	t.Run("PATH lost while dropping git reach variables", func(t *testing.T) {
		if v, _ := envExact(child, "PATH"); v != "/usr/local/bin:/usr/bin:/bin" {
			t.Errorf("child PATH = %q; want it kept", v)
		}
	})
}

func TestCleanEnvEmptyName(t *testing.T) {
	const secret = "ghp_emptyName7Hq2xKfLm9Pz3RtWvYb4Nc8Jd"
	parent := []string{
		"PATH=/usr/local/bin:/usr/bin:/bin",
		"=GH_TOKEN=" + secret,
	}
	child := core.CleanEnv(parent, nil)
	t.Run("entry with an empty-looking name carries a token to the child", func(t *testing.T) {
		for _, kv := range child {
			if strings.Contains(kv, secret) {
				t.Errorf("child entry %q holds the parent secret; want it dropped", kv)
			}
		}
	})
}

func TestCleanEnvGitAskpass(t *testing.T) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("git is the only prerequisite (SPEC §6.2) and is not on PATH: %v", err)
	}
	const secret = "ghp_askpassLeak7Qm2Xv9Rt4Lp8Zc1Wn6Bh3"
	dir := t.TempDir()
	script := filepath.Join(dir, "askpass.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho "+secret+"\n"), 0o700); err != nil {
		t.Fatalf("write askpass script: %v", err)
	}
	home := filepath.Join(dir, "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatalf("make home: %v", err)
	}
	// The parent is this process's environment with a home that holds no git config and an
	// askpass program that answers every prompt with the secret.
	var parent []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(name) {
		case "HOME", "USERPROFILE", "XDG_CONFIG_HOME", "GIT_CONFIG_GLOBAL", "GIT_ASKPASS", "SSH_ASKPASS":
			continue
		}
		parent = append(parent, kv)
	}
	parent = append(parent, "HOME="+home, "USERPROFILE="+home, "XDG_CONFIG_HOME="+home,
		"GIT_ASKPASS="+script, "SSH_ASKPASS="+script)
	child := core.CleanEnv(parent, nil)

	fill := func(t *testing.T, env []string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, gitPath, "credential", "fill")
		cmd.Env = env
		cmd.Dir = dir
		cmd.Stdin = strings.NewReader("protocol=https\nhost=example.invalid\n\n")
		out, _ := cmd.CombinedOutput()
		if ctx.Err() != nil {
			t.Fatalf("git credential fill did not finish in 30 s: %s", out)
		}
		return string(out)
	}
	t.Run("fixture askpass program not reached by git", func(t *testing.T) {
		if out := fill(t, append(append([]string{}, child...), "GIT_ASKPASS="+script)); !strings.Contains(out, secret) {
			t.Fatalf("git credential fill with GIT_ASKPASS set = %q; want the askpass answer, so the check below is live", out)
		}
	})
	t.Run("git credential fill in the child returns a secret from the parent askpass", func(t *testing.T) {
		if out := fill(t, child); strings.Contains(out, secret) {
			t.Errorf("git credential fill in the child = %q; want no credential (SPEC §6.5)", out)
		}
	})
}

func TestCleanEnvGitGlobalConfig(t *testing.T) {
	t.Run("user git config still read by the child, since GIT_CONFIG_GLOBAL is not the null device", func(t *testing.T) {
		parent := []string{"PATH=/usr/local/bin:/usr/bin:/bin", "GIT_CONFIG_GLOBAL=/home/zeynep/.config/git/work.gitconfig"}
		child := core.CleanEnv(parent, nil)
		var values []string
		for _, kv := range child {
			if k, v, _ := strings.Cut(kv, "="); strings.EqualFold(k, "GIT_CONFIG_GLOBAL") {
				values = append(values, v)
			}
		}
		if len(values) != 1 || !strings.EqualFold(values[0], os.DevNull) {
			t.Errorf("child GIT_CONFIG_GLOBAL values = %q; want exactly one, %q (SPEC §6.5)", values, os.DevNull)
		}
	})
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("git is the only prerequisite (SPEC §6.2) and is not on PATH: %v", err)
	}
	const secret = "ghp_globalAskpass3Vn8Kd2Lq7Rx5Tz9Wm4Pc"
	dir := t.TempDir()
	script := filepath.Join(dir, "askpass.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho "+secret+"\n"), 0o700); err != nil {
		t.Fatalf("write askpass script: %v", err)
	}
	// One user-level git config names the askpass program, the way core.askPass in a user's
	// ~/.gitconfig does; git reads it from HOME, from XDG_CONFIG_HOME or from GIT_CONFIG_GLOBAL.
	gitConfig := "[core]\n\taskPass = \"" + filepath.ToSlash(script) + "\"\n"
	for _, tc := range []struct {
		name string
		// file is the config path below the case home; global says GIT_CONFIG_GLOBAL names it.
		file   string
		global bool
	}{
		{"core.askPass in ~/.gitconfig", ".gitconfig", false},
		{"core.askPass in XDG_CONFIG_HOME/git/config", filepath.Join("xdg", "git", "config"), false},
		{"core.askPass in the file GIT_CONFIG_GLOBAL names", "work.gitconfig", true},
	} {
		t.Run(tc.name+" answers git credential fill in the child", func(t *testing.T) {
			home := t.TempDir()
			config := filepath.Join(home, tc.file)
			if err := os.MkdirAll(filepath.Dir(config), 0o700); err != nil {
				t.Fatalf("make config dir: %v", err)
			}
			if err := os.WriteFile(config, []byte(gitConfig), 0o600); err != nil {
				t.Fatalf("write git config: %v", err)
			}
			var parent []string
			for _, kv := range os.Environ() {
				name, _, _ := strings.Cut(kv, "=")
				switch strings.ToUpper(name) {
				case "HOME", "USERPROFILE", "XDG_CONFIG_HOME", "GIT_CONFIG_GLOBAL", "GIT_ASKPASS", "SSH_ASKPASS":
					continue
				}
				parent = append(parent, kv)
			}
			parent = append(parent, "HOME="+home, "USERPROFILE="+home, "XDG_CONFIG_HOME="+filepath.Join(home, "xdg"))
			if tc.global {
				parent = append(parent, "GIT_CONFIG_GLOBAL="+config)
			}
			child := core.CleanEnv(parent, nil)
			// live is the child with its GIT_CONFIG_GLOBAL pointed at the fixture config, so the
			// fixture is proven to reach git before the child is checked.
			live := []string{"GIT_CONFIG_GLOBAL=" + config}
			for _, kv := range child {
				if name, _, _ := strings.Cut(kv, "="); !strings.EqualFold(name, "GIT_CONFIG_GLOBAL") {
					live = append(live, kv)
				}
			}
			if out := envGitFill(t, gitPath, dir, live); !strings.Contains(out, secret) {
				t.Fatalf("git credential fill with the fixture config = %q; want the askpass answer, so the check below is live", out)
			}
			if out := envGitFill(t, gitPath, dir, child); strings.Contains(out, secret) {
				t.Errorf("git credential fill in the child = %q; want no credential from the user git config (SPEC §6.5)", out)
			}
		})
	}
}

// envGitFill runs git credential fill for a fixed host in dir with env and returns its output.
func envGitFill(t *testing.T, gitPath, dir string, env []string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, gitPath, "credential", "fill")
	cmd.Env = env
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader("protocol=https\nhost=example.invalid\n\n")
	out, _ := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("git credential fill did not finish in 30 s: %s", out)
	}
	return string(out)
}

// TestCleanEnvSecretsDropped pins the credential forms the #4 security review found kept: a name
// ending in _PAT or _PASS, NPM_CONFIG__AUTH, GOOGLE_APPLICATION_CREDENTIALS, and a _URL value that
// carries user:password@ (DR-35).
func TestCleanEnvSecretsDropped(t *testing.T) {
	tests := []struct {
		name string
		kv   string
	}{
		{"fine-grained GitHub token GITHUB_PAT kept",
			"GITHUB_PAT=github_pat_11AEXAMPLE0aBcDeFgHiJkL_EXAMPLEmNoPqRsTuVwXyZ0123456789aBcDeFgHiJkLmN"},
		{"GitHub token GH_PAT kept", "GH_PAT=ghp_EXAMPLE0aBcDeFgHiJkLmNoPqRsTuVwXyZ01"},
		{"lower-case github_pat kept, which Windows reads as GITHUB_PAT",
			"github_pat=ghp_EXAMPLE1lowerCaseNameReachesWindows2"},
		{"Azure DevOps token AZURE_DEVOPS_EXT_PAT kept", "AZURE_DEVOPS_EXT_PAT=EXAMPLEazdo0pat1value2abcdefghijklmnopqrstu"},
		{"database password DB_PASS kept", "DB_PASS=EXAMPLE-db-Pass-2026"},
		{"mail password SMTP_PASS kept", "SMTP_PASS=EXAMPLEsmtpPass9"},
		{"mixed-case Redis_Pass kept", "Redis_Pass=EXAMPLEredisPass7"},
		{"npm registry auth NPM_CONFIG__AUTH kept", "NPM_CONFIG__AUTH=emV5bmVwOkVYQU1QTEVucG1QYXNz"},
		{"lower-case npm_config__auth kept, which npm reads", "npm_config__auth=emV5bmVwOkVYQU1QTEVucG1QYXNz"},
		{"Google credentials file GOOGLE_APPLICATION_CREDENTIALS kept",
			"GOOGLE_APPLICATION_CREDENTIALS=/home/zeynep/.config/gcloud/EXAMPLE-service-account.json"},
		{"DATABASE_URL with user:password@ kept", "DATABASE_URL=postgres://user:secret@db.example.com/app"},
		{"lower-case database_url with user:password@ kept",
			"database_url=postgresql://app_rw:EXAMPLEpgPass@db.example.com:5432/app?sslmode=require"},
		{"REDIS_URL with an empty user and a password kept", "REDIS_URL=redis://:EXAMPLEredisPw@cache.example.com:6379/0"},
		{"AMQP_URL with a percent-encoded password kept",
			"AMQP_URL=amqps://svc:EXAMPLE%40mqPw@mq.example.com/vhost"},
		{"git remote URL with a token as password kept",
			"UPSTREAM_URL=https://zeynep:ghp_EXAMPLE2tokenInUrlAbCdEfGhIjKlMnOp@github.com/sungurerdim/aiwe.git"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			name, value, _ := strings.Cut(tc.kv, "=")
			child := core.CleanEnv([]string{"PATH=/usr/local/bin:/usr/bin:/bin", tc.kv}, nil)
			if got, ok := envCleanLookup(child, name); ok {
				t.Errorf("child keeps %s=%q; want it dropped", name, got)
			}
			for _, kv := range child {
				if strings.Contains(kv, value) {
					t.Errorf("child entry %q holds the parent secret %q", kv, value)
				}
			}
			if got, _ := envCleanLookup(child, "PATH"); got != "/usr/local/bin:/usr/bin:/bin" {
				t.Errorf("child PATH = %q; want it kept beside the dropped %s", got, name)
			}
		})
	}
}

// TestCleanEnvSecretsKept pins the boundaries of the new forms: a name that only starts with, or
// holds, PAT or PASS, and a _URL value without a password in its authority, still reach the child.
func TestCleanEnvSecretsKept(t *testing.T) {
	tests := []struct {
		name string
		kv   string
	}{
		{"PATH dropped as a _PAT name", "PATH=/usr/local/bin:/usr/bin:/bin"},
		{"lower-case path dropped", "path=/usr/bin:/bin"},
		{"PATHEXT dropped", "PATHEXT=.COM;.EXE;.BAT;.CMD"},
		{"GOPATH dropped", "GOPATH=/home/zeynep/go"},
		{"HOME dropped", "HOME=/home/zeynep"},
		{"USERPROFILE dropped", "USERPROFILE=/Users/zeynep"},
		{"QUILT_PATCHES dropped as a _PAT name", "QUILT_PATCHES=debian/patches"},
		{"PATTERN_DIR dropped as a _PAT name", "PATTERN_DIR=/srv/patterns"},
		{"PASSENGER_APP_ENV dropped as a _PASS name", "PASSENGER_APP_ENV=production"},
		{"COMPASS_HOME dropped as a _PASS name", "COMPASS_HOME=/opt/compass"},
		{"NPM_CONFIG_REGISTRY dropped as NPM_CONFIG__AUTH", "NPM_CONFIG_REGISTRY=https://registry.npmjs.org/"},
		{"GIT_AUTHOR_NAME dropped", "GIT_AUTHOR_NAME=Zeynep Çağlar"},
		{"_URL without user info dropped", "HOMEPAGE_URL=https://docs.example.com/aiwe"},
		{"_URL with @ only in its path dropped", "REGISTRY_URL=https://registry.npmjs.org/@types%2fnode"},
		{"_URL with user:server@ only in its path dropped",
			"MATRIX_URL=https://matrix.example.com/_matrix/client/v3/profile/@zeynep:example.com"},
		{"scp-style _URL without a password dropped", "MIRROR_URL=git@github.com:sungurerdim/aiwe.git"},
		{"_URL with a port and no user info dropped", "CACHE_URL=redis://cache.example.com:6379/0"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			name, value, _ := strings.Cut(tc.kv, "=")
			child := core.CleanEnv([]string{tc.kv}, nil)
			if got, ok := envExact(child, name); !ok || got != value {
				t.Errorf("child %s = %q (present %v); want %q", name, got, ok, value)
			}
		})
	}
}
