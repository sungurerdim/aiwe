package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ErrConfigInvalid is returned when a state file cannot be read as settings (SPEC §5.12).
var ErrConfigInvalid = errors.New("config_invalid")

// maxSettingsBytes caps one settings file (SPEC §5.12); a larger file is config_invalid.
const maxSettingsBytes = 64 << 10

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
// Only the settings files of stateDir are read: settings.json and settings.*.json; each holds one
// JSON object of string values. Other state files are not settings. A broken settings file, or one
// setting in two settings files, returns ErrConfigInvalid.
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

// readConfigState merges every settings file of dir into one name → value map, in file name
// order. An empty dir or a missing directory holds no settings; a
// relative dir is a usage error, since the working directory is never used (SPEC §5.12).
func readConfigState(dir string) (map[string]string, error) {
	state := map[string]string{}
	if dir == "" {
		return state, nil
	}
	if !filepath.IsAbs(dir) {
		return nil, &Error{Outcome: OutcomeUsage, Expected: "an absolute state directory", Found: dir}
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return nil, configInvalid("a readable state directory", err.Error())
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, configInvalid("a readable state directory", err.Error())
	}
	err = mergeConfigFiles(state, root, dir, entries)
	if cerr := root.Close(); err == nil && cerr != nil {
		err = configInvalid("a readable state directory", cerr.Error())
	}
	if err != nil {
		return nil, err
	}
	return state, nil
}

// mergeConfigFiles adds the settings of every settings file among entries of dir to state, in
// entry order.
func mergeConfigFiles(state map[string]string, root *os.Root, dir string, entries []fs.DirEntry) error {
	for _, e := range entries {
		if !isSettingsFile(e.Name()) {
			continue
		}
		if err := mergeConfigFile(state, root, dir, e.Name()); err != nil {
			return err
		}
	}
	return nil
}

// isSettingsFile reports whether name is settings.json or settings.<part>.json with a non-empty
// part, lower-case as written (SPEC §5.12).
func isSettingsFile(name string) bool {
	if name == "settings.json" {
		return true
	}
	part, ok := strings.CutPrefix(name, "settings.")
	return ok && len(part) > len(".json") && strings.HasSuffix(part, ".json")
}

// mergeConfigFile adds the settings of the state file name of the state directory dir to state.
// The file is read through root, so a link out of dir is refused, never followed.
func mergeConfigFile(state map[string]string, root *os.Root, dir, name string) error {
	path := filepath.Join(dir, name)
	b, err := readSettingsFile(root, name)
	if err != nil {
		return configInvalid("a regular state file of at most 64 KiB", fmt.Sprintf("%s: %v", path, err))
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

// readSettingsFile opens name of root once, without blocking, checks on that handle that it is a
// regular file, and reads at most maxSettingsBytes+1 bytes from it, refusing a larger file. A FIFO
// or a directory swapped in after the directory listing is refused before any read (#199).
func readSettingsFile(root *os.Root, name string) (b []byte, err error) {
	f, err := root.OpenFile(name, ownerKeyOpenFlag, 0)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file: %s", info.Mode().Type())
	}
	b, err = io.ReadAll(io.LimitReader(f, maxSettingsBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxSettingsBytes {
		return nil, fmt.Errorf("larger than %d bytes", maxSettingsBytes)
	}
	return b, nil
}

// configInvalid returns the config_invalid error with what was expected and what was found.
func configInvalid(expected, found string) error {
	return &Error{Outcome: "config_invalid", Expected: expected, Found: found, Err: ErrConfigInvalid}
}
