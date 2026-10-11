package core_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/sungurerdim/aiwe/internal/core"
)

// configShowSettings is the setting table the cases resolve: one setting per origin and one
// secret-shaped setting.
func configShowSettings() []core.ConfigSetting {
	return []core.ConfigSetting{
		{Name: "model", Env: "AIWE_MODEL", Default: "claude-opus-5-5"},
		{Name: "max_turns", Env: "AIWE_MAX_TURNS", Default: "40"},
		{Name: "tracker", Env: "AIWE_TRACKER", Default: "github"},
		{Name: "log_level", Env: "AIWE_LOG_LEVEL", Default: "info"},
		{Name: "api_token", Env: "AIWE_API_TOKEN", Default: ""},
		{Name: "proxy", Env: "AIWE_PROXY_URL", Default: ""},
	}
}

// configShowEnv returns an env lookup where every OS convention of the disk roots points below
// base, plus extra.
func configShowEnv(base string, extra map[string]string) func(string) string {
	env := map[string]string{
		"LOCALAPPDATA":   filepath.Join(base, "local"),
		"TEMP":           filepath.Join(base, "temp"),
		"TMPDIR":         filepath.Join(base, "temp"),
		"XDG_STATE_HOME": filepath.Join(base, "xdg-state"),
		"XDG_CACHE_HOME": filepath.Join(base, "xdg-cache"),
	}
	for k, v := range extra {
		env[k] = v
	}
	return func(name string) string { return env[name] }
}

// writeConfigState writes one state file of string settings into dir.
func writeConfigState(t *testing.T, dir string, settings map[string]string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestConfigShow(t *testing.T) {
	base := t.TempDir()
	stateDir := filepath.Join(base, "aiwe-state")
	cacheDir := filepath.Join(base, "aiwe-cache")
	writeConfigState(t, stateDir, map[string]string{"model": "state-model", "max_turns": "25",
		"tracker": "gitlab"})
	getenv := configShowEnv(base, map[string]string{
		"AIWE_HOME":      stateDir,
		"AIWE_CACHE":     cacheDir,
		"AIWE_MODEL":     "env-model",
		"AIWE_MAX_TURNS": "30",
		"AIWE_API_TOKEN": "ghp_Zq81LmN0pQrStUvWx",
		"AIWE_PROXY_URL": "https://ada:hunter2-proxy@proxy.example.com:8080",
	})
	res, err := core.ConfigShow(core.ConfigShowInput{Settings: configShowSettings(),
		Flags: map[string]string{"model": "flag-model"}, Getenv: getenv, GOOS: runtime.GOOS, Home: base})
	if err != nil {
		t.Fatalf("ConfigShow: %v", err)
	}
	rows, ok := res.Data.([]core.ConfigRow)
	if !ok {
		t.Fatalf("Data is %T, want []core.ConfigRow", res.Data)
	}
	want := []core.ConfigRow{
		{Name: "root.state", Value: stateDir, Origin: core.ConfigOriginEnv},
		{Name: "root.cache", Value: cacheDir, Origin: core.ConfigOriginEnv},
		{Name: "root.temp", Origin: core.ConfigOriginDefault},
		{Name: "model", Value: "flag-model", Origin: core.ConfigOriginFlag},
		{Name: "max_turns", Value: "30", Origin: core.ConfigOriginEnv},
		{Name: "tracker", Value: "gitlab", Origin: core.ConfigOriginState},
		{Name: "log_level", Value: "info", Origin: core.ConfigOriginDefault},
		{Name: "api_token", Value: core.ConfigMasked, Origin: core.ConfigOriginEnv},
		{Name: "proxy", Value: core.ConfigMasked, Origin: core.ConfigOriginEnv},
	}
	wantTemp, err := core.ResolveRoots(runtime.GOOS, getenv, base)
	if err != nil {
		t.Fatal(err)
	}
	want[2].Value = wantTemp.Temp
	t.Run("a setting left out or shown twice is caught", func(t *testing.T) {
		if len(rows) != len(want) {
			t.Fatalf("rows = %d %+v, want %d", len(rows), rows, len(want))
		}
	})
	for i, w := range want {
		t.Run(fmt.Sprintf("%s shown with its %s origin", w.Name, w.Origin), func(t *testing.T) {
			if i >= len(rows) || rows[i] != w {
				t.Fatalf("row %d = %+v, want %+v", i, rowAt(rows, i), w)
			}
			line := fmt.Sprintf("%s = %s (%s)", w.Name, w.Value, w.Origin)
			if !containsLine(res.Detail, line) {
				t.Fatalf("detail misses line %q:\n%s", line, res.Detail)
			}
		})
	}
	t.Run("a secret value never prints in clear", func(t *testing.T) {
		var out bytes.Buffer
		if err := core.WriteResult(&out, core.FormatJSON, res); err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{"ghp_Zq81LmN0pQrStUvWx", "hunter2-proxy"} {
			if strings.Contains(out.String(), secret) || strings.Contains(res.Detail, secret) {
				t.Fatalf("secret %q printed in clear:\n%s", secret, out.String())
			}
		}
	})
}

func TestConfigShowRefuses(t *testing.T) {
	base := t.TempDir()
	stateDir := filepath.Join(base, "aiwe-state")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "broken.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		getenv func(string) string
	}{
		{"a broken state file is not shown as defaults",
			configShowEnv(base, map[string]string{"AIWE_HOME": stateDir})},
		{"a relative root override is not shown as a guessed root",
			configShowEnv(base, map[string]string{"AIWE_HOME": "relative-state"})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := core.ConfigShow(core.ConfigShowInput{Settings: configShowSettings(), Getenv: tc.getenv,
				GOOS: runtime.GOOS, Home: base})
			if err == nil {
				t.Fatal("ConfigShow succeeded, want an error")
			}
		})
	}
}

func TestConfigShowCommand(t *testing.T) {
	var out bytes.Buffer
	code := core.RunCLI(context.Background(), core.ConfigCommands(), []string{"config", "show", "--format", "json"}, &out)
	if code != 0 {
		t.Fatalf("exit %d, want 0:\n%s", code, out.String())
	}
	var env struct {
		Data []core.ConfigRow `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("decode %q: %v", out.String(), err)
	}
	for _, name := range []string{"root.state", "root.cache", "root.temp"} {
		if !hasConfigRow(env.Data, name) {
			t.Fatalf("config show left out %s: %+v", name, env.Data)
		}
	}
}

func rowAt(rows []core.ConfigRow, i int) any {
	if i < len(rows) {
		return rows[i]
	}
	return "missing"
}

func containsLine(text, line string) bool {
	for _, l := range strings.Split(text, "\n") {
		if l == line {
			return true
		}
	}
	return false
}

func hasConfigRow(rows []core.ConfigRow, name string) bool {
	for _, r := range rows {
		if r.Name == name && r.Origin != "" && r.Value != "" {
			return true
		}
	}
	return false
}
