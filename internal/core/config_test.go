package core_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sungurerdim/aiwe/internal/core"
)

// configEnvName returns the environment variable of a setting, derived from the product constant.
func configEnvName(suffix string) string {
	return strings.ToUpper(core.Product) + "_" + suffix
}

// configWriteState writes one state file under dir and returns dir.
func configWriteState(t *testing.T, dir, file, body string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o600); err != nil {
		t.Fatalf("write state file %s: %v", file, err)
	}
	return dir
}

func TestConfig(t *testing.T) {
	keys := []core.ConfigKey{
		{Name: "format", Env: configEnvName("FORMAT"), File: "settings.json", Default: "text"},
		{Name: "ledger-max-mb", Env: configEnvName("LEDGER_MAX_MB"), File: "settings.json", Default: "64"},
	}
	allState := `{"format": "json", "ledger-max-mb": "48"}`
	tests := []struct {
		name  string
		flags map[string]string
		env   map[string]string
		// state is the body of settings.json; empty means the file does not exist.
		state string
		want  []core.Setting
	}{
		{
			name:  "flag loses to env, state or default",
			flags: map[string]string{"format": "json", "ledger-max-mb": "32"},
			env:   map[string]string{configEnvName("FORMAT"): "text", configEnvName("LEDGER_MAX_MB"): "16"},
			state: `{"format": "text", "ledger-max-mb": "48"}`,
			want: []core.Setting{
				{Name: "format", Value: "json", Origin: core.OriginFlag},
				{Name: "ledger-max-mb", Value: "32", Origin: core.OriginFlag},
			},
		},
		{
			name:  "env loses to a state file or the default",
			env:   map[string]string{configEnvName("FORMAT"): "text", configEnvName("LEDGER_MAX_MB"): "16"},
			state: allState,
			want: []core.Setting{
				{Name: "format", Value: "text", Origin: core.OriginEnv},
				{Name: "ledger-max-mb", Value: "16", Origin: core.OriginEnv},
			},
		},
		{
			name:  "state file loses to the default",
			state: allState,
			want: []core.Setting{
				{Name: "format", Value: "json", Origin: core.OriginState},
				{Name: "ledger-max-mb", Value: "48", Origin: core.OriginState},
			},
		},
		{
			name: "missing state file not falling back to the default",
			want: []core.Setting{
				{Name: "format", Value: "text", Origin: core.OriginDefault},
				{Name: "ledger-max-mb", Value: "64", Origin: core.OriginDefault},
			},
		},
		{
			name:  "each setting not resolved on its own layer",
			flags: map[string]string{"format": "json"},
			env:   map[string]string{configEnvName("LEDGER_MAX_MB"): "16"},
			state: `{"format": "text"}`,
			want: []core.Setting{
				{Name: "format", Value: "json", Origin: core.OriginFlag},
				{Name: "ledger-max-mb", Value: "16", Origin: core.OriginEnv},
			},
		},
		{
			name:  "state value printed with the default origin when it equals the default",
			state: `{"format": "text", "ledger-max-mb": "64"}`,
			want: []core.Setting{
				{Name: "format", Value: "text", Origin: core.OriginState},
				{Name: "ledger-max-mb", Value: "64", Origin: core.OriginState},
			},
		},
		{
			name:  "flag value printed with the default origin when it equals the default",
			flags: map[string]string{"format": "text"},
			want: []core.Setting{
				{Name: "format", Value: "text", Origin: core.OriginFlag},
				{Name: "ledger-max-mb", Value: "64", Origin: core.OriginDefault},
			},
		},
		{
			name:  "Unicode state value changed on the way out",
			state: `{"format": "json", "ledger-max-mb": "48", "note": "çalışma günü"}`,
			want: []core.Setting{
				{Name: "format", Value: "json", Origin: core.OriginState},
				{Name: "ledger-max-mb", Value: "48", Origin: core.OriginState},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.state != "" {
				configWriteState(t, dir, "settings.json", tc.state)
			}
			lookup := func(k string) (string, bool) { v, ok := tc.env[k]; return v, ok }
			got, err := core.ResolveConfig(keys, core.ConfigLayers{Flags: tc.flags, LookupEnv: lookup, StateDir: dir})
			if err != nil {
				t.Fatalf("ResolveConfig: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("ResolveConfig = %+v; want %+v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("setting %d = %+v; want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
	t.Run("unparsable state file read as absent instead of refused", func(t *testing.T) {
		dir := configWriteState(t, t.TempDir(), "settings.json", `{"format": "json",`)
		got, err := core.ResolveConfig(keys, core.ConfigLayers{LookupEnv: func(string) (string, bool) { return "", false }, StateDir: dir})
		if err == nil {
			t.Errorf("ResolveConfig with a truncated settings.json = %+v, nil; want an error", got)
		}
	})
	for _, body := range []string{`null`, `[]`, `"json"`, `1`} {
		t.Run("state file holding "+body+" instead of an object not refused as config_invalid", func(t *testing.T) {
			dir := configWriteState(t, t.TempDir(), "settings.json", body)
			got, err := core.ResolveConfig(keys, core.ConfigLayers{LookupEnv: func(string) (string, bool) { return "", false }, StateDir: dir})
			if !errors.Is(err, core.ErrConfigInvalid) {
				t.Errorf("ResolveConfig with settings.json = %s: %+v, %v; want %v", body, got, err, core.ErrConfigInvalid)
			}
		})
	}
	t.Run("process environment not read through the env layer", func(t *testing.T) {
		t.Setenv(configEnvName("FORMAT"), "json")
		got, err := core.ResolveConfig(keys[:1], core.ConfigLayers{LookupEnv: os.LookupEnv, StateDir: t.TempDir()})
		if err != nil {
			t.Fatalf("ResolveConfig: %v", err)
		}
		want := core.Setting{Name: "format", Value: "json", Origin: core.OriginEnv}
		if len(got) != 1 || got[0] != want {
			t.Errorf("ResolveConfig = %+v; want [%+v]", got, want)
		}
	})
}
