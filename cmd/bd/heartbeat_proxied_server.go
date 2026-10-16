package main

import (
	"context"
)

// Compatibility entry point retained for existing in-package callers.
func runHeartbeatProxiedServer(ctx context.Context, id string) error {
	return runHeartbeatProxied(ctx, id)
}
