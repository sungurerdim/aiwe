package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// The tool names one loop run may call, named once so the definitions a turn offers, the calls a
// model makes and the tests agree (SPEC §10.2, O14).
const (
	ToolRead  = "read"
	ToolWrite = "write"
	ToolEdit  = "edit"
	ToolList  = "list"
	ToolShell = "shell"
)

// maxToolReadBytes caps the file one read or edit loads: the content goes into every later turn of
// the run, so a larger file is refused, never cut (SPEC §10.2). SPEC names no value; 256 KiB holds a
// source file whole and stays far below the 4 MiB reply cap.
const maxToolReadBytes = 256 << 10

// The named refusals of a tool call the loop feeds back to the model (SPEC §5.21, §10.2, O2).
var (
	// ErrGitPath refuses a path into the git directory of the clone.
	ErrGitPath = errors.New("path_in_git_dir")
	// ErrPathLink refuses a path through a link the clone holds, which may point at the git
	// directory without naming it.
	ErrPathLink = errors.New("path_through_link")
	// ErrReadTooLarge refuses a tool result over maxToolReadBytes: a read of a larger file, and a
	// shell command whose output is larger (SPEC §10.2).
	ErrReadTooLarge = errors.New("read_too_large")
)

// Tools runs the tool calls of one run confined to its clone: the decisions live here and every
// body that touches the clone lives in loop_io.go, so a call reaches the clone through its
// file-system root only (SPEC §5.6, §10.2, O2). env is the environment a bound run hands the shell
// child; envSet reports that a run bound it, so an empty environment stays empty and the child
// never inherits the parent process (SPEC §6.5, DR-10).
type Tools struct {
	root   string
	perms  ToolPermissions
	env    []string
	envSet bool
}

// NewTools returns the tools of a run confined to root and offering only the tools perms allows
// (SPEC §10.2, O2).
func NewTools(root string, perms ToolPermissions) *Tools {
	return &Tools{root: root, perms: perms}
}

// bindRunEnv records the environment the shell tool of this run hands its child. The child gets a
// copy of env and nothing of the parent process, including when env is empty (SPEC §6.5, DR-10).
func (t *Tools) bindRunEnv(env []string) {
	t.envSet = true
	t.env = append([]string{}, env...)
}

// Do runs one tool call and returns its output. A tool the run does not allow, a path outside the
// clone and a call the tool cannot run are refused; a refusal is a result the loop feeds back to
// the model (SPEC §10.2, O2).
func (t *Tools) Do(ctx context.Context, name string, args map[string]any) (string, error) {
	if !t.perms.allows(name) {
		return "", fmt.Errorf("tools: the run does not allow %q", name)
	}
	switch name {
	case ToolRead:
		return t.readCall(args)
	case ToolWrite:
		return t.writeCall(args)
	case ToolEdit:
		return t.editCall(args)
	case ToolList:
		return t.listCall(args)
	case ToolShell:
		return t.shellCall(ctx, args)
	}
	return "", fmt.Errorf("tools: %q is not a tool of this loop", name)
}

// allows reports whether a run with these permissions offers name to the model (SPEC §10.2).
func (p ToolPermissions) allows(name string) bool {
	switch name {
	case ToolRead:
		return p.Read
	case ToolWrite:
		return p.Write
	case ToolEdit:
		return p.Edit
	case ToolList:
		return p.List
	case ToolShell:
		return p.Shell
	}
	return false
}

// readCall runs one read call (SPEC §10.2).
func (t *Tools) readCall(args map[string]any) (string, error) {
	path, err := callPath(args)
	if err != nil {
		return "", err
	}
	return t.read(path)
}

// writeCall runs one write call (SPEC §10.2).
func (t *Tools) writeCall(args map[string]any) (string, error) {
	path, err := callPath(args)
	if err != nil {
		return "", err
	}
	content, err := argText(args, "content")
	if err != nil {
		return "", err
	}
	return t.write(path, content)
}

// editCall runs one edit call (SPEC §10.2).
func (t *Tools) editCall(args map[string]any) (string, error) {
	path, err := callPath(args)
	if err != nil {
		return "", err
	}
	old, err := argText(args, "old")
	if err != nil {
		return "", err
	}
	text, err := argText(args, "new")
	if err != nil {
		return "", err
	}
	return t.edit(path, old, text)
}

