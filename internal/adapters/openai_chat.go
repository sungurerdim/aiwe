package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/sungurerdim/aiwe/internal/core"
)

// maxReplyBytes caps the reply one turn reads, so a cut or endless body is refused fail-closed
// (DR-6).
const maxReplyBytes = 4 << 20

// PriceRow is one pinned row of the price table, in US dollars per million tokens (SPEC §8.1).
type PriceRow struct {
	InputPerM       float64
	CachedInputPerM float64
	OutputPerM      float64
}

// PriceTable prices a model's tokens; a model without a row has no price, never a measured 0
// (DR-6).
type PriceTable map[string]PriceRow

// Cost returns the price of usage on model under the table: fresh input, cached input and
// completion tokens, each at its per-million price. An unpriced model, or any unknown count, is
// unknown, never 0 (DR-6).
func (t PriceTable) Cost(model string, usage Usage) core.Measured[float64] {
	row, priced := t[model]
	if !priced {
		return core.Measured[float64]{}
	}
	if !usage.Prompt.Known || !usage.Cached.Known || !usage.Completion.Known {
		return core.Measured[float64]{}
	}
	fresh := usage.Prompt.Value - usage.Cached.Value
	if fresh < 0 {
		fresh = 0
	}
	usd := (float64(fresh)*row.InputPerM + float64(usage.Cached.Value)*row.CachedInputPerM +
		float64(usage.Completion.Value)*row.OutputPerM) / 1e6
	return core.Measured[float64]{Value: usd, Known: true}
}

// ChatConfig wires one OpenAI-compatible chat client: the environment it reads its key from, the
// endpoint, the session header, the price table, the user-agent version and the HTTP client (SPEC
// §10.2, DR-19).
type ChatConfig struct {
	Env           Env
	Endpoint      string
	SessionHeader string
	KeyEnv        string
	Version       string
	Prices        PriceTable
	Log           *slog.Logger
	HTTP          *http.Client
}

// ChatClient sends one chat turn to an OpenAI-compatible endpoint (SPEC §10.2).
type ChatClient struct {
	cfg ChatConfig
}

// NewChatClient returns the client of cfg; an empty price table takes the pinned defaults.
func NewChatClient(cfg ChatConfig) *ChatClient {
	if cfg.Prices == nil {
		cfg.Prices = DefaultPrices()
	}
	return &ChatClient{cfg: cfg}
}

