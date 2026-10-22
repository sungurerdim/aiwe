package adapters

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/sungurerdim/aiwe/internal/core"
)

// deepSeekKeyVar is the variable a DeepSeek model API reads its key from, by name at run time
// (SPEC §10.2, DR-19).
const deepSeekKeyVar = "DEEPSEEK_API_KEY"

// newDeepSeekFixture returns the DeepSeek provider of a test: it talks to the fake host of f, reads
// its key from env at run time and carries the account mode and the session decision (O14).
func newDeepSeekFixture(f *fakeHost, env Env, log *slog.Logger, mode DeepSeekMode, session OffPeakSession) *DeepSeek {
	cfg := DeepSeekConfig{
		Env:     env,
		Base:    f.srv.URL,
		Version: chatVersion,
		Mode:    mode,
		Session: session,
		Log:     log,
		HTTP:    f.srv.Client(),
	}
	return NewDeepSeek(cfg)
}

// TestDeepSeekModeDefaultsToOffPeakOnly catches a new DeepSeek account that runs always by default:
// an account without a mode setting is off-peak only, and "always" is an explicit choice (owner,
// 2026-10-12, O14).
func TestDeepSeekModeDefaultsToOffPeakOnly(t *testing.T) {
	log, _ := testLog(t)
	if got := DefaultDeepSeekMode(); got != DeepSeekModeOffPeak {
		t.Errorf("DefaultDeepSeekMode() = %q, want %q (owner, 2026-10-12)", got, DeepSeekModeOffPeak)
	}
	plain := NewDeepSeek(DeepSeekConfig{Log: log})
	if got := plain.Mode(); got != DeepSeekModeOffPeak {
		t.Errorf("a new account without a mode setting has mode %q, want %q (owner, 2026-10-12)", got, DeepSeekModeOffPeak)
	}
	always := NewDeepSeek(DeepSeekConfig{Log: log, Mode: DeepSeekModeAlways})
	if got := always.Mode(); got != DeepSeekModeAlways {
		t.Errorf("an account set to %q has mode %q, want the explicit choice; always is never the default", DeepSeekModeAlways, got)
	}
}

// TestDeepSeekSkipsOffPeakOnlyOutsideWindow catches an off-peak-only account that still runs outside
// the window, a skip recorded without its reason, or routing that stops at the first account: outside
// the window routing skips an off-peak-only account with a snake_case reason and takes the next
// account of the reserve list, while an always account and an off-peak session are never skipped
// (O3, O14).
func TestDeepSeekSkipsOffPeakOnlyOutsideWindow(t *testing.T) {
	log, _ := testLog(t)
	w := DeepSeekOffPeakWindow()
	inside := StartOffPeakSession(w, time.Date(2026, 10, 12, 20, 0, 0, 0, time.UTC))
	outside := StartOffPeakSession(w, time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC))
	accounts := func(modes ...DeepSeekMode) []*DeepSeek {
		out := make([]*DeepSeek, 0, len(modes))
		for _, mode := range modes {
			out = append(out, NewDeepSeek(DeepSeekConfig{Log: log, Mode: mode}))
		}
		return out
	}
	tests := []struct {
		name    string
		modes   []DeepSeekMode
		session OffPeakSession
		want    int
		skips   int
		catch   string
	}{
		{"an off-peak-only account is skipped outside the window for the next account", []DeepSeekMode{DeepSeekModeOffPeak, DeepSeekModeAlways}, outside, 1, 1, "outside the window the off-peak-only first account must be skipped and the always next account used"},
		{"an account without a mode setting is off-peak only and skipped outside the window", []DeepSeekMode{"", DeepSeekModeAlways}, outside, 1, 1, "a new account without a mode setting is off-peak only (owner, 2026-10-12)"},
		{"an off-peak session runs the off-peak-only account", []DeepSeekMode{DeepSeekModeOffPeak, DeepSeekModeAlways}, inside, 0, 0, "inside the window the off-peak-only account must run"},
		{"an always account runs outside the window", []DeepSeekMode{DeepSeekModeAlways, DeepSeekModeOffPeak}, outside, 0, 0, "an always account is never skipped"},
		{"a reserve list of off-peak-only accounts is all skipped outside the window", []DeepSeekMode{DeepSeekModeOffPeak, DeepSeekModeOffPeak}, outside, -1, 2, "with no runnable account routing reports every skip and chooses none"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, skips := ChooseDeepSeek(accounts(tc.modes...), tc.session)
			if got != tc.want {
				t.Errorf("ChooseDeepSeek chose account %d, want %d; %s (O3, O14)", got, tc.want, tc.catch)
			}
			if len(skips) != tc.skips {
				t.Fatalf("ChooseDeepSeek recorded %d skips, want %d; %s (O3, O14)", len(skips), tc.skips, tc.catch)
			}
			for i, skip := range skips {
				if skip.Index != i {
					t.Errorf("skip %d names account %d, want the skipped accounts in order; %s (O3)", i, skip.Index, tc.catch)
				}
				if !outcomeName.MatchString(skip.Reason) || !strings.Contains(skip.Reason, "off_peak") {
					t.Errorf("skip %d records reason %q, want a snake_case reason naming the off-peak window (O14)", i, skip.Reason)
				}
			}
		})
	}
}

