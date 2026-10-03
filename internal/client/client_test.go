package client

import (
	"testing"
)

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		v1   string
		v2   string
		want int // -1 if v1 < v2, 0 if v1 == v2, 1 if v1 > v2
	}{
		{"0.11.0", "0.11.0", 0},
		{"0.11.1", "0.11.0", 1},
		{"0.10.9", "0.11.0", -1},
		{"1.0.0", "0.11.0", 1},
		{"0.5.0", "0.6.0", -1},
		{"0.11.0", "0.11", 0},
	}

	for _, tt := range tests {
		t.Run(tt.v1+"_vs_"+tt.v2, func(t *testing.T) {
			got := CompareVersions(tt.v1, tt.v2)
			if (tt.want < 0 && got >= 0) || (tt.want > 0 && got <= 0) || (tt.want == 0 && got != 0) {
				t.Fatalf("CompareVersions(%q, %q) = %d, want sign %d", tt.v1, tt.v2, got, tt.want)
			}
		})
	}
}

func TestGetDefaultSessionName(t *testing.T) {
	name := GetDefaultSessionName("echo hello", "/home/user/project")
	if !stringsHasPrefix(name, "project-") {
		t.Fatalf("expected name to start with project-, got %q", name)
	}
	if !stringsHasSuffix(name, "-echo-hello") {
		t.Fatalf("expected name to end with -echo-hello, got %q", name)
	}
}

func stringsHasPrefix(s, p string) bool {
	return len(s) >= len(p) && s[:len(p)] == p
}

func stringsHasSuffix(s, p string) bool {
	return len(s) >= len(p) && s[len(s)-len(p):] == p
}
