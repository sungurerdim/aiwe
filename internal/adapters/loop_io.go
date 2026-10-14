package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"

	"github.com/sungurerdim/aiwe/internal/core"
)

// This file holds the process, HTTP and file IO of the agent loop: the turn it sends, the reply it
// reads and the tool bodies it runs inside the clone (SPEC §5.6, §10.2).

// finishLength is the finish reason of a reply the endpoint cut at its token limit. Such a reply is
// an attempt, never a finished run, so the loop refuses it instead of reading it as a final answer
// (SPEC §7.4, §10.2).
const finishLength = "length"

// loopRequest is the body of one turn: the model, the whole conversation, the tool definitions the
// run allows and the reasoning effort of the candidate, when it names one (SPEC §10.2, DR-15).
type loopRequest struct {
	Model           string        `json:"model"`
	Messages        []wireMessage `json:"messages"`
	Tools           []chatTool    `json:"tools"`
	ReasoningEffort string        `json:"reasoning_effort,omitempty"`
}

// wireMessage is one message of the OpenAI wire format: the stable prefix, the task, the assistant
// message of a reply and the result of a tool call, which names the call it answers (SPEC §10.2).
type wireMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	ToolCalls  []wireToolCall `json:"tool_calls,omitempty"`
}

// wireToolCall is one tool call of an assistant message; the model sends its arguments as a JSON
// object in a string (SPEC §10.2).
type wireToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// wireReply is what the loop reads of one reply: the served model, the usage of the turn and the
// assistant message with the tool calls it asks for (SPEC §10.2).
type wireReply struct {
	Model   string       `json:"model"`
	Choices []wireChoice `json:"choices"`
	Usage   usageWire    `json:"usage"`
}