// TestDeepSeekRunPricesTheWindowTheSessionDecided catches a run priced from the wrong window: a run
// in a session that decided off-peak uses the off-peak price of the served model, a run on an always
// account outside the window uses the standard price, and each cost equals the priced row, not the
// other window's (SPEC §8.1, O14).
func TestDeepSeekRunPricesTheWindowTheSessionDecided(t *testing.T) {
	log, _ := testLog(t)
	t.Setenv(deepSeekKeyVar, envSecret)
	w := DeepSeekOffPeakWindow()
	inside := StartOffPeakSession(w, time.Date(2026, 10, 12, 20, 0, 0, 0, time.UTC))
	outside := StartOffPeakSession(w, time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC))
	standard, offPeak := DeepSeekPrices()
	usage := usageOf(400, 800, 300, 120)
	standardCost := standard.Cost(DeepSeekDefaultModel, usage)
	offPeakCost := offPeak.Cost(DeepSeekDefaultModel, usage)
	if !standardCost.Known || !offPeakCost.Known {
		t.Fatalf("DeepSeekPrices() has no row for %s, so no run can be priced (SPEC §8.1)", DeepSeekDefaultModel)
	}
	if offPeakCost.Value >= standardCost.Value {
		t.Fatalf("the off-peak price %v is not below the standard price %v, so the two windows cannot be told apart (SPEC §8.1)", offPeakCost.Value, standardCost.Value)
	}
	tests := []struct {
		name    string
		mode    DeepSeekMode
		session OffPeakSession
		want    core.Measured[float64]
		other   core.Measured[float64]
		catch   string
	}{
		{"an off-peak session prices the off-peak row", DeepSeekModeOffPeak, inside, offPeakCost, standardCost, "the run must use the off-peak price of the window the session decided"},
		{"an always account outside the window prices the standard row", DeepSeekModeAlways, outside, standardCost, offPeakCost, "an always account outside the window must use the standard price"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeHost(t)
			p := newDeepSeekFixture(f, ProcessEnv(), log, tc.mode, tc.session)
			f.serveURL(t, p.Endpoints().Chat, answerJSON(replyBody(DeepSeekDefaultModel, usageFull)))
			res, err := p.Run(context.Background(), fixtureRequest(""))
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			checkMeasured(t, "cost", res.Data.Cost, tc.want, tc.catch)
			if res.Data.Cost.Value == tc.other.Value {
				t.Errorf("the run's cost %v equals the other window's price, so the session decision did not pick the price; %s (O14)", res.Data.Cost.Value, tc.catch)
			}
		})
	}
}

// TestDeepSeekPinnedDefaults catches a client pointed at another host, path or model: the base URL,
// the chat path and the default model are the pinned ones (SPEC §10.2, O14).
func TestDeepSeekPinnedDefaults(t *testing.T) {
	log, _ := testLog(t)
	p := NewDeepSeek(DeepSeekConfig{Env: ProcessEnv(), Log: log})
	if got := p.Endpoints().Chat; got != "https://api.deepseek.com/chat/completions" {
		t.Errorf("Endpoints().Chat = %q, want the pinned DeepSeek chat endpoint (SPEC §10.2)", got)
	}
	if DeepSeekDefaultModel != "deepseek-chat" {
		t.Errorf("DeepSeekDefaultModel = %q, want the pinned model (SPEC §10.2)", DeepSeekDefaultModel)
	}
}

// TestDeepSeekProviderContract runs the port contract against the DeepSeek client, the same suite the
// fake passes, so both share one request and result vocabulary (SPEC §5.13, DR-19).
func TestDeepSeekProviderContract(t *testing.T) {
	log, lines := testLog(t)
	t.Setenv(deepSeekKeyVar, envSecret)
	f := newFakeHost(t)
	build := func(env Env, logger *slog.Logger) Provider {
		p := NewDeepSeek(DeepSeekConfig{Env: env, Base: f.srv.URL, Version: chatVersion, Mode: DeepSeekModeAlways, Log: logger, HTTP: f.srv.Client()})
		f.serveURL(t, p.Endpoints().Chat, answerJSON(replyBody(DeepSeekDefaultModel, usageFull)))
		return p
	}
	p := NewDeepSeek(DeepSeekConfig{Env: ProcessEnv(), Base: f.srv.URL, Version: chatVersion, Mode: DeepSeekModeAlways, Log: log, HTTP: f.srv.Client()})
	f.serveURL(t, p.Endpoints().Chat, answerJSON(replyBody(DeepSeekDefaultModel, usageFull)))
	tools := ToolPermissions{Read: true, Edit: true}
	providerContract(t, contractFixture{
		New:    build,
		Key:    envSecret,
		Env:    fixtureEnv{deepSeekKeyVar + "=" + envSecret},
		Absent: fixtureEnv{"HOME=/tmp"},
		Log:    log,
		Lines:  lines,
		Run:    RunRequest{RunID: chatRunID, Role: chatRole, Task: chatTask, Tools: tools},
	})
}

// TestAllHoldsDeepSeek catches a compiled DeepSeek provider missing from the one list: All() holds a
// provider with the DeepSeek id, so an off-peak account is reachable (SPEC §5.1, O14).
func TestAllHoldsDeepSeek(t *testing.T) {
	log, _ := testLog(t)
	found := false
	for _, p := range All(log) {
		if p.ID() == "deepseek" {
			found = true
		}
	}
	if !found {
		t.Error("All() holds no DeepSeek provider, so the off-peak account never runs (SPEC §5.1, O14)")
	}
}
