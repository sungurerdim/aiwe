package core_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sungurerdim/aiwe/internal/core"
)

// configTestEnv returns the env variable name of suffix, derived from the product constant
// (DR-25).
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
					"run.json":   `{"gateTimeout": "45m"}`,
					"level.json": `{"logLevel": "error"}`,
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
				"a.json": `{"gateTimeout": "45m"}`, "b.json": `{"logLevel":`}},
			{name: "JSON null accepted as an empty settings object", state: map[string]string{"settings.json": `null`}},
			{name: "one setting in two state files resolved by file order", state: map[string]string{
				"a.json": `{"logLevel": "warn"}`, "b.json": `{"logLevel": "error"}`}},
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

	t.Run("upper-case .JSON suffix read as a state file", func(t *testing.T) {
		// SPEC §5.12 names <state>/*.json; only the lower-case suffix is a state file.
		dir := configTestWriteState(t, map[string]string{"SETTINGS.JSON": `{"logLevel": "error"}`})
		got, err := core.ResolveConfig(configTestSettings(), nil, configTestGetenv(nil), dir)
		if err != nil {
			t.Fatalf("ResolveConfig: %v", err)
		}
		if v := configTestFind(t, got, "logLevel"); v.Value != "info" || v.Origin != core.ConfigOriginDefault {
			t.Errorf("logLevel = %q from %q, want %q from %q", v.Value, v.Origin, "info", core.ConfigOriginDefault)
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
		if row.Err != core.ErrConfigInvalid || row.Status != core.StatusError || row.Next == "" {
			t.Errorf("config_invalid row = %+v, want Err core.ErrConfigInvalid, status error and a next action", row)
		}
		if core.ErrConfigInvalid.Error() != "config_invalid" {
			t.Errorf("core.ErrConfigInvalid = %q, want %q", core.ErrConfigInvalid, "config_invalid")
		}
	})
}
