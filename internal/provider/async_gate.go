package provider

// asyncEnabled gates respond-async behavior for release safety.
//
// For the current release, async is intentionally disabled unconditionally.
func asyncEnabled() bool {
	return false
}

