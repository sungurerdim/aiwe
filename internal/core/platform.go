package core

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
)

// Product is the one product name constant; directory and env variable names derive from it
// (DR-25).
const Product = "aiwe"

// ErrRootUnresolved reports a disk root that neither its override nor its OS convention resolves.
var ErrRootUnresolved = errors.New("root_unresolved")

// Roots are the three disk roots of SPEC §6.4.
type Roots struct {
	State string
	Cache string
	Temp  string
}

// productEnv returns the env variable name of suffix, derived from the product constant.
func productEnv(suffix string) string {
	return strings.ToUpper(Product) + "_" + suffix
}

// UserRoots resolves the disk roots of this process on the host OS (DR-37).
func UserRoots() (Roots, error) {
	return ResolveRoots(runtime.GOOS, processGetenv, processHome())
}

// ResolveRoots resolves the disk roots for goos from getenv and the user's home directory. Per
// root, a non-empty override wins; else the OS convention, first hit per cell (SPEC §6.4). A root
// that would be relative or guessed is an error (fail-closed).
func ResolveRoots(goos string, getenv func(string) string, home string) (Roots, error) {
	state, err := resolveRoot(getenv, "HOME", func() (string, error) { return stateConvention(goos, getenv, home) })
	if err != nil {
		return Roots{}, fmt.Errorf("state root: %w", err)
	}
	cache, err := resolveRoot(getenv, "CACHE", func() (string, error) { return cacheConvention(goos, getenv, home) })
	if err != nil {
		return Roots{}, fmt.Errorf("cache root: %w", err)
	}
	temp, err := resolveRoot(getenv, "TMP", func() (string, error) { return tempConvention(goos, getenv) })
	if err != nil {
		return Roots{}, fmt.Errorf("temp root: %w", err)
	}
	return Roots{State: state, Cache: cache, Temp: temp}, nil
}

// resolveRoot returns the override of suffix when it is set, else the convention.
func resolveRoot(getenv func(string) string, suffix string, convention func() (string, error)) (string, error) {
	name := productEnv(suffix)
	if v := getenv(name); v != "" {
		return absDir(name, v)
	}
	return convention()
}

func stateConvention(goos string, getenv func(string) string, home string) (string, error) {
	switch goos {
	case "windows":
		local, err := absDir("LOCALAPPDATA", getenv("LOCALAPPDATA"))
		return filepath.Join(local, Product, "state"), err
	case "darwin":
		h, err := absDir("home directory", home)
		return filepath.Join(h, "Library", "Application Support", Product), err
	default:
		if x, err := absDir("XDG_STATE_HOME", getenv("XDG_STATE_HOME")); err == nil {
			return filepath.Join(x, Product), nil
		}
		h, err := absDir("home directory", home)
		return filepath.Join(h, ".local", "state", Product), err
	}
}

func cacheConvention(goos string, getenv func(string) string, home string) (string, error) {
	switch goos {
	case "windows":
		local, err := absDir("LOCALAPPDATA", getenv("LOCALAPPDATA"))
		return filepath.Join(local, Product, "cache"), err
	case "darwin":
		h, err := absDir("home directory", home)
		return filepath.Join(h, "Library", "Caches", Product), err
	default:
		if x, err := absDir("XDG_CACHE_HOME", getenv("XDG_CACHE_HOME")); err == nil {
			return filepath.Join(x, Product), nil
		}
		h, err := absDir("home directory", home)
		return filepath.Join(h, ".cache", Product), err
	}
}

func tempConvention(goos string, getenv func(string) string) (string, error) {
	switch goos {
	case "windows":
		t, err := absDir("TEMP", getenv("TEMP"))
		return filepath.Join(t, Product), err
	case "darwin":
		t, err := absDir("TMPDIR", getenv("TMPDIR"))
		return filepath.Join(t, Product), err
	default:
		if t, err := absDir("TMPDIR", getenv("TMPDIR")); err == nil {
			return filepath.Join(t, Product), nil
		}
		return filepath.Join("/tmp", Product), nil
	}
}

// absDir returns v when it is a non-empty absolute path; name says where v came from.
func absDir(name, v string) (string, error) {
	if v == "" {
		return "", fmt.Errorf("%w: %s is not set", ErrRootUnresolved, name)
	}
	if !filepath.IsAbs(v) {
		return "", fmt.Errorf("%w: %s is not an absolute path", ErrRootUnresolved, name)
	}
	return v, nil
}
