package session

import (
	"fmt"
	"regexp"
	"strings"
)

// ParsePattern compiles literal text or /expression/ with g, i, m, and s flags.
func ParsePattern(pattern string) (*regexp.Regexp, error) {
	lastSlash := strings.LastIndex(pattern, "/")
	if !strings.HasPrefix(pattern, "/") || lastSlash <= 0 {
		return regexp.Compile(regexp.QuoteMeta(pattern))
	}
	body, flags := pattern[1:lastSlash], pattern[lastSlash+1:]
	var modifiers strings.Builder
	seen := make(map[rune]bool)
	for _, flag := range flags {
		if seen[flag] {
			return nil, fmt.Errorf("duplicate regex flag %q in %q", flag, pattern)
		}
		seen[flag] = true
		switch flag {
		case 'i', 'm', 's':
			modifiers.WriteRune(flag)
		case 'g':
			// Wait starts each search fresh, and click already finds every match.
		default:
			return nil, fmt.Errorf("unsupported regex flag %q in %q; supported flags: g, i, m, s", flag, pattern)
		}
	}
	if modifiers.Len() > 0 {
		body = "(?" + modifiers.String() + ")" + body
	}
	re, err := regexp.Compile(body)
	if err != nil {
		return nil, fmt.Errorf("invalid regex %q: %w", pattern, err)
	}
	return re, nil
}
