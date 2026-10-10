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
		// \??\ is the NT object-manager prefix; Windows reads it as absolute, so it is a device
		// form like \\?\ and is refused like it.
		{`NT device override \??\ with a drive accepted as a root`, `\??\C:\Users\zeynep\aiwe`},
		{`NT device override \??\UNC with a network share accepted as a root`, `\??\UNC\fileserver\share\aiwe`},
		// Declared fail-closed: a local long-path override is refused, not followed (SPEC §6.4).
		{`local long-path override \\?\ with a drive accepted as a root`, `\\?\C:\Users\zeynep\aiwe`},
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
	platformTrailingSeparator(t)
	platformConventionChecks(t)
}

// platformTrailingSeparator checks that an override that ends in a separator names a directory
// below a root, not a root: it is accepted and returned without the separator.
func platformTrailingSeparator(t *testing.T) {
	ovState := filepath.Join(t.TempDir(), "override-state")
	ovCache := filepath.Join(t.TempDir(), "override-cache")
	ovTemp := filepath.Join(t.TempDir(), "override-tmp")
	tmp := filepath.Join(t.TempDir(), "Temp")
	home := filepath.Join(t.TempDir(), "home", "zeynep")
	seps := []struct{ name, sep string }{{"separator", string(filepath.Separator)}}
	if filepath.Separator != '/' {
		seps = append(seps, struct{ name, sep string }{"forward slash", "/"})
	}
	for _, s := range seps {
		t.Run("override with a trailing "+s.name+" refused as a file system root", func(t *testing.T) {
			env := map[string]string{
				"LOCALAPPDATA": filepath.Join(t.TempDir(), "Local"), "TEMP": tmp, "TMPDIR": tmp,
				platformOverride("HOME"): ovState + s.sep, platformOverride("CACHE"): ovCache + s.sep,
				platformOverride("TMP"): ovTemp + s.sep,
			}
			got, err := core.ResolveRoots(runtime.GOOS, func(k string) string { return env[k] }, home)
			if err != nil {
				t.Fatalf("ResolveRoots with overrides ending in %q: %v; want them accepted", s.sep, err)
			}
			if want := (core.Roots{State: ovState, Cache: ovCache, Temp: ovTemp}); got != want {
				t.Errorf("ResolveRoots with overrides ending in %q\n got  %+v\n want %+v", s.sep, got, want)
			}
		})
	}
}

// platformConventionChecks checks that a convention variable (LOCALAPPDATA, TEMP, TMPDIR,
// XDG_STATE_HOME, XDG_CACHE_HOME) passes the same root checks as an override: a .. element, a
// network share or an NT device form never becomes a root. A linux XDG or TMPDIR value that
// fails them may be refused, or ignored for the home or /tmp convention as an unset one is.
func platformConventionChecks(t *testing.T) {
	sep := string(filepath.Separator)
	base := filepath.Join(t.TempDir(), "base")
	tmp := filepath.Join(t.TempDir(), "Temp")
	local := filepath.Join(t.TempDir(), "Local")
	home := filepath.Join(t.TempDir(), "home", "zeynep")
	ovState := filepath.Join(t.TempDir(), "override-state")
	ovCache := filepath.Join(t.TempDir(), "override-cache")
	p := core.Product
	state := filepath.Join(home, ".local", "state", p)
	cache := filepath.Join(home, ".cache", p)
	temp := filepath.Join("/tmp", p)
	for _, bad := range []struct{ name, value string }{
		{"a .. element", base + sep + "a" + sep + ".." + sep + ".." + sep + "elsewhere"},
		{"a network share", "//fileserver/share/aiwe"},
		{"a network share with backslashes", `\\fileserver\share`},
		{`an NT device form \??\`, `\??\C:\Users\zeynep\AppData\Local`},
	} {
		for _, tc := range []struct {
			goos, variable string
			env            map[string]string
			// root returns the root the variable resolves; fallback is that root when an
			// unusable value is ignored, or "" when the value must be refused.
			root     func(core.Roots) string
			fallback string
		}{
			{"windows", "LOCALAPPDATA", map[string]string{"TEMP": tmp},
				func(r core.Roots) string { return r.State }, ""},
			// An override for one root must not let LOCALAPPDATA skip the checks for the other.
			{"windows", "LOCALAPPDATA", map[string]string{"TEMP": tmp, platformOverride("CACHE"): ovCache},
				func(r core.Roots) string { return r.State }, ""},
			{"windows", "LOCALAPPDATA", map[string]string{"TEMP": tmp, platformOverride("HOME"): ovState},
				func(r core.Roots) string { return r.Cache }, ""},
			{"windows", "TEMP", map[string]string{"LOCALAPPDATA": local},
				func(r core.Roots) string { return r.Temp }, ""},
			{"darwin", "TMPDIR", map[string]string{}, func(r core.Roots) string { return r.Temp }, ""},
			{"linux", "XDG_STATE_HOME", map[string]string{}, func(r core.Roots) string { return r.State }, state},
			{"linux", "XDG_CACHE_HOME", map[string]string{}, func(r core.Roots) string { return r.Cache }, cache},
			{"linux", "TMPDIR", map[string]string{}, func(r core.Roots) string { return r.Temp }, temp},
		} {
			t.Run(tc.goos+" "+tc.variable+" with "+bad.name+" taken as a root without the root checks", func(t *testing.T) {
				env := map[string]string{tc.variable: bad.value}
				for k, v := range tc.env {
					env[k] = v
				}
				got, err := core.ResolveRoots(tc.goos, func(k string) string { return env[k] }, home)
				if errors.Is(err, core.ErrRootUnresolved) {
					return
				}
				if err != nil {
					t.Fatalf("ResolveRoots(%s) with %s=%q: %v; want %v or the fallback root",
						tc.goos, tc.variable, bad.value, err, core.ErrRootUnresolved)
				}
				if tc.fallback == "" || tc.root(got) != tc.fallback {
					t.Errorf("ResolveRoots(%s) with %s=%q = %+v, nil; want %v (or root %q when ignored)",
						tc.goos, tc.variable, bad.value, got, core.ErrRootUnresolved, tc.fallback)
				}
			})
		}
	}
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
