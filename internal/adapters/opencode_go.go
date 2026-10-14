package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/sungurerdim/aiwe/internal/core"
)

// The Go quota of OpenCode uses only the /zen/go/v1 path, never the credit path /zen/v1
// (SPEC §10.2, O14).
const (
	openCodeHost       = "https://opencode.ai"
	openCodeChatPath   = "/zen/go/v1/chat/completions"
	openCodeUsagePath  = "/zen/go/v1/usage"
	openCodeSessionKey = "x-opencode-session"
	openCodeKeyEnv     = "OPENCODE_API_KEY"
)

// DefaultModel is the model the skeleton runs on OpenCode Go (SPEC §10.2, O14).
const DefaultModel = "deepseek-v4.1-flash"

// maxQuotaBytes caps the usage answer one run reads, so an endless body is refused fail-closed.
const maxQuotaBytes = 1 << 20

// ErrRunIDMissing reports a run without the run id the session header needs (SPEC §10.2).
var ErrRunIDMissing = errors.New("run_id_missing")

// Endpoints are the URLs of one provider, so a test names no endpoint of its own (SPEC §10.2).
type Endpoints struct {
	Chat  string
	Usage string
}

// OpenCodeGoConfig wires the OpenCode Go client: the environment it reads OPENCODE_API_KEY from,
// the host, the user-agent version and the HTTP client (SPEC §10.2, DR-19).
type OpenCodeGoConfig struct {
	Env     Env
	Base    string
	Version string
	Prices  PriceTable
	Log     *slog.Logger
	HTTP    *http.Client
}

// OpenCodeGo is the OpenCode Go model API client: it reads the Go quota, then runs one chat turn
// (SPEC §10.2, O14).
type OpenCodeGo struct {
	cfg OpenCodeGoConfig
}

// NewOpenCodeGo returns the client of cfg; an empty host or price table takes the pinned defaults.
func NewOpenCodeGo(cfg OpenCodeGoConfig) *OpenCodeGo {
	if cfg.Base == "" {
		cfg.Base = openCodeHost
	}
	if cfg.Prices == nil {
		cfg.Prices = DefaultPrices()
	}
	return &OpenCodeGo{cfg: cfg}
}

// ID returns the stable id of the OpenCode Go adapter (SPEC §5.1).
func (p *OpenCodeGo) ID() string { return "opencode-go" }

// Capabilities reports one model API that serves the model the skeleton runs and names the model
// its reply reports (SPEC §10.2, O14).
func (p *OpenCodeGo) Capabilities() Capabilities {
	return Capabilities{Kind: KindModelAPI, Models: []string{DefaultModel}, ReportsModel: true}
}

// Endpoints returns the URLs of the client (SPEC §10.2).
func (p *OpenCodeGo) Endpoints() Endpoints {
	return Endpoints{Chat: p.cfg.Base + openCodeChatPath, Usage: p.cfg.Base + openCodeUsagePath}
}

// chatConfig wires the chat client of this provider, so one turn shares the one request vocabulary
// (SPEC §10.2).
func (p *OpenCodeGo) chatConfig() ChatConfig {
	return ChatConfig{
		Env: p.cfg.Env, Endpoint: p.Endpoints().Chat, SessionHeader: openCodeSessionKey,
		KeyEnv: openCodeKeyEnv, Version: p.cfg.Version, Prices: p.cfg.Prices, Log: p.cfg.Log, HTTP: p.cfg.HTTP,
	}
}

// Detect reads only the environment: the model API is present exactly when OPENCODE_API_KEY holds
// a value, and an absent one names a reason (SPEC §10.2, DR-6).
func (p *OpenCodeGo) Detect(_ context.Context, env Env) Detection {
	if _, err := Key(env, openCodeKeyEnv); err != nil {
		return Detection{Reason: "harness_missing"}
	}
	return Detection{Present: true, Version: p.cfg.Version}
}

// Run reads the Go quota, then runs one chat turn. The key is read by name at run time, the quota
// read carries it, and the report names the served model, usage, cost and quota (SPEC §10.2,
// DR-19).
func (p *OpenCodeGo) Run(ctx context.Context, req RunRequest) (core.Result[RunReport], error) {
	key, err := Key(p.cfg.Env, openCodeKeyEnv)
	if err != nil {
		return core.Result[RunReport]{}, err
	}
	if req.RunID == "" {
		return core.Result[RunReport]{}, ErrRunIDMissing
	}
	quota := p.readQuota(ctx, key)
	if req.Model == "" {
		req.Model = DefaultModel
	}
	res, err := NewChatClient(p.chatConfig()).Turn(ctx, req)
	if err != nil {
		return core.Result[RunReport]{}, err
	}
	res.Data.Quota = quota
	return res, nil
}

// readQuota reads the Go usage endpoint before a run; any unreadable answer leaves every window
// unknown, never a measured 0 (SPEC §10.2, DR-6).
func (p *OpenCodeGo) readQuota(ctx context.Context, key Secret) Quota {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.Endpoints().Usage, nil)
	if err != nil {
		return Quota{}
	}
	req.Header.Set("Authorization", "Bearer "+string(key))
	req.Header.Set("User-Agent", userAgent(p.cfg.Version))
	resp, err := p.client().Do(req)
	if err != nil {
		return Quota{}
	}
	if resp == nil || resp.Body == nil {
		return Quota{}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return Quota{}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxQuotaBytes))
	if err != nil {
		return Quota{}
	}
	return parseQuota(body)
}

// client returns the HTTP client of the run, or the default one.
func (p *OpenCodeGo) client() *http.Client {
	if p.cfg.HTTP != nil {
		return p.cfg.HTTP
	}
	return http.DefaultClient
}

// quotaWire is the undocumented usage answer: percent and reset time per window (SPEC §10.2).
type quotaWire struct {
	Rolling *windowWire `json:"rolling"`
	Weekly  *windowWire `json:"weekly"`
	Monthly *windowWire `json:"monthly"`
}

// windowWire is one window of the usage answer; an omitted field stays unknown (DR-6).
type windowWire struct {
	Percent  *float64 `json:"percent"`
	ResetsAt string   `json:"resets_at"`
}

// parseQuota reads the usage answer; an unparsable body is unknown (DR-6).
func parseQuota(body []byte) Quota {
	var wire quotaWire
	if err := json.Unmarshal(body, &wire); err != nil {
		return Quota{}
	}
	return Quota{Rolling: measuredWindow(wire.Rolling), Weekly: measuredWindow(wire.Weekly),
		Monthly: measuredWindow(wire.Monthly)}
}

// measuredWindow maps one wire window to a measured window; an omitted window is unknown.
func measuredWindow(w *windowWire) Window {
	if w == nil {
		return Window{}
	}
	return Window{Percent: measuredFloat(w.Percent), Reset: resetAt(w.ResetsAt)}
}

// measuredFloat returns a measured percent, or unknown when the answer named no value (DR-6).
func measuredFloat(value *float64) core.Measured[float64] {
	if value == nil {
		return core.Measured[float64]{}
	}
	return core.Measured[float64]{Value: *value, Known: true}
}

// resetAt parses the reset time of a window; an unparsable or empty value is unknown (DR-6).
func resetAt(value string) Reset {
	at, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return Reset{}
	}
	return Reset{At: at, Known: true}
}

// DefaultPrices returns the pinned price table of the models the skeleton runs; a model without a
// row has no price, never 0 (SPEC §8.1, DR-6).
func DefaultPrices() PriceTable {
	return PriceTable{
		DefaultModel: {InputPerM: 0.28, CachedInputPerM: 0.028, OutputPerM: 0.42},
	}
}
