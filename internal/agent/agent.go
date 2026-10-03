package agent

import (
	"os"
	"strings"
)

// IsAgentMode recognizes AGENT's boolean values or a nonempty AI_AGENT name,
// preserving the TypeScript implementation's std-env agent-name contract.
func IsAgentMode() bool {
	if os.Getenv("AI_AGENT") != "" {
		return true
	}
	v := strings.ToLower(strings.TrimSpace(os.Getenv("AGENT")))
	return v == "1" || v == "true" || v == "yes"
}
