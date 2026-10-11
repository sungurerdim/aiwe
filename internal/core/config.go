package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// ErrConfigInvalid is returned when a state file cannot be read as settings (SPEC §5.12).
var ErrConfigInvalid = errors.New("config_invalid")

// ConfigOrigin names the source a resolved value came from.
type ConfigOrigin string

// The four origins, in precedence order (SPEC §5.12).
const (
	ConfigOriginFlag    ConfigOrigin = "flag"
	ConfigOriginEnv     ConfigOrigin = "env"
	ConfigOriginState   ConfigOrigin = "state"
	ConfigOriginDefault ConfigOrigin = "default"
)

// ConfigSetting declares one setting: its name, its env variable and its built-in default.
type ConfigSetting struct {
	Name    string
	Env     string
	Default string
}

// ConfigValue is one resolved setting and the origin of its value.
type ConfigValue struct {
	Name   string
	Value  string
	Origin ConfigOrigin
}

// ResolveConfig resolves each setting, in settings order, by flags > env > state files > default
// (SPEC §5.12). A flag counts when its name is a key of flags; an empty env value counts as unset.
// Only <stateDir>/*.json files are state files; each holds one JSON object of string values. A
// broken state file, or one setting in two state files, returns ErrConfigInvalid.
func ResolveConfig(settings []ConfigSetting, flags map[string]string, getenv func(string) string,
	stateDir string) ([]ConfigValue, error) {
	state, err := readConfigState(stateDir)
	if err != nil {
		return nil, err
	}
	values := make([]ConfigValue, 0, len(settings))
	for _, s := range settings {
		values = append(values, resolveSetting(s, flags, getenv, state))
	}
	return values, nil
}

// resolveSetting returns the value of s from the first source that sets it.
func resolveSetting(s ConfigSetting, flags map[string]string, getenv func(string) string,
	state map[string]string) ConfigValue {
	if v, ok := flags[s.Name]; ok {
		return ConfigValue{Name: s.Name, Value: v, Origin: ConfigOriginFlag}
	}
	if s.Env != "" {
		if v := getenv(s.Env); v != "" {
			return ConfigValue{Name: s.Name, Value: v, Origin: ConfigOriginEnv}
		}
	}
	if v, ok := state[s.Name]; ok {
		return ConfigValue{Name: s.Name, Value: v, Origin: ConfigOriginState}
	}
	return ConfigValue{Name: s.Name, Value: s.Default, Origin: ConfigOriginDefault}
}

// readConfigState merges every <dir>/*.json file into one name → value map. A missing directory
// holds no settings.
func readConfigState(dir string) (map[string]string, error) {
	state := map[string]string{}
	if dir == "" {
		return state, nil
	}
	paths, err := filepath.Glob(filepath.Join(filepath.Clean(dir), "*.json"))
	if err != nil {
		return nil, configInvalid("a state directory pattern", err.Error())
	}
	sort.Strings(paths)
	for _, p := range paths {
		if err := mergeConfigFile(state, p); err != nil {
			return nil, err
		}
	}
	return state, nil
}

// mergeConfigFile adds the settings of the state file at path to state.
func mergeConfigFile(state map[string]string, path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return configInvalid("a readable state file", fmt.Sprintf("%s: %v", path, err))
	}
	var file map[string]string
	if err := json.Unmarshal(b, &file); err != nil {
		return configInvalid("one JSON object of string values", fmt.Sprintf("%s: %v", path, err))
	}
	if file == nil {
		return configInvalid("one JSON object of string values", path+": null")
	}
	for name, v := range file {
		if _, ok := state[name]; ok {
			return configInvalid("each setting in one state file", fmt.Sprintf("%s sets %q again", path, name))
		}
		state[name] = v
	}
	return nil
}

// configInvalid returns the config_invalid error with what was expected and what was found.
func configInvalid(expected, found string) error {
	return &Error{Outcome: "config_invalid", Expected: expected, Found: found, Err: ErrConfigInvalid}
}