// wireChoice is one choice of a reply: the assistant message with the tool calls it asks for and the
// finish reason, which tells a finished reply from one the endpoint cut (SPEC §10.2, §7.4).
type wireChoice struct {
	Message      wireMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

// turn sends the conversation of one run as one turn and returns the reply the model served. The
// key reaches only the Authorization header, so it never enters a log line or an error (SPEC §10.2,
// DR-19).
func (l *Loop) turn(ctx context.Context, run *loopRun) (wireReply, error) {
	body, err := json.Marshal(loopRequest{
		Model: run.req.Model, Messages: run.messages, Tools: run.defs, ReasoningEffort: run.req.Effort,
	})
	if err != nil {
		return wireReply{}, fmt.Errorf("loop: encode the turn: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, l.chat.cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		return wireReply{}, fmt.Errorf("loop: build the turn: %w", err)
	}
	l.chat.setHeaders(httpReq, run.key, run.req.RunID)
	resp, err := l.chat.client().Do(httpReq)
	if err != nil {
		return wireReply{}, fmt.Errorf("loop: %w", err)
	}
	if resp == nil || resp.Body == nil {
		return wireReply{}, errors.New("loop: the endpoint sent no reply body")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return wireReply{}, fmt.Errorf("loop: the endpoint answered %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxReplyBytes))
	if err != nil {
		return wireReply{}, fmt.Errorf("loop: read the reply: %w", err)
	}
	return decodeTurn(data)
}

// decodeTurn reads the reply of one turn; a reply that is not JSON, names no model, holds no choice,
// carries neither an answer nor a tool call, or was cut at the endpoint's token limit is an error,
// so a cut turn is never read as a finished one (SPEC §10.2, §7.4, DR-6).
func decodeTurn(data []byte) (wireReply, error) {
	var reply wireReply
	if err := json.Unmarshal(data, &reply); err != nil {
		return wireReply{}, fmt.Errorf("loop: read the reply: %w", err)
	}
	if reply.Model == "" {
		return wireReply{}, errors.New("loop: the reply names no model")
	}
	if len(reply.Choices) == 0 {
		return wireReply{}, errors.New("loop: the reply holds no answer")
	}
	choice := reply.Choices[0]
	if choice.FinishReason == finishLength {
		return wireReply{}, errors.New("loop: the endpoint cut the reply at its token limit")
	}
	if choice.Message.Content == "" && len(choice.Message.ToolCalls) == 0 {
		return wireReply{}, errors.New("loop: the reply holds neither an answer nor a tool call")
	}
	return reply, nil
}

// dir returns the clone the tools of a run stay inside; a run without one runs no tool at all
// (SPEC §10.2, O2).
func (t *Tools) dir() (string, error) {
	if t.root == "" {
		return "", errors.New("tools: the run names no clone")
	}
	return t.root, nil
}

// open returns the file-system root of the clone. Every read and write of a tool goes through it,
// so a path that leaves the clone, an absolute one and a link out of it are all refused by the
// kernel-level check (SPEC §5.6, §10.2, O2).
func (t *Tools) open() (*os.Root, error) {
	dir, err := t.dir()
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("tools: open the clone %s: %w", dir, err)
	}
	return root, nil
}

// read returns the content of one file of the clone (SPEC §10.2).
func (t *Tools) read(path string) (string, error) {
	root, err := t.open()
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }()
	file, err := root.Open(path)
	if err != nil {
		return "", fmt.Errorf("read: %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(file)
	if err != nil {
		return "", fmt.Errorf("read: %s: %w", path, err)
	}
	return string(data), nil
}

// write writes one file of the clone and returns what the file holds now, the text the call wrote;
// a file it makes is owner-only (SPEC §10.2, DR-11).
func (t *Tools) write(path, content string) (string, error) {
	root, err := t.open()
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }()
	file, err := root.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return "", fmt.Errorf("write: %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()
	if _, err := file.WriteString(content); err != nil {
		return "", fmt.Errorf("write: %s: %w", path, err)
	}
	return content, nil
}

// edit replaces the one occurrence of text in a file of the clone; text that occurs more than once
// or not at all is refused, so an edit is never ambiguous (SPEC §10.2).
func (t *Tools) edit(path, old, text string) (string, error) {
	if old == "" {
		return "", errors.New("edit: the call names no text to replace")
	}
	content, err := t.read(path)
	if err != nil {
		return "", err
	}
	if count := strings.Count(content, old); count != 1 {
		return "", fmt.Errorf("edit: %s holds that text %d times, want exactly one", path, count)
	}
	return t.write(path, strings.Replace(content, old, text, 1))
}

// list returns the entries of one directory of the clone, one name per line and a directory with a
// trailing slash; the names are sorted, so two listings of one directory read the same lines
// (SPEC §10.2).
func (t *Tools) list(path string) (string, error) {
	root, err := t.open()
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }()
	dir, err := root.Open(path)
	if err != nil {
		return "", fmt.Errorf("list: %s: %w", path, err)
	}
	defer func() { _ = dir.Close() }()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return "", fmt.Errorf("list: %s: %w", path, err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			name += "/"
		}
		names = append(names, name)
	}
	slices.Sort(names)
	return strings.Join(names, "\n"), nil
}

// shell runs one command with the clone as its working directory and returns its combined output.
// The command goes through no shell, so a metacharacter is an argument, never a second command
// (SPEC §5.20), and it runs in the run environment — the parent environment minus its secret-shaped
// variables (SPEC §6.5), so the provider key and the tracker token never reach a command of the
// clone. What a command of the clone may reach besides that is the sandbox level of the clone, the
// one open risk of the skeleton (SPEC §6.7, §10.2, O2).
func (t *Tools) shell(ctx context.Context, command string) (string, error) {
	dir, err := t.dir()
	if err != nil {
		return "", err
	}
	argv := strings.Fields(command)
	if len(argv) == 0 {
		return "", errors.New("shell: the call names no command")
	}
	out := &bytes.Buffer{}
	spec := core.ChildSpec{Argv: argv, Dir: dir, Env: core.CleanEnv(core.Environ(), nil), Stdout: out, Stderr: out}
	child, err := core.Start(ctx, spec)
	if err != nil {
		return "", fmt.Errorf("shell: %w", err)
	}
	if err := child.Wait(); err != nil {
		return out.String(), fmt.Errorf("shell: %s: %w", argv[0], err)
	}
	return out.String(), nil
}
