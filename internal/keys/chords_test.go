package keys

import (
	"slices"
	"testing"
)

func TestModifierEncoding(t *testing.T) {
	for _, tt := range []struct {
		keys []string
		want string
	}{
		{[]string{"ctrl", "1"}, "1"},
		{[]string{"alt", "up"}, "\x1b\x1b[A"},
		{[]string{"alt", "shift", "tab"}, "\x1b[9;4u"},
		{[]string{"ctrl", "alt", "shift", "enter"}, "\x1b[13;8u"},
		{[]string{"a", "enter", "b"}, "a\rb"},
		{[]string{"meta", "a"}, "a"},
		{[]string{"unknown"}, "unknown"},
	} {
		if got := GetKeyCode(tt.keys); got != tt.want {
			t.Errorf("%v = %q, want %q", tt.keys, got, tt.want)
		}
	}
}

func TestKeyListOwnership(t *testing.T) {
	keys := ValidKeysList()
	if len(keys) == 0 || !slices.IsSorted(keys) {
		t.Fatal("key list empty or unsorted")
	}
	for _, key := range keys {
		if !IsValidKey(key) {
			t.Fatalf("listed key %q cannot be used", key)
		}
	}
	keys[0] = "invalid-key-name"
	if slices.Contains(ValidKeysList(), "invalid-key-name") {
		t.Fatal("returned list mutation affects registry")
	}
}
