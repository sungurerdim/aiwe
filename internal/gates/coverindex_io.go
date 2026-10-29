package gates

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sungurerdim/aiwe/internal/core"
)

// coverReadMax caps one file the coverage index reads: a profile or a Go file it hashes.
const coverReadMax = 64 << 20

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
		limit, err := r.coverLimit(def)
		if err != nil {
			return "coverage index not refreshed: " + err.Error()
		}
		collect = func(ctx context.Context, dir string) (CoverCollection, error) {
			return r.collectCover(ctx, dir, limit)
		}
	}
	got, err := collect(ctx, dir)
	if err == nil {
		var idx CoverIndex
		if idx, err = BuildCoverIndex(got, time.Now()); err == nil {
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

// coverLimit returns the limit, in seconds, of the coverage run of one test package: the limit the
// test step of def is prepared with from its measured durations (DR-1, SPEC §5.13). 0 is no limit
// beyond the gate's context, as for the step.
func (r *Runner) coverLimit(def Definition) (int, error) {
	i := slices.IndexFunc(def.Steps, func(s GateStep) bool { return s.ID == testStepID })
	if i < 0 {
		return 0, errors.New("the gate has no test step to take the coverage run limit from")
	}
	step, _, err := r.prepare(def.Steps[i], def.Proof)
	return step.TimeoutSeconds, err
}

// collectCover runs the tests of each package of the Go module in dir under coverage of the whole
// module, one package at a time per worker, in fresh roots, each bounded by limit seconds, and
// returns each package's profile and the hash of each package's Go files. A package whose run fails
// makes the collection fail, so a half-measured index is never written; a package whose run wrote
// no profile is listed as not measured.
func (r *Runner) collectCover(ctx context.Context, dir string, limit int) (CoverCollection, error) {
	g, err := ListGoPackages(ctx, dir)
	if err != nil {
		return CoverCollection{}, err
	}
	got := CoverCollection{DirOf: map[string]string{}}
	var pkgs []*goNode
	dirs := make([]string, 0, len(g.nodes))
	for _, n := range g.nodes {
		got.DirOf[n.importPath] = n.dir
		dirs = append(dirs, n.dir)
		if n.hasTests {
			pkgs = append(pkgs, n)
		}
	}
	if got.Hashes, err = HashGoDirs(dir, dirs); err != nil {
		return CoverCollection{}, err
	}
	roots, err := r.freshRoots(ctx)
	if err != nil {
		return CoverCollection{}, err
	}
	slices.SortFunc(pkgs, func(a, b *goNode) int { return strings.Compare(a.dir, b.dir) })
	got.Profiles, got.NotMeasured, err = r.coverPackages(ctx, dir, roots, pkgs, limit)
	return got, errors.Join(err, roots.remove(ctx))
}

// coverPackages runs coverPackage for each of pkgs, as many at a time as the worker count, and
// returns the profiles and the slash directories of the packages whose run wrote no profile.
func (r *Runner) coverPackages(ctx context.Context, dir string, roots *checkRoots, pkgs []*goNode, limit int) (CoverProfiles, []string, error) {
	profiles := make(CoverProfiles, len(pkgs))
	errs := make([]error, len(pkgs))
	measured := make([]bool, len(pkgs))
	var mu sync.Mutex
	var wg sync.WaitGroup
	slots := make(chan struct{}, max(1, r.Capacity.Workers))
	for i, n := range pkgs {
		slots <- struct{}{}
		wg.Add(1)
		go func() {
			defer func() { <-slots; wg.Done() }()
			data, ok, err := coverPackage(ctx, dir, roots, i, n, limit)
			mu.Lock()
			defer mu.Unlock()
			errs[i], measured[i] = err, ok
			if ok {
				profiles[n.dir] = data
			}
		}()
	}
	wg.Wait()
	var notMeasured []string
	for i, n := range pkgs {
		if errs[i] == nil && !measured[i] {
			notMeasured = append(notMeasured, n.dir)
		}
	}
	return profiles, notMeasured, errors.Join(errs...)
}

// coverPackage runs the tests of package n under coverage of the whole module, bounded by limit
// seconds, and returns the profile it wrote. A run that wrote no profile is not measured (DR-6).
func coverPackage(ctx context.Context, dir string, roots *checkRoots, i int, n *goNode, limit int) ([]byte, bool, error) {
	profile := filepath.Join(roots.base, fmt.Sprintf("cover-%d.out", i))
	step := GateStep{ID: "cover", TimeoutSeconds: limit, Command: []string{"go", "test", "-count=1",
		"-coverpkg=" + coverFullSelection, "-coverprofile=" + profile, n.importPath}}
	var all outputLog
	code, _, failed := execStep(ctx, step, core.ChildSpec{Dir: dir, Env: roots.env(), Stdout: &all, Stderr: &all})
	switch {
	case failed != nil:
		return nil, false, fmt.Errorf("%s: %s", n.importPath, failed.Detail)
	case code != 0:
		return nil, false, fmt.Errorf("`go test %s` exited %d: %s", n.importPath, code, lastLine(all.Bytes()))
	}
	return readCoverProfile(roots.base, filepath.Base(profile))
}

// lastLine returns the last non-empty line of out.
func lastLine(out []byte) string {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// readCoverProfile reads the profile name under dir through an os.Root, so a link cannot lead out.
// A run that wrote no profile is not measured: measured is false and the error nil, never an empty
// profile that covers nothing (DR-6).
func readCoverProfile(dir, name string) (data []byte, measured bool, err error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, false, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	data, err = core.ReadRegularCapped(root, name, coverReadMax)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	return data, err == nil, err
}

// HashGoDirs returns the content hash of the Go files of each of dirs, slash directories relative to
// root. A directory with no Go file, or no directory at all, is no Go package and gets no entry. The
// hash covers each Go file's name, length and bytes in name order, read through an os.Root (DR-2).
func HashGoDirs(root string, dirs []string) (sums map[string]string, err error) {
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, r.Close()) }()
	sums = map[string]string{}
	for _, d := range dirs {
		sum, ok, hashErr := hashGoDir(r, d)
		if hashErr != nil {
			return nil, hashErr
		}
		if ok {
			sums[d] = sum
		}
	}
	return sums, nil
}

// hashGoDir hashes the Go files of dir under root; ok is false when dir holds none.
func hashGoDir(root *os.Root, dir string) (sum string, ok bool, err error) {
	entries, err := fs.ReadDir(root.FS(), dir)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	h := sha256.New()
	for _, e := range entries {
		if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		data, readErr := core.ReadRegularCapped(root, filepath.FromSlash(path.Join(dir, e.Name())), coverReadMax)
		if readErr != nil {
			return "", false, readErr
		}
		goFileSum(h, e.Name(), data)
		ok = true
	}
	return hex.EncodeToString(h.Sum(nil)), ok, nil
}

// goFileSum adds one Go file to the hash of its package: its name, its length and its bytes. The
// work tree (hashGoDir) and the base commit (BaseGoHashes) both hash through it, so the same files
// give the same hash.
func goFileSum(h hash.Hash, name string, data []byte) {
	_, _ = fmt.Fprintf(h, "%s %d\n", name, len(data))
	_, _ = h.Write(data)
}

// baseGoFile is one Go file of a package at the base commit.
type baseGoFile struct {
	dir, name string
	data      []byte
}

// BaseGoHashes returns the content hash of the Go files of each of dirs, slash directories relative
// to the repository root, at the commit base of the repository in repo: the hash HashGoDirs gives
// the same files in a work tree (DR-2). One `git ls-tree` lists the tree and one `git cat-file
// --batch` reads the blobs (K31). A directory with no Go file at the base gets no entry; a link or
// a submodule is no Go file, as in HashGoDirs.
func BaseGoHashes(ctx context.Context, repo, base string, dirs []string) (map[string]string, error) {
	if base == "" || strings.HasPrefix(base, "-") || strings.ContainsAny(base, "\r\n") {
		return nil, coverError("a base commit", fmt.Sprintf("the base %q", base), nil)
	}
	tree, err := core.Git(ctx, repo, "ls-tree", "-r", "-z", "--full-tree", base)
	if err != nil {
		return nil, err
	}
	files, request := baseGoTree(tree.Stdout, dirs)
	if len(files) == 0 {
		return map[string]string{}, nil
	}
	blobs, err := core.GitInput(ctx, repo, request, "cat-file", "--batch")
	if err != nil {
		return nil, err
	}
	rest := blobs.Stdout
	for i := range files {
		if files[i].data, rest, err = nextBlob(rest); err != nil {
			return nil, coverError("the Go blobs of "+base, err.Error(), err)
		}
	}
	return sumByDir(files), nil
}

// baseGoTree reads the `git ls-tree -r -z` output tree and returns the regular Go files in dirs, in
// tree order, and the `cat-file --batch` request that reads their blobs in the same order.
func baseGoTree(tree string, dirs []string) ([]baseGoFile, string) {
	var files []baseGoFile
	var request strings.Builder
	for _, entry := range strings.Split(tree, "\x00") {
		meta, name, ok := strings.Cut(entry, "\t")
		f := strings.Fields(meta)
		if !ok || len(f) != 3 || f[1] != "blob" || (f[0] != "100644" && f[0] != "100755") ||
			!strings.HasSuffix(name, ".go") || !slices.Contains(dirs, path.Dir(name)) {
			continue
		}
		files = append(files, baseGoFile{dir: path.Dir(name), name: path.Base(name)})
		request.WriteString(f[2] + "\n")
	}
	return files, request.String()
}

// nextBlob reads one answer of `cat-file --batch`, `<oid> blob <size>`, the bytes and a newline,
// and returns the bytes and the rest of the output. Any other answer is an error (DR-6).
func nextBlob(out string) (data []byte, rest string, err error) {
	header, tail, _ := strings.Cut(out, "\n")
	f := strings.Fields(header)
	if len(f) != 3 || f[1] != "blob" {
		return nil, "", fmt.Errorf("the answer %q is not a blob", header)
	}
	size, err := strconv.Atoi(f[2])
	if err != nil || size < 0 || size > coverReadMax || size >= len(tail) || tail[size] != '\n' {
		return nil, "", fmt.Errorf("the blob %s does not hold its %s bytes", f[0], f[2])
	}
	return []byte(tail[:size]), tail[size+1:], nil
}

// sumByDir hashes the files of each directory in name order through goFileSum.
func sumByDir(files []baseGoFile) map[string]string {
	slices.SortFunc(files, func(a, b baseGoFile) int {
		return cmp.Or(strings.Compare(a.dir, b.dir), strings.Compare(a.name, b.name))
	})
	sums := map[string]string{}
	for start := 0; start < len(files); {
		h, end := sha256.New(), start
		for ; end < len(files) && files[end].dir == files[start].dir; end++ {
			goFileSum(h, files[end].name, files[end].data)
		}
		sums[files[start].dir] = hex.EncodeToString(h.Sum(nil))
		start = end
	}
	return sums
}
