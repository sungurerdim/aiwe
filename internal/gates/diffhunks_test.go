package gates

import (
	"reflect"
	"testing"
)

// diffhunksTestAdded is a zero-context diff of two files: two hunks in a.go (an insertion and a
// replacement) and a new file.
const diffhunksTestAdded = `diff --git a/a.go b/a.go
index 1111111..2222222 100644
--- a/a.go
+++ b/a.go
@@ -3,0 +4,2 @@ func first() {
+one
+two
@@ -10 +12 @@ func second() {
-old
+new
diff --git a/pkg/b.go b/pkg/b.go
new file mode 100644
index 0000000..3333333
--- /dev/null
+++ b/pkg/b.go
@@ -0,0 +1,3 @@
+x
+y
+z
`

// TestParseDiffHunks catches a hunk reader that drops an added range, names the old path of a
// rename, reports a deleted file, or lets an added line that looks like a diff header forge a
// file or a range (DR-9).
func TestParseDiffHunks(t *testing.T) {
	for _, tc := range []struct {
		name string
		diff string
		want DiffHunks
	}{
		{"added ranges per file, an insertion, a replacement and a new file", diffhunksTestAdded,
			DiffHunks{"a.go": {{4, 5}, {12, 12}}, "pkg/b.go": {{1, 3}}}},
		{"a rename with an edit keeps the new path only",
			"diff --git a/old.go b/new.go\nsimilarity index 80%\nrename from old.go\nrename to new.go\n" +
				"index 111..222 100644\n--- a/old.go\n+++ b/new.go\n@@ -2 +2 @@\n-x\n+y\n",
			DiffHunks{"new.go": {{2, 2}}}},
		{"a pure rename has no hunk and no entry",
			"diff --git a/old.go b/new.go\nsimilarity index 100%\nrename from old.go\nrename to new.go\n",
			DiffHunks{}},
		{"a deleted file gives no range",
			"diff --git a/gone.go b/gone.go\ndeleted file mode 100644\nindex 111..000\n" +
				"--- a/gone.go\n+++ /dev/null\n@@ -1,2 +0,0 @@\n-a\n-b\n",
			DiffHunks{}},
		{"a pure deletion in a kept file gives no range",
			"diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -5,2 +4,0 @@\n-a\n-b\n",
			DiffHunks{}},
		{"an added line that reads as a file header forges no file",
			"diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -0,0 +1,3 @@\n+x\n+++ b/evil.go\n" +
				"+@@ -0,0 +9,9 @@\n",
			DiffHunks{"a.go": {{1, 3}}}},
		{"a removed line that reads as a file header forges no file",
			"diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -4,2 +4 @@\n--- a/evil.go\n-y\n+z\n",
			DiffHunks{"a.go": {{4, 4}}}},
		{"a quoted path is decoded to its bytes",
			"diff --git \"a/na\\303\\257ve.go\" \"b/na\\303\\257ve.go\"\n--- \"a/na\\303\\257ve.go\"\n" +
				"+++ \"b/na\\303\\257ve.go\"\n@@ -1 +1 @@\n-a\n+b\n",
			DiffHunks{"na\u00efve.go": {{1, 1}}}},
		{"a path with a space drops the tab git appends",
			"diff --git a/my file.go b/my file.go\n--- a/my file.go\t\n+++ b/my file.go\t\n@@ -1 +1 @@\n-a\n+b\n",
			DiffHunks{"my file.go": {{1, 1}}}},
		{"a no-newline marker is not a line of the hunk",
			"diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-a\n\\ No newline at end of file\n" +
				"+b\n\\ No newline at end of file\n@@ -9,0 +10 @@\n+c\n",
			DiffHunks{"a.go": {{1, 1}, {10, 10}}}},
		{"a binary file has no hunk",
			"diff --git a/x.bin b/x.bin\nindex 111..222 100644\nBinary files a/x.bin and b/x.bin differ\n",
			DiffHunks{}},
		{"a hunk header with a bad number is skipped",
			"diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1 +x @@\n@@ -1 +99999999999999999999 @@\n",
			DiffHunks{}},
		{"a hunk before any file header belongs to no file", "@@ -0,0 +1 @@\n+a\n", DiffHunks{}},
		{"an empty diff", "", DiffHunks{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParseDiffHunks(tc.diff); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ParseDiffHunks = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestReadDiffHunks catches a reader that counts from a ref other than the run's base commit, that
// accepts a HEAD~n base (DR-9), or that reads hunks from anything but Git's own output.
func TestReadDiffHunks(t *testing.T) {
	dir := cacheTestRepo(t, map[string]string{"a.go": "one\ntwo\nthree\n"})
	base := cacheTestGit(t, dir, "", "rev-parse", "HEAD")
	cacheTestCommit(t, dir, map[string]string{"a.go": "one\ntwo\nthree\nfour\n", "b.go": "x\n"})
	tip := cacheTestGit(t, dir, "", "rev-parse", "HEAD")

	got, err := ReadDiffHunks(t.Context(), dir, base, tip)
	if err != nil {
		t.Fatalf("ReadDiffHunks: %v", err)
	}
	want := DiffHunks{"a.go": {{4, 4}}, "b.go": {{1, 1}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ReadDiffHunks = %v, want %v", got, want)
	}
	for _, bad := range []string{"", "-p", "HEAD~1", "HEAD^", base + "~1"} {
		if _, err := ReadDiffHunks(t.Context(), dir, bad, tip); err == nil {
			t.Errorf("ReadDiffHunks with base %q succeeded, want a refusal", bad)
		}
	}
}

// FuzzParseDiffHunks catches a hunk reader that panics on untrusted diff text or that returns an
// empty, reversed or unnamed range (STD-19).
func FuzzParseDiffHunks(f *testing.F) {
	f.Add(diffhunksTestAdded)
	f.Add("diff --git a/a b/a\n--- a/a\n+++ b/a\n@@ -1,0 +1,2147483648 @@\n+x\n")
	f.Add("+++ b/a\n@@ -1 +1 @@\n+++ b/b\n@@ -0,0 +0,0 @@\n")
	f.Add("diff --git a/a b/a\n+++ \"b/\\")
	f.Add("")
	f.Fuzz(func(t *testing.T, diff string) {
		for file, ranges := range ParseDiffHunks(diff) {
			if file == "" || len(ranges) == 0 {
				t.Fatalf("ParseDiffHunks(%q) kept the entry %q with %d range(s)", diff, file, len(ranges))
			}
			for _, r := range ranges {
				if r.Start < 1 || r.End < r.Start {
					t.Fatalf("ParseDiffHunks(%q) gave the invalid range %v for %q", diff, r, file)
				}
			}
		}
		if strings.Contains(diff, "\x00") {
			return
		}
	})
}
