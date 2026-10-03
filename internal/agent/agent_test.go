package agent

import (
	"testing"
)

func TestIsAgentMode(t *testing.T) {
	tests := []struct {
		name     string
		envVal   string
		setEnv   bool
		expected bool
	}{
		{"unset", "", false, false},
		{"empty", "", true, false},
		{"zero", "0", true, false},
		{"false", "false", true, false},
		{"no", "no", true, false},
		{"one", "1", true, true},
		{"true", "true", true, true},
		{"yes", "yes", true, true},
		{"TRUE uppercase", "TRUE", true, true},
		{"YES uppercase", "YES", true, true},
		{"with spaces", "  1  ", true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AI_AGENT", "")
			if tt.setEnv {
				t.Setenv("AGENT", tt.envVal)
			} else {
				t.Setenv("AGENT", "")
			}
			actual := IsAgentMode()
			if actual != tt.expected {
				t.Errorf("IsAgentMode() = %v, want %v (env=%q)", actual, tt.expected, tt.envVal)
			}
		})
	}
}

func TestAIAgentDetection(t *testing.T) {
	tests := []struct {
		name, agent, aiAgent string
		want                 bool
	}{
		{"empty", "", "", false},
		{"agent name", "", "codex", true},
		{"custom agent name", "", "my-agent", true},
		{"one", "", "1", true},
		{"zero is a name", "", "0", true},
		{"false is a name", "", "false", true},
		{"spaces remain nonempty", "", "  ", true},
		{"empty alias preserves AGENT", "true", "", true},
		{"alias with disabled AGENT", "0", "claude", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AGENT", tt.agent)
			t.Setenv("AI_AGENT", tt.aiAgent)
			if got := IsAgentMode(); got != tt.want {
				t.Fatalf("IsAgentMode()=%v, want %v", got, tt.want)
			}
		})
	}
}
