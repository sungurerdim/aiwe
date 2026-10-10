package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// ErrConfigInvalid reports config input that cannot be read as settings (fail-closed).
var ErrConfigInvalid = errors.New("config_invalid")

// Origin names the layer a setting value came from.
type Origin string

// The config layers of SPEC §5.12, highest precedence first.
const (
	OriginFlag    Origin = "flag"
	OriginEnv     Origin = "env"
	OriginState   Origin = "state"
	OriginDefault Origin = "default"
)

// ConfigKey declares one setting: its name, env variable, state file under the state root and
// built-in default. An empty Env or File skips that layer.
type ConfigKey struct {
	Name    string
	Env     string
	File    string
	Default string
}

// Setting is one resolved value and its origin.
type Setting struct {
	Name   string
	Value  string
	Origin Origin
}

// ConfigLayers are the sources ResolveConfig reads. LookupEnv is required; the process
// environment is never read behind the caller's back.
type ConfigLayers struct {
	Flags     map[string]string
	LookupEnv func(string) (string, bool)
	StateDir  string
}

// ResolveConfig resolves each key on its own: flags > env > state files > defaults (SPEC §5.12).
// A missing state file is absent; an unreadable or unparsable one is an error.
func ResolveConfig(keys []ConfigKey, l ConfigLayers) ([]Setting, error) {
	if l.LookupEnv == nil {
		return nil, fmt.Errorf("%w: no env lookup given", ErrConfigInvalid)
	}
	files := map[string]map[string]json.RawMessage{}
	out := make([]Setting, 0, len(keys))
	for _, k := range keys {
		if v, ok := l.Flags[k.Name]; ok {
			out = append(out, Setting{Name: k.Name, Value: v, Origin: OriginFlag})
			continue
		}
		if k.Env != "" {
			if v, ok := l.LookupEnv(k.Env); ok {
				out = append(out, Setting{Name: k.Name, Value: v, Origin: OriginEnv})
				continue
			}
		}
		if k.File != "" {
			values, ok := files[k.File]
			if !ok {
				var err error
				if values, err = readStateFile(l.StateDir, k.File); err != nil {
					return nil, err
				}
				files[k.File] = values
			}
			if msg, ok := values[k.Name]; ok {
				var v string
				if err := json.Unmarshal(msg, &v); err != nil || string(msg) == "null" {
					return nil, fmt.Errorf("%w: state file %s: %s is not a string", ErrConfigInvalid, k.File, k.Name)
				}
				out = append(out, Setting{Name: k.Name, Value: v, Origin: OriginState})
				continue
			}
		}
		out = append(out, Setting{Name: k.Name, Value: k.Default, Origin: OriginDefault})
	}
	return out, nil
}

// readStateFile reads the JSON object of file under dir; a missing file reads as no values.
// Keys no ConfigKey declares are ignored.
func readStateFile(dir, file string) (map[string]json.RawMessage, error) {
	if !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("%w: state directory is not an absolute path", ErrConfigInvalid)
	}
	if filepath.Base(file) != file {
		return nil, fmt.Errorf("%w: state file %q is not a plain file name", ErrConfigInvalid, file)
	}
	body, err := os.ReadFile(filepath.Join(dir, file))
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]json.RawMessage{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%w: read state file %s: %v", ErrConfigInvalid, file, err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("%w: parse state file %s: %v", ErrConfigInvalid, file, err)
	}
	if raw == nil {
		return nil, fmt.Errorf("%w: state file %s holds null, not an object", ErrConfigInvalid, file)
	}
	return raw, nil
}
