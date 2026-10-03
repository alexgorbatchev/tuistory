package keys

import "testing"

func TestIsValidKey(t *testing.T) {
	tests := []struct {
		name string
		key  string
		want bool
	}{
		{"enter", "enter", true},
		{"ctrl", "ctrl", true},
		{"letter a", "a", true},
		{"upper letter A", "A", true},
		{"digit 1", "1", true},
		{"punctuation minus", "-", true},
		{"f1", "f1", true},
		{"f12", "f12", true},
		{"invalid key", "invalidkey", false},
		{"empty string", "", false},
		{"unknown word", "foo", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsValidKey(tt.key); got != tt.want {
				t.Fatalf("IsValidKey(%q) = %v, want %v", tt.key, got, tt.want)
			}
		})
	}
}

func TestGetKeyCode(t *testing.T) {
	tests := []struct {
		name string
		keys []string
		want string
	}{
		{"single enter", []string{"enter"}, "\r"},
		{"single return", []string{"return"}, "\r"},
		{"single tab", []string{"tab"}, "\t"},
		{"single escape", []string{"esc"}, "\x1b"},
		{"single space", []string{"space"}, " "},
		{"single backspace", []string{"backspace"}, "\x7f"},
		{"arrow up", []string{"up"}, "\x1b[A"},
		{"ctrl c", []string{"ctrl", "c"}, "\x03"},
		{"ctrl a", []string{"ctrl", "a"}, "\x01"},
		{"ctrl z", []string{"ctrl", "z"}, "\x1a"},
		{"alt f", []string{"alt", "f"}, "\x1bf"},
		{"shift a", []string{"shift", "a"}, "A"},
		{"ctrl+shift enter (csi u)", []string{"ctrl", "shift", "enter"}, "\x1b[13;6u"},
		{"ctrl enter (csi u)", []string{"ctrl", "enter"}, "\x1b[13;5u"},
		{"only modifier returns empty", []string{"ctrl"}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetKeyCode(tt.keys)
			if got != tt.want {
				t.Fatalf("GetKeyCode(%v) = %q, want %q", tt.keys, got, tt.want)
			}
		})
	}
}
