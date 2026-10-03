package keys

import (
	"fmt"
	"slices"
	"strings"
)

var (
	letters = []string{
		"a", "b", "c", "d", "e", "f", "g", "h", "i", "j",
		"k", "l", "m", "n", "o", "p", "q", "r", "s", "t",
		"u", "v", "w", "x", "y", "z",
	}

	digits = []string{"0", "1", "2", "3", "4", "5", "6", "7", "8", "9"}

	specialKeys = []string{
		"enter", "return", "esc", "escape", "tab", "space", "backspace", "delete",
		"insert", "up", "down", "left", "right", "home", "end", "pageup", "pagedown",
		"clear", "linefeed", "f1", "f2", "f3", "f4", "f5", "f6", "f7", "f8", "f9",
		"f10", "f11", "f12",
	}

	modifiers = []string{"ctrl", "alt", "shift", "meta"}

	punctuation = []string{
		"-", "=", "[", "]", "\\", ";", "'", ",", ".", "/", "`",
		"!", "@", "#", "$", "%", "^", "&", "*", "(", ")", "_", "+",
		"{", "}", "|", ":", "\"", "<", ">", "?", "~",
	}

	validKeysMap = func() map[string]struct{} {
		m := make(map[string]struct{})
		for _, list := range [][]string{letters, digits, specialKeys, modifiers, punctuation} {
			for _, k := range list {
				m[k] = struct{}{}
			}
		}
		return m
	}()

	csiUKeyCodes = map[string]int{
		"enter":     13,
		"return":    13,
		"tab":       9,
		"backspace": 127,
		"escape":    27,
		"esc":       27,
	}

	keyCodes = map[string]string{
		"enter":     "\r",
		"return":    "\r",
		"esc":       "\x1b",
		"escape":    "\x1b",
		"tab":       "\t",
		"space":     " ",
		"backspace": "\x7f",
		"delete":    "\x1b[3~",
		"insert":    "\x1b[2~",
		"up":        "\x1b[A",
		"down":      "\x1b[B",
		"left":      "\x1b[D",
		"right":     "\x1b[C",
		"home":      "\x1b[H",
		"end":       "\x1b[F",
		"pageup":    "\x1b[5~",
		"pagedown":  "\x1b[6~",
		"clear":     "\x1b[E",
		"linefeed":  "\n",
		"f1":        "\x1bOP",
		"f2":        "\x1bOQ",
		"f3":        "\x1bOR",
		"f4":        "\x1bOS",
		"f5":        "\x1b[15~",
		"f6":        "\x1b[17~",
		"f7":        "\x1b[18~",
		"f8":        "\x1b[19~",
		"f9":        "\x1b[20~",
		"f10":       "\x1b[21~",
		"f11":       "\x1b[23~",
		"f12":       "\x1b[24~",
	}

	ctrlCodes = map[string]string{
		"a": "\x01", "b": "\x02", "c": "\x03", "d": "\x04", "e": "\x05", "f": "\x06",
		"g": "\x07", "h": "\x08", "i": "\x09", "j": "\x0a", "k": "\x0b", "l": "\x0c",
		"m": "\x0d", "n": "\x0e", "o": "\x0f", "p": "\x10", "q": "\x11", "r": "\x12",
		"s": "\x13", "t": "\x14", "u": "\x15", "v": "\x16", "w": "\x17", "x": "\x18",
		"y": "\x19", "z": "\x1a",
	}
)

// IsValidKey checks whether a string is a recognized key name.
func IsValidKey(key string) bool {
	_, ok := validKeysMap[strings.ToLower(key)]
	return ok
}

// ValidKeysList returns a sorted list of all valid key names.
func ValidKeysList() []string {
	keys := make([]string, 0, len(validKeysMap))
	for k := range validKeysMap {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// GetKeyCode converts a slice of key tokens into escape sequences.
func GetKeyCode(keyArray []string) string {
	var (
		hasCtrl  bool
		hasAlt   bool
		hasShift bool
		mainKeys []string
	)

	for _, rawKey := range keyArray {
		k := strings.ToLower(rawKey)
		switch k {
		case "ctrl":
			hasCtrl = true
		case "alt":
			hasAlt = true
		case "shift":
			hasShift = true
		case "meta":
			// Meta is accepted without extra sequence modulation
		default:
			mainKeys = append(mainKeys, k)
		}
	}

	if len(mainKeys) == 0 {
		return ""
	}

	var sb strings.Builder
	for _, key := range mainKeys {
		var code string

		if hasCtrl && len(key) == 1 {
			if c, ok := ctrlCodes[key]; ok {
				code = c
			} else {
				code = key
			}
		} else if (hasCtrl || hasAlt || hasShift) && csiUKeyCodes[key] != 0 {
			// CSI u encoding: \x1b[keycode;modifiersu
			// Modifier = 1 + (shift ? 1 : 0) + (alt ? 2 : 0) + (ctrl ? 4 : 0)
			keycode := csiUKeyCodes[key]
			modifier := 1
			if hasShift {
				modifier += 1
			}
			if hasAlt {
				modifier += 2
			}
			if hasCtrl {
				modifier += 4
			}
			code = fmt.Sprintf("\x1b[%d;%du", keycode, modifier)
		} else if c, ok := keyCodes[key]; ok {
			code = c
			if hasAlt {
				code = "\x1b" + code
			}
		} else if len(key) == 1 {
			if hasShift {
				code = strings.ToUpper(key)
			} else {
				code = key
			}
			if hasAlt {
				code = "\x1b" + code
			}
		} else {
			code = key
		}

		sb.WriteString(code)
	}

	return sb.String()
}
