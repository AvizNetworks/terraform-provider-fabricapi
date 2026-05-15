package provider

import (
	"os"
	"strings"
)

// asyncEnabled gates respond-async (HTTP Prefer: respond-async) in the provider.
//
// By default async is enabled: when the API returns an operation id, the provider polls
// GET /operations/{id} until completion (including when webhooks are enabled).
//
// Set FABRICAPI_DISABLE_ASYNC=1 (or true/yes) to treat async as unsupported at plan/apply
// time (emergency rollback without rebuilding).
func asyncEnabled() bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv("FABRICAPI_DISABLE_ASYNC")))
	switch v {
	case "1", "true", "yes", "y", "on":
		return false
	default:
		return true
	}
}
