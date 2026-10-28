package ledger

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// writers is how many goroutines append to one ledger file at once.
const writers = 10

// TestAppendFileTenWritersLoseNoRecord pins L67: ten writers appending to one ledger file at once
// lose no record, and the prev of each record is the hash of the line before it (L59, SPEC §8.5).
func TestAppendFileTenWritersLoseNoRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range writers {
		wg.Go(func() {
			rec := ledgerTestCheck()
			rec.Key = fmt.Sprintf("test:writer-%02d", i)
			<-start
			if err := AppendFile(t.Context(), path, rec); err != nil {
				t.Errorf("AppendFile by writer %d error = %v, want nil", i, err)
			}
		})
	}
	close(start)
	wg.Wait()

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read ledger %s: %v", path, err)
	}
	t.Run("every record is stored once", func(t *testing.T) {
		// catches two writers reading the same head, where the later write is lost or torn
		records, err := Read(bytes.NewReader(body))
		if err != nil {
			t.Fatalf("Read of the ledger error = %v, want nil", err)
		}
		seen := map[string]int{}
		for _, rec := range records {
			seen[rec.(Check).Key]++
		}
		for i := range writers {
			if key := fmt.Sprintf("test:writer-%02d", i); seen[key] != 1 {
				t.Errorf("record %s stored %d times, want 1 (ledger holds %d records)", key, seen[key], len(records))
			}
		}
	})
	t.Run("every prev is the hash of the line before", func(t *testing.T) {
		// catches two records linked to the same head, which breaks the chain
		lines, prevs := recordLines(t, string(body))
		if len(lines) != writers {
			t.Fatalf("ledger holds %d record lines, want %d", len(lines), writers)
		}
		want := genesis
		for i, line := range lines {
			if prevs[i] != want {
				t.Errorf("record %d prev = %s, want %s", i, prevs[i], want)
			}
			next, err := hashLine([]byte(line))
			if err != nil {
				t.Fatalf("hash record %d: %v", i, err)
			}
			want = next
		}
	})
}
