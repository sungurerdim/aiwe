package core_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sungurerdim/aiwe/internal/core"
)

// configTestEnv returns the env variable name of suffix, derived from the product constant
// (DR-17).
func configTestEnv(suffix string) string {
	return strings.ToUpper(core.Product) + "_" + suffix
}

// configTestSettings are two realistic settings: a log level and a gate timeout.
func configTestSettings() []core.ConfigSetting {
	return []core.ConfigSetting{
		{Name: "logLevel", Env: configTestEnv("LOG_LEVEL"), Default: "info"},
		{Name: "gateTimeout", Env: configTestEnv("GATE_TIMEOUT"), Default: "30m"},
	}
}

// configTestGetenv returns a getenv that reads vars only; the process environment is never read.
func configTestGetenv(vars map[string]string) func(string) string {
	return func(name string) string { return vars[name] }
}

// configTestWriteState writes each file of files into a new state directory and returns it.
func configTestWriteState(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("write state file %s: %v", name, err)
		}
	}
	return dir
}

// configTestWriteSized writes a valid settings object of exactly size bytes to path.
func configTestWriteSized(t *testing.T, path string, size int) {
	t.Helper()
	head, tail := `{"logLevel": "`, `"}`
	body := head + strings.Repeat("e", size-len(head)-len(tail)) + tail
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write state file %s: %v", path, err)
	}
}

// configTestResolveWithin resolves the state directory dir and returns the error, failing when
// the read still blocks after 1 s; a writer opened on path then releases a blocked FIFO open.
func configTestResolveWithin(t *testing.T, dir, path string) error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		_, err := core.ResolveConfig(configTestSettings(), nil, configTestGetenv(nil), dir)
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(time.Second):
		if w, err := os.OpenFile(path, os.O_WRONLY, 0); err == nil {
			_ = w.Close()
		}
		<-done
		t.Fatalf("ResolveConfig of %s still blocked after 1 s; want config_invalid", path)
		return nil
	}
}

// configTestFind returns the resolved value of name, failing when it is missing or listed twice.
func configTestFind(t *testing.T, values []core.ConfigValue, name string) core.ConfigValue {
	t.Helper()
	var found []core.ConfigValue
	for _, v := range values {
		if v.Name == name {
			found = append(found, v)
		}
	}
	if len(found) != 1 {
		t.Fatalf("setting %q resolved %d times in %+v, want exactly once", name, len(found), values)
	}
	return found[0]
}

