package core

import (
	"context"
	"fmt"
	"runtime"
	"strings"
)

// ConfigMasked replaces the value of a secret-shaped setting in every config show output (DR-35).
const ConfigMasked = "[masked]"

// ConfigRow is one line of config show: a setting, its value as printed and its origin.
type ConfigRow struct {
	Name   string       `json:"name"`
	Value  string       `json:"value"`
	Origin ConfigOrigin `json:"origin"`
}

// ConfigShowInput is what config show reads: the setting table, the flags given, the env lookup,
// and the OS and home directory the disk roots resolve for.
type ConfigShowInput struct {
	Settings []ConfigSetting
	Flags    map[string]string
	Getenv   func(string) string
	GOOS     string
	Home     string
}

// ConfigCommands returns the config commands of the registry.
func ConfigCommands() []Command {
	return []Command{
		{Module: "core", Object: "config", Verb: "show", Params: []string{"format"},
			Purpose: "print each setting and the disk roots with their origin", Run: runConfigShow},
	}
}

// runConfigShow shows the settings of this process. No setting table exists yet, so it shows the
// disk roots; the given flags are command parameters and set no setting.
func runConfigShow(_ context.Context, in Input) (Result[any], error) {
	return ConfigShow(ConfigShowInput{Flags: in.Params, Getenv: LookupEnv, GOOS: runtime.GOOS, Home: processHome()})
}

// ConfigShow resolves the disk roots and then every setting of in.Settings, in order, each with its
// origin (SPEC §5.12, §6.4). The state root holds the state files. A secret-shaped setting prints
// as ConfigMasked. A root that does not resolve, or a broken state file, is an error.
func ConfigShow(in ConfigShowInput) (Result[any], error) {
	roots, err := ResolveRoots(in.GOOS, in.Getenv, in.Home)
	if err != nil {
		return Result[any]{}, err
	}
	values, err := ResolveConfig(in.Settings, in.Flags, in.Getenv, roots.State)
	if err != nil {
		return Result[any]{}, err
	}
	rows := rootRows(roots, in.Getenv)
	for i, v := range values {
		rows = append(rows, ConfigRow{Name: v.Name, Value: shownValue(in.Settings[i], v.Value), Origin: v.Origin})
	}
	lines := make([]string, 0, len(rows))
	for _, r := range rows {
		lines = append(lines, fmt.Sprintf("%s = %s (%s)", r.Name, r.Value, r.Origin))
	}
	return Result[any]{Status: StatusOK, Outcome: OutcomeOK, Data: rows, Detail: strings.Join(lines, "\n")}, nil
}

// rootRows returns the three disk roots; a root comes from env when its override is set, else
// from the OS convention, its default (SPEC §6.4).
func rootRows(roots Roots, getenv func(string) string) []ConfigRow {
	rows := []ConfigRow{
		{Name: "root.state", Value: roots.State},
		{Name: "root.cache", Value: roots.Cache},
		{Name: "root.temp", Value: roots.Temp},
	}
	for i, suffix := range []string{"HOME", "CACHE", "TMP"} {
		rows[i].Origin = ConfigOriginDefault
		if getenv(productEnv(suffix)) != "" {
			rows[i].Origin = ConfigOriginEnv
		}
	}
	return rows
}

// shownValue returns value, or ConfigMasked when the setting's name or env variable is
// secret-shaped or the value is a URL that may carry a password (DR-35); an empty value stays empty.
func shownValue(s ConfigSetting, value string) string {
	name, env := strings.ToUpper(s.Name), strings.ToUpper(s.Env)
	if value != "" && (secretName(name) || secretName(env) || urlWithPassword(name, value) || urlWithPassword(env, value)) {
		return ConfigMasked
	}
	return value
}
