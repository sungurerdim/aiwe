package adapters

import (
	"context"
	"log/slog"
)

// All returns the compiled providers, one per adapter file; the list is the one copy the engine
// reads (SPEC §5.1, §10.2).
func All(log *slog.Logger) []Provider {
	return []Provider{
		NewOpenCodeGo(OpenCodeGoConfig{Env: ProcessEnv(), Log: log}),
		NewDeepSeek(DeepSeekConfig{Env: ProcessEnv(), Log: log}),
	}
}

// Available returns the providers Detect finds present in env, so no role runs without a credential
// (SPEC §5.1).
func Available(ctx context.Context, providers []Provider, env Env) []Provider {
	found := make([]Provider, 0, len(providers))
	for _, p := range providers {
		if p != nil && p.Detect(ctx, env).Present {
			found = append(found, p)
		}
	}
	return found
}