// Turn sends one chat turn and returns the served model, usage and cost. The key is read by name at
// run time and is set only on the Authorization header (SPEC §10.2, DR-19).
func (c *ChatClient) Turn(ctx context.Context, req RunRequest) (core.Result[RunReport], error) {
	key, err := Key(c.cfg.Env, c.cfg.KeyEnv)
	if err != nil {
		return core.Result[RunReport]{}, err
	}
	body, err := json.Marshal(buildChatBody(req))
	if err != nil {
		return core.Result[RunReport]{}, fmt.Errorf("chat turn: encode the request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		return core.Result[RunReport]{}, fmt.Errorf("chat turn: build the request: %w", err)
	}
	c.setHeaders(httpReq, key, req.RunID)
	resp, err := c.client().Do(httpReq)
	if err != nil {
		return core.Result[RunReport]{}, fmt.Errorf("chat turn: %w", err)
	}
	if resp == nil || resp.Body == nil {
		return core.Result[RunReport]{}, errors.New("chat turn: no reply body")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return core.Result[RunReport]{}, fmt.Errorf("chat turn: the endpoint answered %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxReplyBytes))
	if err != nil {
		return core.Result[RunReport]{}, fmt.Errorf("chat turn: read the reply: %w", err)
	}
	report, err := c.decode(data)
	if err != nil {
		return core.Result[RunReport]{}, err
	}
	return core.Result[RunReport]{Status: core.StatusOK, Outcome: core.OutcomeOK, Data: report}, nil
}

// setHeaders writes the required turn headers: Bearer auth, the session header with the run id and
// the product user agent (SPEC §10.2).
func (c *ChatClient) setHeaders(httpReq *http.Request, key Secret, runID string) {
	httpReq.Header.Set("Authorization", "Bearer "+string(key))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", userAgent(c.cfg.Version))
	if c.cfg.SessionHeader != "" {
		httpReq.Header.Set(c.cfg.SessionHeader, runID)
	}
}

// client returns the HTTP client of the run, or the default one.
func (c *ChatClient) client() *http.Client {
	if c.cfg.HTTP != nil {
		return c.cfg.HTTP
	}
	return http.DefaultClient
}

// decode reads the served model and usage of one reply and prices the served model; an unreadable
// reply, or one that names no model, is an error (SPEC §10.2, DR-6).
func (c *ChatClient) decode(data []byte) (RunReport, error) {
	var reply chatReply
	if err := json.Unmarshal(data, &reply); err != nil {
		return RunReport{}, fmt.Errorf("chat turn: read the reply: %w", err)
	}
	if reply.Model == "" {
		return RunReport{}, errors.New("chat turn: the reply names no model")
	}
	usage := reply.Usage.usage()
	return RunReport{Served: reply.Model, Usage: usage, Cost: c.cfg.Prices.Cost(reply.Model, usage)}, nil
}

// userAgent names the product and, when known, its version on every provider request (SPEC §5.13).
func userAgent(version string) string {
	if version == "" {
		return core.Product
	}
	return core.Product + "/" + version
}

// chatMessage is one message of the OpenAI wire format.
type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatFunction is one function tool of the OpenAI wire format.
type chatFunction struct {
	Name       string         `json:"name"`
	Parameters map[string]any `json:"parameters"`
}

// chatTool is one tool of the OpenAI wire format.
type chatTool struct {
	Type     string       `json:"type"`
	Function chatFunction `json:"function"`
}

// requestBody is the body of one chat turn.
type requestBody struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Tools    []chatTool    `json:"tools"`
}

// buildChatBody builds the body of one turn: the stable role prefix first, then the task, then the
// tools the run allows (SPEC §10.2).
func buildChatBody(req RunRequest) requestBody {
	return requestBody{
		Model: req.Model,
		Messages: []chatMessage{
			{Role: "system", Content: "role: " + req.Role},
			{Role: "user", Content: req.Task},
		},
		Tools: tools(req.Tools),
	}
}

// tools returns the tool definitions of the permissions a run allows, in a stable order.
func tools(perms ToolPermissions) []chatTool {
	var out []chatTool
	if perms.Read {
		out = append(out, functionTool("read", "path"))
	}
	if perms.Edit {
		out = append(out, functionTool("edit", "path"))
	}
	if perms.Shell {
		out = append(out, functionTool("shell", "command"))
	}
	return out
}

// functionTool returns one function tool that takes a single required string parameter.
func functionTool(name, param string) chatTool {
	return chatTool{
		Type: "function",
		Function: chatFunction{
			Name: name,
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{param: map[string]any{"type": "string"}},
				"required":   []string{param},
			},
		},
	}
}

// tokenDetails is the nested detail object of the usage wire format (SPEC §10.2).
type tokenDetails struct {
	CachedTokens    *int64 `json:"cached_tokens"`
	ReasoningTokens *int64 `json:"reasoning_tokens"`
}

// usageWire is the usage object a reply carries; a count it omits stays unknown (DR-6).
type usageWire struct {
	PromptTokens            *int64        `json:"prompt_tokens"`
	PromptTokensDetails     *tokenDetails `json:"prompt_tokens_details"`
	CompletionTokens        *int64        `json:"completion_tokens"`
	CompletionTokensDetails *tokenDetails `json:"completion_tokens_details"`
}

// usage maps the wire usage to the run vocabulary; a count the reply never named is unknown.
func (w usageWire) usage() Usage {
	out := Usage{Prompt: measuredInt(w.PromptTokens), Completion: measuredInt(w.CompletionTokens)}
	if w.PromptTokensDetails != nil {
		out.Cached = measuredInt(w.PromptTokensDetails.CachedTokens)
	}
	if w.CompletionTokensDetails != nil {
		out.Reasoning = measuredInt(w.CompletionTokensDetails.ReasoningTokens)
	}
	return out
}

// chatReply is the part of one reply a turn records: the served model and its usage (SPEC §10.2).
type chatReply struct {
	Model string    `json:"model"`
	Usage usageWire `json:"usage"`
}

// measuredInt returns a measured count, or an unknown one when no value was named (DR-6).
func measuredInt(value *int64) core.Measured[int64] {
	if value == nil {
		return core.Measured[int64]{}
	}
	return core.Measured[int64]{Value: *value, Known: true}
}