func TestConfig(t *testing.T) {
	level := configTestEnv("LOG_LEVEL")

	t.Run("precedence other than flags, env, state files, defaults", func(t *testing.T) {
		tests := []struct {
			name       string
			flags      map[string]string
			env        map[string]string
			state      map[string]string
			wantValue  string
			wantOrigin core.ConfigOrigin
		}{
			{
				name:      "flag loses to env, state or default",
				flags:     map[string]string{"logLevel": "debug"},
				env:       map[string]string{level: "warn"},
				state:     map[string]string{"settings.json": `{"logLevel": "error"}`},
				wantValue: "debug", wantOrigin: core.ConfigOriginFlag,
			},
			{
				name:      "env loses to a state file or the default",
				env:       map[string]string{level: "warn"},
				state:     map[string]string{"settings.json": `{"logLevel": "error"}`},
				wantValue: "warn", wantOrigin: core.ConfigOriginEnv,
			},
			{
				name:      "state file loses to the default",
				state:     map[string]string{"settings.json": `{"logLevel": "error"}`},
				wantValue: "error", wantOrigin: core.ConfigOriginState,
			},
			{
				name:      "default missing when no source sets the value",
				wantValue: "info", wantOrigin: core.ConfigOriginDefault,
			},
			{
				name:      "flag loses to the default when env and state are silent",
				flags:     map[string]string{"logLevel": "debug"},
				wantValue: "debug", wantOrigin: core.ConfigOriginFlag,
			},
			{
				name:      "env loses to the default when state is silent",
				env:       map[string]string{level: "warn"},
				wantValue: "warn", wantOrigin: core.ConfigOriginEnv,
			},
			{
				name:      "empty env value overrides the state file",
				env:       map[string]string{level: ""},
				state:     map[string]string{"settings.json": `{"logLevel": "error"}`},
				wantValue: "error", wantOrigin: core.ConfigOriginState,
			},
			{
				name:      "Unicode value from a state file is rewritten",
				state:     map[string]string{"settings.json": `{"logLevel": "Zürich-ğüş-日本"}`},
				wantValue: "Zürich-ğüş-日本", wantOrigin: core.ConfigOriginState,
			},
			{
				name: "a non-JSON file in the state directory is read as a state file",
				state: map[string]string{
					"notes.txt":    "logLevel = trace, not json at all {",
					"settings.bak": `{"logLevel": "trace"}`,
				},
				wantValue: "info", wantOrigin: core.ConfigOriginDefault,
			},
			{
				name: "a setting in a second state file is missed",
				state: map[string]string{
					"settings.run.json":   `{"gateTimeout": "45m"}`,
					"settings.level.json": `{"logLevel": "error"}`,
				},
				wantValue: "error", wantOrigin: core.ConfigOriginState,
			},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				dir := configTestWriteState(t, tc.state)
				got, err := core.ResolveConfig(configTestSettings(), tc.flags, configTestGetenv(tc.env), dir)
				if err != nil {
					t.Fatalf("ResolveConfig: %v", err)
				}
				v := configTestFind(t, got, "logLevel")
				if v.Value != tc.wantValue || v.Origin != tc.wantOrigin {
					t.Errorf("logLevel = %q from %q, want %q from %q", v.Value, v.Origin, tc.wantValue, tc.wantOrigin)
				}
			})
		}
	})

	t.Run("value printed with the wrong origin", func(t *testing.T) {
		dir := configTestWriteState(t, map[string]string{"settings.json": `{"gateTimeout": "45m"}`})
		got, err := core.ResolveConfig(configTestSettings(), map[string]string{"logLevel": "debug"},
			configTestGetenv(nil), dir)
		if err != nil {
			t.Fatalf("ResolveConfig: %v", err)
		}
		if len(got) != 2 || got[0].Name != "logLevel" || got[1].Name != "gateTimeout" {
			t.Fatalf("ResolveConfig = %+v, want logLevel then gateTimeout, in settings order", got)
		}
		tests := []struct {
			name       string
			setting    string
			wantValue  string
			wantOrigin core.ConfigOrigin
		}{
			{name: "flag value printed with another origin", setting: "logLevel",
				wantValue: "debug", wantOrigin: core.ConfigOriginFlag},
			{name: "state value printed with the origin of another setting", setting: "gateTimeout",
				wantValue: "45m", wantOrigin: core.ConfigOriginState},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				v := configTestFind(t, got, tc.setting)
				if v.Value != tc.wantValue || v.Origin != tc.wantOrigin {
					t.Errorf("%s = %q from %q, want %q from %q", tc.setting, v.Value, v.Origin, tc.wantValue, tc.wantOrigin)
				}
			})
		}
	})

	t.Run("origin names other than flag, env, state, default", func(t *testing.T) {
		tests := []struct {
			name   string
			origin core.ConfigOrigin
			want   string
		}{
			{name: "flag origin renamed", origin: core.ConfigOriginFlag, want: "flag"},
			{name: "env origin renamed", origin: core.ConfigOriginEnv, want: "env"},
			{name: "state origin renamed", origin: core.ConfigOriginState, want: "state"},
			{name: "default origin renamed", origin: core.ConfigOriginDefault, want: "default"},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				if string(tc.origin) != tc.want {
					t.Errorf("origin = %q, want %q", tc.origin, tc.want)
				}
			})
		}
	})

	t.Run("broken state file guessed past instead of config_invalid", func(t *testing.T) {
		tests := []struct {
			name  string
			state map[string]string
		}{
			{name: "truncated JSON accepted", state: map[string]string{"settings.json": `{"logLevel": "err`}},
			{name: "JSON array accepted as settings", state: map[string]string{"settings.json": `["logLevel", "error"]`}},
			{name: "number value accepted as a string", state: map[string]string{"settings.json": `{"logLevel": 3}`}},
			{name: "one broken file hidden by a valid one", state: map[string]string{
				"settings.a.json": `{"gateTimeout": "45m"}`, "settings.b.json": `{"logLevel":`}},
			{name: "JSON null accepted as an empty settings object", state: map[string]string{"settings.json": `null`}},
			{name: "one setting in two state files resolved by file order", state: map[string]string{
				"settings.a.json": `{"logLevel": "warn"}`, "settings.b.json": `{"logLevel": "error"}`}},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				dir := configTestWriteState(t, tc.state)
				got, err := core.ResolveConfig(configTestSettings(), nil, configTestGetenv(nil), dir)
				if !errors.Is(err, core.ErrConfigInvalid) {
					t.Fatalf("ResolveConfig = %+v, %v; want an error that is core.ErrConfigInvalid", got, err)
				}
			})
		}
	})

	t.Run("state directory with glob characters read as a pattern", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "work[a]", "state")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("make state directory: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"logLevel": "error"}`), 0o600); err != nil {
			t.Fatalf("write state file: %v", err)
		}
		got, err := core.ResolveConfig(configTestSettings(), nil, configTestGetenv(nil), dir)
		if err != nil {
			t.Fatalf("ResolveConfig: %v", err)
		}
		if v := configTestFind(t, got, "logLevel"); v.Value != "error" || v.Origin != core.ConfigOriginState {
			t.Errorf("logLevel = %q from %q, want %q from %q", v.Value, v.Origin, "error", core.ConfigOriginState)
		}
	})

	t.Run("state file that is not settings read as settings", func(t *testing.T) {
		// SPEC §6.4: the state root also holds sources.json and sources.state.json (§8.1); they are
		// not settings, so they never make config resolution fail.
		dir := configTestWriteState(t, map[string]string{
			"settings.json": `{"logLevel": "error"}`,
			"sources.json": `{"formatVersion": 1, "window": 100, "sources": [
				{"id": "sub-a", "adapter": "claude-code", "kind": "subscription", "share": 50}]}`,
			"sources.state.json": `{"sub-a": {"state": "active", "credit": 12}}`,
		})
		got, err := core.ResolveConfig(configTestSettings(), nil, configTestGetenv(nil), dir)
		if err != nil {
			t.Fatalf("ResolveConfig: %v; want the non-settings state files ignored", err)
		}
		if v := configTestFind(t, got, "logLevel"); v.Value != "error" || v.Origin != core.ConfigOriginState {
			t.Errorf("logLevel = %q from %q, want %q from %q", v.Value, v.Origin, "error", core.ConfigOriginState)
		}
	})

	t.Run("upper-case .JSON suffix read as a state file", func(t *testing.T) {
		// SPEC §5.12 names settings.json and settings.*.json; only the lower-case name is a settings file.
		dir := configTestWriteState(t, map[string]string{"SETTINGS.JSON": `{"logLevel": "error"}`})
		got, err := core.ResolveConfig(configTestSettings(), nil, configTestGetenv(nil), dir)
		if err != nil {
			t.Fatalf("ResolveConfig: %v", err)
		}
		if v := configTestFind(t, got, "logLevel"); v.Value != "info" || v.Origin != core.ConfigOriginDefault {
			t.Errorf("logLevel = %q from %q, want %q from %q", v.Value, v.Origin, "info", core.ConfigOriginDefault)
		}
	})

	t.Run("settings file linked outside the state directory read as settings", func(t *testing.T) {
		// SPEC §5.12: a settings read stays inside its state directory; a link out of it is a
		// broken state file, never a setting.
		outside := filepath.Join(t.TempDir(), "settings.json")
		if err := os.WriteFile(outside, []byte(`{"logLevel": "error"}`), 0o600); err != nil {
			t.Fatalf("write outside file: %v", err)
		}
		dir := configTestWriteState(t, nil)
		if err := os.Symlink(outside, filepath.Join(dir, "settings.json")); err != nil {
			t.Skipf("file symlink %s: %v; a junction links only a directory", outside, err)
		}
		got, err := core.ResolveConfig(configTestSettings(), nil, configTestGetenv(nil), dir)
		if !errors.Is(err, core.ErrConfigInvalid) {
			t.Fatalf("ResolveConfig = %+v, %v; want an error that is core.ErrConfigInvalid", got, err)
		}
	})

	t.Run("settings file that is not a regular file of bounded size read as settings", func(t *testing.T) {
		// SPEC §5.12: a settings file is a regular file of at most 64 KiB, checked on the open
		// handle; anything else is config_invalid, returned without hanging (#199).
		const settingsCap = 64 << 10
		tests := []struct {
			name      string
			unix      bool
			make      func(t *testing.T, path string)
			wantValid bool
		}{
			{name: "file at the cap refused", wantValid: true,
				make: func(t *testing.T, path string) { configTestWriteSized(t, path, settingsCap) }},
			{name: "file one byte over the cap accepted",
				make: func(t *testing.T, path string) { configTestWriteSized(t, path, settingsCap+1) }},
			{name: "directory under the settings name skipped",
				make: func(t *testing.T, path string) {
					if err := os.Mkdir(path, 0o700); err != nil {
						t.Fatal(err)
					}
				}},
			{name: "FIFO under the settings name blocks the read", unix: true,
				make: func(t *testing.T, path string) {
					if out, err := exec.CommandContext(t.Context(), "mkfifo", "-m", "600", path).CombinedOutput(); err != nil {
						t.Fatalf("mkfifo %s: %v: %s", path, err, out)
					}
				}},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				if tc.unix && runtime.GOOS == "windows" {
					t.Skip("a FIFO is a Unix file type")
				}
				dir := configTestWriteState(t, nil)
				path := filepath.Join(dir, "settings.json")
				tc.make(t, path)
				err := configTestResolveWithin(t, dir, path)
				if tc.wantValid && err != nil {
					t.Fatalf("ResolveConfig: %v; want a settings file at the cap read", err)
				}
				if !tc.wantValid && !errors.Is(err, core.ErrConfigInvalid) {
					t.Fatalf("ResolveConfig = %v; want an error that is core.ErrConfigInvalid", err)
				}
				if !tc.wantValid && !strings.Contains(err.Error(), "settings.json") {
					t.Errorf("ResolveConfig = %v; want the error to name settings.json", err)
				}
			})
		}
	})

	t.Run("oversized settings file read whole before the cap is checked", func(t *testing.T) {
		dir := configTestWriteState(t, nil)
		configTestWriteSized(t, filepath.Join(dir, "settings.json"), 16<<20)
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		_, err := core.ResolveConfig(configTestSettings(), nil, configTestGetenv(nil), dir)
		runtime.ReadMemStats(&after)
		if alloc := after.TotalAlloc - before.TotalAlloc; !errors.Is(err, core.ErrConfigInvalid) || alloc > 1<<20 {
			t.Errorf("ResolveConfig of a 16 MiB settings file allocated %d bytes, error %v; want config_invalid within 1 MiB", alloc, err)
		}
	})

	t.Run("relative state directory resolved against the working directory", func(t *testing.T) {
		// SPEC §5.12: a relative path without --repo is a usage error; the working directory is
		// never used.
		got, err := core.ResolveConfig(configTestSettings(), nil, configTestGetenv(nil), filepath.Join("work", "state"))
		var cerr *core.Error
		if !errors.As(err, &cerr) || cerr.Outcome != core.OutcomeUsage {
			t.Fatalf("ResolveConfig = %+v, %v; want a *core.Error with outcome %q", got, err, core.OutcomeUsage)
		}
	})

	t.Run("config_invalid missing from the outcome table", func(t *testing.T) {
		row, ok := outcomeTable(t)["config_invalid"]
		if !ok {
			t.Fatal("outcome config_invalid is not a row of core.Outcomes() (BC-1)")
		}
		if !errorsTestBare(row.Err, core.ErrConfigInvalid) || row.Status != core.StatusError || row.Next == "" {
			t.Errorf("config_invalid row = %+v, want Err core.ErrConfigInvalid, status error and a next action", row)
		}
		if core.ErrConfigInvalid.Error() != "config_invalid" {
			t.Errorf("core.ErrConfigInvalid = %q, want %q", core.ErrConfigInvalid, "config_invalid")
		}
	})
}
