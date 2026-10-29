package gates

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/sungurerdim/aiwe/internal/core"
)

// coverRunLimit bounds the coverage run of one test package (DR-1).
const coverRunLimit = 600

// CoverRefresh tells a Runner where the coverage reverse index lives and how to measure it (SPEC
// L66). A Runner with no CoverRefresh keeps no index. Collect measures coverage in dir; nil runs
// every test package of the module under coverage, in fresh roots.
type CoverRefresh struct {
	Path    string
	Collect func(ctx context.Context, dir string) (CoverCollection, error)
}

// LoadCoverIndex reads the index file. A missing file is an index that was never built: the zero
// index, which is stale. A file that is not an index of this format is an error (DR-6).
func LoadCoverIndex(file string) (CoverIndex, error) {
	root, err := os.OpenRoot(filepath.Dir(file))
	if errors.Is(err, fs.ErrNotExist) {
		return CoverIndex{}, nil
	}
	if err != nil {
		return CoverIndex{}, err
	}
	data, err := root.ReadFile(filepath.Base(file))
	err = errors.Join(err, root.Close())
	if errors.Is(err, fs.ErrNotExist) {
		return CoverIndex{}, nil
	}
	if err != nil {
		return CoverIndex{}, err
	}
	return ParseCoverIndex(data)
}

// ParseCoverIndex reads the JSON of an index file; it refuses an unknown field, a format other
// than the current one and data after the object.
func ParseCoverIndex(data []byte) (CoverIndex, error) {
	var idx CoverIndex
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&idx); err != nil {
		return CoverIndex{}, coverError("a coverage index object", err.Error(), err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return CoverIndex{}, coverError("one JSON object", "data after the object", nil)
	}
	if idx.FormatVersion != coverFormatVersion {
		return CoverIndex{}, coverError(fmt.Sprintf("formatVersion %d", coverFormatVersion),
			fmt.Sprintf("formatVersion %d", idx.FormatVersion), nil)
	}
	return idx, nil
}

// SaveCoverIndex writes idx to a new temporary file next to file and renames it over file, so a
// reader sees the whole old index or the whole new one.
func SaveCoverIndex(file string, idx CoverIndex) error {
	data, err := json.Marshal(idx)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o750); err != nil {
		return err
	}
	root, err := os.OpenRoot(filepath.Dir(file))
	if err != nil {
		return err
	}
	name, tmp := filepath.Base(file), filepath.Base(file)+".tmp"
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err == nil {
		_, err = f.Write(append(data, '\n'))
		if err = errors.Join(err, f.Close()); err == nil {
			err = root.Rename(tmp, name)
		}
		if err != nil {
			_ = root.Remove(tmp)
		}
	}
	return errors.Join(err, root.Close())
}

// refreshCoverIndex rebuilds the index after a green full run and returns the note a failed
// refresh adds to the gate detail, "" otherwise. Only a run that left no step out and whose full
// `go test ./...` step is green refreshes; any other run leaves the index as it is (SPEC L66). A
// refresh that fails keeps the gate result, because the tests did pass, and says so.
func (r *Runner) refreshCoverIndex(ctx context.Context, def Definition, report GateReport, dir string) string {
	if r.Cover == nil || report.Result != ResultGreen || report.Partial || !fullTestGreen(def, report) {
		return ""
	}
	collect := r.Cover.Collect
	if collect == nil {
		collect = r.collectCover
	}
	got, err := collect(ctx, dir)
	if err == nil {
		var idx CoverIndex
		if idx, err = BuildCoverIndex(got.Profiles, got.DirOf, time.Now()); err == nil {
			err = SaveCoverIndex(r.Cover.Path, idx)
		}
	}
	if err != nil {
		return "coverage index not refreshed: " + err.Error()
	}
	return ""
}

// fullTestGreen reports whether the test step of def runs go test over every package and its report
// is green.
func fullTestGreen(def Definition, report GateReport) bool {
	i := slices.IndexFunc(def.Steps, func(s GateStep) bool { return s.ID == testStepID })
	return i >= 0 && i < len(report.Steps) && isFullGoTest(def.Steps[i].Command) &&
		report.Steps[i].Result == ResultGreen
}

// collectCover runs the tests of each package of the Go module in dir under coverage of the whole
// module, one package at a time per worker, in fresh roots, and returns each package's profile. A
// package whose run fails makes the collection fail, so a half-measured index is never written.
func (r *Runner) collectCover(ctx context.Context, dir string) (CoverCollection, error) {
	g, err := ListGoPackages(ctx, dir)
	if err != nil {
		return CoverCollection{}, err
	}
	roots, err := r.freshRoots(ctx)
	if err != nil {
		return CoverCollection{}, err
	}
	got := CoverCollection{DirOf: map[string]string{}}
	var pkgs []*goNode
	for _, n := range g.nodes {
		got.DirOf[n.importPath] = n.dir
		if n.hasTests {
			pkgs = append(pkgs, n)
		}
	}
	slices.SortFunc(pkgs, func(a, b *goNode) int { return strings.Compare(a.dir, b.dir) })
	got.Profiles, err = r.coverPackages(ctx, dir, roots, pkgs)
	return got, errors.Join(err, roots.remove(ctx))
}

// coverPackages runs coverPackage for each of pkgs, as many at a time as the worker count.
func (r *Runner) coverPackages(ctx context.Context, dir string, roots *checkRoots, pkgs []*goNode) (CoverProfiles, error) {
	profiles := make(CoverProfiles, len(pkgs))
	errs := make([]error, len(pkgs))
	var mu sync.Mutex
	var wg sync.WaitGroup
	slots := make(chan struct{}, max(1, r.Capacity.Workers))
	for i, n := range pkgs {
		slots <- struct{}{}
		wg.Add(1)
		go func() {
			defer func() { <-slots; wg.Done() }()
			data, err := coverPackage(ctx, dir, roots, i, n)
			mu.Lock()
			defer mu.Unlock()
			errs[i], profiles[n.dir] = err, data
		}()
	}
	wg.Wait()
	return profiles, errors.Join(errs...)
}

// coverPackage runs the tests of package n under coverage of the whole module and returns the
// profile it wrote. A package whose tests ran no statement writes no profile: its profile is empty.
func coverPackage(ctx context.Context, dir string, roots *checkRoots, i int, n *goNode) ([]byte, error) {
	profile := filepath.Join(roots.base, fmt.Sprintf("cover-%d.out", i))
	step := GateStep{ID: "cover", TimeoutSeconds: coverRunLimit,
		Command: withCoverage([]string{"go", "test", "-count=1", n.importPath}, profile)}
	var all outputLog
	code, _, failed := execStep(ctx, step, core.ChildSpec{Dir: dir, Env: roots.env(), Stdout: &all, Stderr: &all})
	switch {
	case failed != nil:
		return nil, fmt.Errorf("%s: %s", n.importPath, failed.Detail)
	case code != 0:
		return nil, fmt.Errorf("`go test %s` exited %d: %s", n.importPath, code, lastLine(all.Bytes()))
	}
	data, err := os.ReadFile(profile)
	if errors.Is(err, fs.ErrNotExist) {
		return []byte("mode: set\n"), nil
	}
	return data, err
}

// lastLine returns the last non-empty line of out.
func lastLine(out []byte) string {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
