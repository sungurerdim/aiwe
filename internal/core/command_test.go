package core_test

import (
	"errors"
	"testing"

	"github.com/sungurerdim/aiwe/internal/core"
)

func TestCommand(t *testing.T) {
	dict := []core.ParamDef{
		{Name: "repo", Meaning: "path of the target repository; every relative path resolves against it"},
		{Name: "unit", Meaning: "number of the unit issue"},
		{Name: "format", Meaning: "output format of the result envelope: text or json"},
	}
	unitRun := core.Command{Module: "engine", Object: "unit", Verb: "run", Params: []string{"repo", "unit"}}
	gatesGate := core.Command{Module: "gates", Object: "gates", Verb: "gate", Params: []string{"repo", "format"}}

	tests := []struct {
		name    string
		dict    []core.ParamDef
		cmds    []core.Command
		wantErr error
	}{
		{
			name: "shared parameter refused although it has one meaning in the dictionary",
			dict: dict,
			cmds: []core.Command{unitRun, gatesGate},
		},
		{
			name: "parameter name defined twice with two meanings",
			dict: append(append([]core.ParamDef{}, dict...),
				core.ParamDef{Name: "repo", Meaning: "owner/name slug of the GitHub repository"}),
			cmds:    []core.Command{unitRun},
			wantErr: core.ErrParamDuplicate,
		},
		{
			name: "parameter name defined twice with the same meaning",
			dict: append(append([]core.ParamDef{}, dict...),
				core.ParamDef{Name: "unit", Meaning: "number of the unit issue"}),
			cmds:    []core.Command{unitRun},
			wantErr: core.ErrParamDuplicate,
		},
		{
			name: "command uses a parameter the dictionary does not define",
			dict: dict,
			cmds: []core.Command{unitRun,
				{Module: "engine", Object: "runs", Verb: "stop", Params: []string{"run"}}},
			wantErr: core.ErrParamUndeclared,
		},
		{
			name:    "dictionary entry without a meaning lets each command read it its own way",
			dict:    []core.ParamDef{{Name: "repo", Meaning: ""}},
			cmds:    []core.Command{{Module: "engine", Object: "unit", Verb: "run", Params: []string{"repo"}}},
			wantErr: core.ErrParamUndeclared,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := core.CheckParams(tc.dict, tc.cmds)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("CheckParams() error = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("CheckParams() error = %v, want errors.Is %v", err, tc.wantErr)
			}
		})
	}

	t.Run("production dictionary defines one name twice or a name without meaning", func(t *testing.T) {
		prod := core.Dictionary()
		if len(prod) == 0 {
			t.Fatalf("Dictionary() is empty")
		}
		seen := map[string]bool{}
		for _, p := range prod {
			if p.Name == "" || p.Meaning == "" {
				t.Errorf("Dictionary() entry %+v has an empty name or meaning", p)
			}
			if seen[p.Name] {
				t.Errorf("Dictionary() defines %q twice", p.Name)
			}
			seen[p.Name] = true
		}
		if !seen["format"] {
			t.Errorf("Dictionary() misses %q, which selects the result envelope format", "format")
		}
		if err := core.CheckParams(prod, nil); err != nil {
			t.Errorf("CheckParams(Dictionary(), nil) error = %v, want nil", err)
		}
	})
}