// listCall runs one list call (SPEC §10.2).
func (t *Tools) listCall(args map[string]any) (string, error) {
	path, err := callPath(args)
	if err != nil {
		return "", err
	}
	return t.list(path)
}

// shellCall runs one shell call (SPEC §10.2).
func (t *Tools) shellCall(ctx context.Context, args map[string]any) (string, error) {
	command, err := argText(args, "command")
	if err != nil {
		return "", err
	}
	return t.shell(ctx, command)
}

// callPath returns the path of a tool call, confined to the clone (SPEC §10.2, O2).
func callPath(args map[string]any) (string, error) {
	raw, err := argText(args, "path")
	if err != nil {
		return "", err
	}
	return confinedPath(raw)
}

// confinedPath returns a path of the clone: an empty path, an absolute one, one that walks above
// the clone and one into its git directory are refused, and the check runs before any read or
// write (SPEC §5.21, §10.2, O2).
func confinedPath(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("tools: the call names no path")
	}
	if filepath.IsAbs(raw) || filepath.VolumeName(raw) != "" {
		return "", fmt.Errorf("tools: the path %q is outside the clone", raw)
	}
	path := filepath.Clean(raw)
	if path == ".." || strings.HasPrefix(path, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("tools: the path %q leaves the clone", raw)
	}
	for _, part := range strings.FieldsFunc(path, isPathSeparator) {
		if isGitName(part) {
			return "", fmt.Errorf("%w: the path %q reaches the git directory of the clone", ErrGitPath, raw)
		}
	}
	return path, nil
}

// isPathSeparator reports a separator of either OS, so a backslash never hides a component on Unix
// (SPEC §10.2).
func isPathSeparator(r rune) bool {
	return r == '/' || r == '\\'
}

// isGitName reports a path component one OS resolves to the git directory: .git in any case, with
// the trailing dots and spaces Windows drops, before an NTFS stream name, or its 8.3 short name
// GIT~N. A filter or hook written there runs on the engine's next git call (SPEC §5.21, O2).
func isGitName(part string) bool {
	name := strings.ToLower(part)
	if i := strings.IndexByte(name, ':'); i >= 0 {
		name = name[:i]
	}
	name = strings.TrimRight(name, ". ")
	if name == ".git" {
		return true
	}
	digits, short := strings.CutPrefix(name, "git~")
	return short && digits != "" && strings.Trim(digits, "0123456789") == ""
}

// toolArgs reads the arguments of one tool call: the model sends them as a JSON object in a string
// (SPEC §10.2).
func toolArgs(raw string) (map[string]any, error) {
	var args map[string]any
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return nil, fmt.Errorf("tools: the arguments are not a JSON object: %w", err)
	}
	return args, nil
}

// argText returns one text argument of a tool call; an argument the call does not name is refused
// (SPEC §10.2).
func argText(args map[string]any, name string) (string, error) {
	value, ok := args[name].(string)
	if !ok {
		return "", fmt.Errorf("tools: the call names no %s", name)
	}
	return value, nil
}

// toolDefinitions returns the definition of every tool the run allows, in a fixed order, so the
// tool part of the stable prefix never changes within a run and the provider's prompt cache hits
// (SPEC §10.2).
func toolDefinitions(perms ToolPermissions) []chatTool {
	offered := []struct {
		name   string
		params []string
	}{
		{ToolRead, []string{"path"}},
		{ToolWrite, []string{"path", "content"}},
		{ToolEdit, []string{"path", "old", "new"}},
		{ToolList, []string{"path"}},
		{ToolShell, []string{"command"}},
	}
	defs := make([]chatTool, 0, len(offered))
	for _, tool := range offered {
		if perms.allows(tool.name) {
			defs = append(defs, functionDefinition(tool.name, tool.params...))
		}
	}
	return defs
}

// functionDefinition returns one function tool that takes the named required string parameters in
// order, in the OpenAI wire format (SPEC §10.2).
func functionDefinition(name string, params ...string) chatTool {
	properties := make(map[string]any, len(params))
	required := make([]string, 0, len(params))
	for _, param := range params {
		properties[param] = map[string]any{"type": "string"}
		required = append(required, param)
	}
	return chatTool{
		Type: "function",
		Function: chatFunction{
			Name: name,
			Parameters: map[string]any{
				"type":       "object",
				"properties": properties,
				"required":   required,
			},
		},
	}
}
