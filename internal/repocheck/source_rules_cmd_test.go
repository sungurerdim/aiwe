package repocheck_test

import (
	"testing"

	"github.com/sungurerdim/aiwe/internal/repocheck"
)

// TestSourceRulesRepocheckMain catches an io_import reported on the repocheck entry point, which
// SPEC §5.6 lets import os, and an os import in any other command entry point that passes.
func TestSourceRulesRepocheckMain(t *testing.T) {
	cfg := repocheck.SourceRulesConfig{Product: "zentra", ProductFile: "internal/core/product.go"}
	body := []string{"package main", `import "os"`, "func main() { os.Exit(0) }"}
	tests := []struct {
		name    string
		path    string
		wantIO  bool
		wantMsg string
	}{
		{"the repocheck entry point may import os", "internal/cmd/repocheck/main.go", false,
			"io_import on the file §5.6 lets import os"},
		{"another command entry point may not import os", "internal/cmd/other/main.go", true,
			"no io_import on a main.go outside the §5.6 list"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			found := repocheck.SourceRules(cfg, []repocheck.SourceFile{sourceRulesFile(tc.path, body...)})
			gotIO := false
			for _, f := range found {
				if f.Rule == "io_import" {
					gotIO = true
					if f.Path != tc.path || f.Line != 2 {
						t.Errorf("io_import at %s:%d, want %s:2", f.Path, f.Line, tc.path)
					}
				}
			}
			if gotIO != tc.wantIO {
				t.Fatalf("%s; findings %+v", tc.wantMsg, found)
			}
		})
	}
}
