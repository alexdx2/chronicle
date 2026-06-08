package store

import (
	"os"
	"strconv"
)

// ClaimTTLMinutes returns how long an obligation claim stays locked before expiry.
// Override with CHRONICLE_CLAIM_TTL_MINUTES (default 5 for agent-friendly scans).
func ClaimTTLMinutes() int {
	if v := os.Getenv("CHRONICLE_CLAIM_TTL_MINUTES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 5
}
