package adapters

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/sungurerdim/aiwe/internal/core"
)

// The pinned DeepSeek model API: its host, its chat path and the variable it reads its key from
// (SPEC §10.2, DR-19, O14).
const (
	deepSeekHost     = "https://api.deepseek.com"
	deepSeekChatPath = "/chat/completions"
	deepSeekKeyEnv   = "DEEPSEEK_API_KEY"
)

// DeepSeekDefaultModel is the model the DeepSeek model API runs by default (SPEC §10.2, O14).
const DeepSeekDefaultModel = "deepseek-chat"

// DeepSeekMode is how an account uses DeepSeek's off-peak window: off-peak only, or always
// (owner, 2026-10-12, O14).
type DeepSeekMode string

// The two account modes of a DeepSeek account (O14).
const (
	DeepSeekModeOffPeak DeepSeekMode = "off_peak"
	DeepSeekModeAlways  DeepSeekMode = "always"
)

// DefaultDeepSeekMode returns the mode of a new DeepSeek account: off-peak only, so a user pays the
// lower price until they choose always explicitly (owner, 2026-10-12, O14).
func DefaultDeepSeekMode() DeepSeekMode { return DeepSeekModeOffPeak }

// Skip is one account routing passed over: its index in the reserve list and a snake_case reason
// (SPEC §5.3, O3, O14).
type Skip struct {
	Index  int
	Reason string
}

// ChooseDeepSeek returns the index of the first runnable account of the reserve list and the skips
// it passed over: an off-peak-only account is skipped outside the session's decided window and the
// next account is tried, an always account and an off-peak session are never skipped, and -1
// chooses none (O3, O14).
func ChooseDeepSeek(accounts []*DeepSeek, session OffPeakSession) (int, []Skip) {
	var skips []Skip
	for i, account := range accounts {
		if session.OffPeak() || account.Mode() == DeepSeekModeAlways {
			return i, skips
		}
		skips = append(skips, Skip{Index: i, Reason: "off_peak_window_closed"})
	}
	return -1, skips
}

// DeepSeekConfig wires the DeepSeek model API client: the environment it reads DEEPSEEK_API_KEY
// from, the host, the user-agent version, the account mode, the session's price-window decision and
// the HTTP client (SPEC §10.2, DR-19, O14).
type DeepSeekConfig struct {
	Env     Env
	Base    string
	Version string
	Mode    DeepSeekMode
	Session OffPeakSession
	Log     *slog.Logger
	HTTP    *http.Client
}

// DeepSeek is the DeepSeek model API client: it runs one chat turn priced by the window the session
// decided (SPEC §10.2, O14).
type DeepSeek struct {
	cfg DeepSeekConfig
}

// NewDeepSeek returns the client of cfg; an empty host takes the pinned one and an empty mode takes
// the off-peak-only default (O14).
func NewDeepSeek(cfg DeepSeekConfig) *DeepSeek {
	if cfg.Base == "" {
		cfg.Base = deepSeekHost
	}
	if cfg.Mode == "" {
		cfg.Mode = DefaultDeepSeekMode()
	}
	return &DeepSeek{cfg: cfg}
}

// ID returns the stable id of the DeepSeek adapter (SPEC §5.1).
func (p *DeepSeek) ID() string { return "deepseek" }

// Mode returns the account mode; an account without a setting is off-peak only (O14).
func (p *DeepSeek) Mode() DeepSeekMode { return p.cfg.Mode }

// Capabilities reports one model API that serves the pinned model and names the model its reply
// reports (SPEC §10.2, O14).
func (p *DeepSeek) Capabilities() Capabilities {
	return Capabilities{Kind: KindModelAPI, Models: []string{DeepSeekDefaultModel}, ReportsModel: true}
}

// Endpoints returns the URLs of the client (SPEC §10.2).
func (p *DeepSeek) Endpoints() Endpoints {
	return Endpoints{Chat: p.cfg.Base + deepSeekChatPath}
}

// Detect reads only the environment: the model API is present exactly when DEEPSEEK_API_KEY holds a
// value, and an absent one names a reason (SPEC §10.2, DR-6).
func (p *DeepSeek) Detect(_ context.Context, env Env) Detection {
	if _, err := Key(env, deepSeekKeyEnv); err != nil {
		return Detection{Reason: "key_missing"}
	}
	return Detection{Present: true, Version: p.cfg.Version}
}

// Run sends one chat turn and prices it by the window the session decided; the key is read by name
// at run time (SPEC §10.2, DR-19, O14).
func (p *DeepSeek) Run(ctx context.Context, req RunRequest) (core.Result[RunReport], error) {
	if req.Model == "" {
		req.Model = DeepSeekDefaultModel
	}
	return NewChatClient(p.chatConfig()).Turn(ctx, req)
}

// chatConfig wires the chat client of this provider, so one turn shares the one request vocabulary
// (SPEC §10.2).
func (p *DeepSeek) chatConfig() ChatConfig {
	return ChatConfig{
		Env: p.cfg.Env, Endpoint: p.Endpoints().Chat, KeyEnv: deepSeekKeyEnv,
		Version: p.cfg.Version, Prices: p.prices(), Log: p.cfg.Log, HTTP: p.cfg.HTTP,
	}
}

// prices returns the price table of the window the session decided: the off-peak row when the
// session started inside the window, the standard row otherwise (SPEC §8.1, O14).
func (p *DeepSeek) prices() PriceTable {
	standard, offPeak := DeepSeekPrices()
	if p.cfg.Session.OffPeak() {
		return offPeak
	}
	return standard
}

// DeepSeekPrices returns the pinned standard and off-peak price tables of DeepSeek's chat model, in
// US dollars per million tokens; the off-peak window's tokens cost less, so the two windows can be
// told apart (SPEC §8.1, O14).
func DeepSeekPrices() (standard, offPeak PriceTable) {
	standard = PriceTable{DeepSeekDefaultModel: {InputPerM: 0.56, CachedInputPerM: 0.056, OutputPerM: 1.68}}
	offPeak = PriceTable{DeepSeekDefaultModel: {InputPerM: 0.28, CachedInputPerM: 0.028, OutputPerM: 0.84}}
	return standard, offPeak
}
