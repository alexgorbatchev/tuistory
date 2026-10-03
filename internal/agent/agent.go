package agent

import (
	"os"
	"strings"
)

// IsAgentMode recognizes AGENT's boolean values or a nonempty AI_AGENT name,
// preserving the TypeScript implementation's std-env agent-name contract.
func IsAgentMode() bool {
	return IsAgentEnv(map[string]string{"AGENT": os.Getenv("AGENT"), "AI_AGENT": os.Getenv("AI_AGENT")})
}

// IsAgentEnv recognizes agent mode in the invoking client's environment.
func IsAgentEnv(env map[string]string) bool {
	if env["AI_AGENT"] != "" {
		return true
	}
	v := strings.ToLower(strings.TrimSpace(env["AGENT"]))
	return v == "1" || v == "true" || v == "yes"
}
