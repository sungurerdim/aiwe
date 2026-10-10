package core_test

import (
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/sungurerdim/aiwe/internal/core"
)

// platformOverride returns the override variable of a root, derived from the product constant
// (SPEC §6.4).
func platformOverride(suffix string) string {
	return strings.ToUpper(core.Product) + "_" + suffix
}

func TestPlatform(t *testing.T) {
	local := filepath.Join(t.TempDir(), "Local")
	tmp := filepath.Join(t.TempDir(), "Temp")
	home := filepath.Join(t.TempDir(), "home", "zeynep")
	xdgState := filepath.Join(t.TempDir(), "xdg-state")
	xdgCache := filepath.Join(t.TempDir(), "xdg-cache")
	ovState := filepath.Join(t.TempDir(), "override-state")
	ovCache := filepath.Join(t.TempDir(), "override-cache")
	ovTemp := filepath.Join(t.TempDir(), "override-tmp")
	p := core.Product
	posixDefault := core.Roots{
		State: filepath.Join(home, ".local", "state", p),
		Cache: filepath.Join(home, ".cache", p),
		Temp:  filepath.Join("/tmp", p),
	}
	tests := []struct {
		name    string
		goos    string
		env     map[string]string
		home    string
		want    core.Roots
		wantErr bool
	}{
		{
			name: "windows roots taken from a fixed path instead of LOCALAPPDATA and TEMP",
			goos: "windows", env: map[string]string{"LOCALAPPDATA": local, "TEMP": tmp}, home: home,
			want: core.Roots{State: filepath.Join(local, p, "state"), Cache: filepath.Join(local, p, "cache"),
				Temp: filepath.Join(tmp, p)},
		},
		{
			name: "macOS roots taken from a fixed path instead of the home Library and TMPDIR",
			goos: "darwin", env: map[string]string{"TMPDIR": tmp}, home: home,
			want: core.Roots{State: filepath.Join(home, "Library", "Application Support", p),
				Cache: filepath.Join(home, "Library", "Caches", p), Temp: filepath.Join(tmp, p)},
		},
		{
			name: "linux roots that ignore XDG_STATE_HOME, XDG_CACHE_HOME and TMPDIR",
			goos: "linux",
			env:  map[string]string{"XDG_STATE_HOME": xdgState, "XDG_CACHE_HOME": xdgCache, "TMPDIR": tmp},
			home: home,
			want: core.Roots{State: filepath.Join(xdgState, p), Cache: filepath.Join(xdgCache, p),
				Temp: filepath.Join(tmp, p)},
		},
		{
			name: "linux roots without XDG variables not under the home convention and /tmp",
			goos: "linux", env: map[string]string{}, home: home, want: posixDefault,
		},
		{
			name: "other POSIX system not resolved like linux",
			goos: "freebsd", env: map[string]string{}, home: home, want: posixDefault,
		},
		{
			name: "windows override variables not winning over the OS convention",
			goos: "windows",
			env: map[string]string{"LOCALAPPDATA": local, "TEMP": tmp, platformOverride("HOME"): ovState,
				platformOverride("CACHE"): ovCache, platformOverride("TMP"): ovTemp},
			home: home,
			want: core.Roots{State: ovState, Cache: ovCache, Temp: ovTemp},
		},
		{
			name: "linux override variables not winning over XDG variables",
			goos: "linux",
			env: map[string]string{"XDG_STATE_HOME": xdgState, "XDG_CACHE_HOME": xdgCache, "TMPDIR": tmp,
				platformOverride("HOME"): ovState, platformOverride("CACHE"): ovCache, platformOverride("TMP"): ovTemp},
			home: home,
			want: core.Roots{State: ovState, Cache: ovCache, Temp: ovTemp},
		},
		{
			name: "one override applied to the other roots too",
			goos: "darwin", env: map[string]string{"TMPDIR": tmp, platformOverride("TMP"): ovTemp}, home: home,
			want: core.Roots{State: filepath.Join(home, "Library", "Application Support", p),
				Cache: filepath.Join(home, "Library", "Caches", p), Temp: ovTemp},
		},
		{
			name: "empty override taken as a root instead of falling back to the convention",
			goos: "windows",
			env:  map[string]string{"LOCALAPPDATA": local, "TEMP": tmp, platformOverride("HOME"): ""},
			home: home,
			want: core.Roots{State: filepath.Join(local, p, "state"), Cache: filepath.Join(local, p, "cache"),
				Temp: filepath.Join(tmp, p)},
		},
		{
			name: "windows without LOCALAPPDATA resolved to a guessed path instead of an error",
			goos: "windows", env: map[string]string{"TEMP": tmp}, home: home, wantErr: true,
		},
		{
			name: "linux without XDG variables and without a home resolved to a relative path",
			goos: "linux", env: map[string]string{}, home: "", wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			getenv := func(k string) string { return tc.env[k] }
			got, err := core.ResolveRoots(tc.goos, getenv, tc.home)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ResolveRoots(%s) = %+v, nil; want an error", tc.goos, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveRoots(%s): %v", tc.goos, err)
			}
			if got != tc.want {
				t.Errorf("ResolveRoots(%s)\n got  %+v\n want %+v", tc.goos, got, tc.want)
			}
		})
	}
	sep := string(filepath.Separator)
	for _, tc := range []struct{ name, override string }{
		// Cleaning a path with .. can differ from the OS walk through a link, so the root is
		// refused rather than cleaned (fail-closed, DR-37).
		{"override with a .. element accepted instead of refused, since cleaning can differ from the OS walk",
			ovState + sep + ".." + sep + "elsewhere"},
		{"override with a trailing .. element accepted instead of refused", ovState + sep + "x" + sep + ".."},
		{"override with a slash-separated .. element accepted instead of refused", ovState + "/../elsewhere"},
		{"network share override with forward slashes accepted as a root", "//fileserver/share/aiwe"},
		{"network share override with backslashes accepted as a root", `\\fileserver\share`},
		{"network share override in long-path form accepted as a root", `\\?\UNC\fileserver\share\aiwe`},
		{"drive root override with a forward slash accepted as a root", "C:/"},
		{"drive root override with a backslash accepted as a root", `C:\`},
		{"file system root override accepted as a root", "/"},
	} {
		for _, suffix := range []string{"HOME", "CACHE", "TMP"} {
			t.Run(tc.name+" ("+platformOverride(suffix)+")", func(t *testing.T) {
				env := map[string]string{"LOCALAPPDATA": local, "TEMP": tmp, "TMPDIR": tmp, platformOverride(suffix): tc.override}
				got, err := core.ResolveRoots(runtime.GOOS, func(k string) string { return env[k] }, home)
				if !errors.Is(err, core.ErrRootUnresolved) {
					t.Errorf("ResolveRoots with %s=%q = %+v, %v; want %v",
						platformOverride(suffix), tc.override, got, err, core.ErrRootUnresolved)
				}
			})
		}
	}
	t.Run("user roots read on this OS from a fixed path instead of its env", func(t *testing.T) {
		platformUserRoots(t)
	})
}

// platformUserRoots checks the run-time entry on the host OS: conventions first, then overrides.
func platformUserRoots(t *testing.T) {
	local := filepath.Join(t.TempDir(), "Local")
	tmp := filepath.Join(t.TempDir(), "Temp")
	home := filepath.Join(t.TempDir(), "home", "zeynep")
	xdgState := filepath.Join(t.TempDir(), "xdg-state")
	xdgCache := filepath.Join(t.TempDir(), "xdg-cache")
	for _, k := range []string{"HOME", "CACHE", "TMP"} {
		t.Setenv(platformOverride(k), "")
	}
	t.Setenv("LOCALAPPDATA", local)
	t.Setenv("TEMP", tmp)
	t.Setenv("TMPDIR", tmp)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_STATE_HOME", xdgState)
	t.Setenv("XDG_CACHE_HOME", xdgCache)
	p := core.Product
	var want core.Roots
	switch runtime.GOOS {
	case "windows":
		want = core.Roots{State: filepath.Join(local, p, "state"), Cache: filepath.Join(local, p, "cache"),
			Temp: filepath.Join(tmp, p)}
	case "darwin":
		want = core.Roots{State: filepath.Join(home, "Library", "Application Support", p),
			Cache: filepath.Join(home, "Library", "Caches", p), Temp: filepath.Join(tmp, p)}
	default:
		want = core.Roots{State: filepath.Join(xdgState, p), Cache: filepath.Join(xdgCache, p),
			Temp: filepath.Join(tmp, p)}
	}
	got, err := core.UserRoots()
	if err != nil {
		t.Fatalf("UserRoots: %v", err)
	}
	if got != want {
		t.Errorf("UserRoots on %s\n got  %+v\n want %+v", runtime.GOOS, got, want)
	}
	ovState := filepath.Join(t.TempDir(), "override-state")
	ovCache := filepath.Join(t.TempDir(), "override-cache")
	ovTemp := filepath.Join(t.TempDir(), "override-tmp")
	t.Setenv(platformOverride("HOME"), ovState)
	t.Setenv(platformOverride("CACHE"), ovCache)
	t.Setenv(platformOverride("TMP"), ovTemp)
	got, err = core.UserRoots()
	if err != nil {
		t.Fatalf("UserRoots with overrides: %v", err)
	}
	if want := (core.Roots{State: ovState, Cache: ovCache, Temp: ovTemp}); got != want {
		t.Errorf("UserRoots with overrides on %s\n got  %+v\n want %+v", runtime.GOOS, got, want)
	}
}
