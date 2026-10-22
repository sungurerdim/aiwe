package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

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

// unlinked walks every component of path inside root and refuses one that is a link or neither a
// directory nor a regular file, so a link the clone holds never leads a tool to the git directory
// or out of the clone. It returns what the last component is, or nil when the path does not exist
// yet (SPEC §5.21, §10.2, O2).
func unlinked(root *os.Root, path string) (fs.FileInfo, error) {
	var walked string
	var info fs.FileInfo
	for _, part := range strings.FieldsFunc(path, isPathSeparator) {
		if part == "." {
			continue
		}
		walked = filepath.Join(walked, part)
		next, err := root.Lstat(walked)
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("tools: %s: %w", walked, err)
		}
		if next.Mode()&fs.ModeSymlink != 0 || (!next.IsDir() && !next.Mode().IsRegular()) {
			return nil, fmt.Errorf("%w: %s is a link or a special file", ErrPathLink, walked)
		}
		info = next
	}
	return info, nil
}

// read returns the content of one regular file of the clone; a file over maxToolReadBytes is
// refused, never cut (SPEC §10.2).
func (t *Tools) read(path string) (string, error) {
	root, err := t.open()
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }()
	info, err := unlinked(root, path)
	if err != nil {
		return "", fmt.Errorf("read: %s: %w", path, err)
	}
	if info != nil && info.Size() > maxToolReadBytes {
		return "", fmt.Errorf("read: %w: %s holds %d bytes, over the cap of %d", ErrReadTooLarge, path,
			info.Size(), maxToolReadBytes)
	}
	data, err := core.ReadRegularCapped(root, path, maxToolReadBytes)
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
	if _, err := unlinked(root, path); err != nil {
		return "", fmt.Errorf("write: %s: %w", path, err)
	}
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
	if _, err := unlinked(root, path); err != nil {
		return "", fmt.Errorf("list: %s: %w", path, err)
	}
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
// (SPEC §5.20). A run hands the child only the environment the request carried, so the parent's
// repository pointers and profile never reach a command of the clone (SPEC §6.5, DR-10). Output
// over the read cap is refused, never cut, and the refusal names the cap; under the cap the output
// is the text of the result, so the child's own line ends read the same on every OS (SPEC §10.2,
// DR-3). What a command of the clone may reach besides that is the sandbox level of the clone, the
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
	out := &cappedOutput{limit: maxToolReadBytes}
	spec := core.ChildSpec{Argv: argv, Dir: dir, Env: t.shellEnv(), Stdout: out, Stderr: out}
	child, err := core.Start(ctx, spec)
	if err != nil {
		return "", fmt.Errorf("shell: %w", err)
	}
	waitErr := child.Wait()
	if out.overCap() {
		return "", fmt.Errorf("shell: %w: the output is over the cap of %d bytes", ErrReadTooLarge, maxToolReadBytes)
	}
	text := toolText(out.String())
	if waitErr != nil {
		return text, fmt.Errorf("shell: %s: %w", argv[0], waitErr)
	}
	return text, nil
}

// shellEnv returns the environment of one shell child. A run that bound an environment gets only
// that copy, including when it is empty. A tools value no run bound gets an empty environment
// too: a nil environment would inherit the parent process, and the parent's repository pointers
// and profile must never reach a command (SPEC §6.5, DR-10).
func (t *Tools) shellEnv() []string {
	if t.envSet {
		return t.env
	}
	return []string{}
}

// cappedOutput stores at most limit+1 bytes of a command's output. Anything past that is discarded
// and over is set, so the shell tool refuses the output whole instead of returning a cut prefix
// (SPEC §10.2). Stdout and stderr share one writer and the process launcher copies them in two
// goroutines, so the lock keeps the two apart.
type cappedOutput struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	limit int
	over  bool
}

// Write stores p up to one byte past the limit, which is enough to know the output went over. It
// reports a full write even where it discards bytes, so the command is not stopped by a short write
// and its exit status stays its own.
func (b *cappedOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.over {
		return len(p), nil
	}
	room := b.limit + 1 - b.buf.Len()
	if room < 0 {
		room = 0
	}
	if room > len(p) {
		room = len(p)
	}
	if room > 0 {
		_, _ = b.buf.Write(p[:room])
	}
	if b.buf.Len() > b.limit {
		b.over = true
	}
	return len(p), nil
}

// overCap reports whether the command wrote more than the limit.
func (b *cappedOutput) overCap() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.over
}

// String returns the bytes stored under the limit; it is read once the child has ended.
func (b *cappedOutput) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// toolText returns a command's output as the text of a tool result: a line the child ended with
// "\r\n" reads as one ended with "\n". A Windows child ends every line that way, so folding it
// keeps the result of one command the same whatever OS ran it (DR-3, SPEC §10.2). The cap is
// decided on the child's own bytes, so folding never hides output; no consumer of a tool result
// needs the carriage return.
func toolText(out string) string {
	return strings.ReplaceAll(out, "\r\n", "\n")
}
