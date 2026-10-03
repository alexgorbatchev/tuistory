package app

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/remorses/tuistory/internal/session"
)

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	for _, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '.' || c == '/' || c == ':' || c == '-') {
			return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
		}
	}
	return s
}

func yamlKey(key string) string {
	return fmt.Sprintf("%s%s", ansi("36", key), ansi("90", ":"))
}

func yamlString(value string) string {
	quote := ansi("90", "\"")
	escaped := strings.ReplaceAll(strings.ReplaceAll(value, "\\", "\\\\"), "\"", "\\\"")
	return fmt.Sprintf("%s%s%s", quote, ansi("32", escaped), quote)
}

func yamlNumber(value int) string {
	return ansi("35", strconv.Itoa(value))
}

func ansi(code, val string) string {
	return fmt.Sprintf("\x1b[%sm%s\x1b[39m", code, val)
}

func timeAgo(ts int64) string {
	secs := int((time.Now().UnixMilli() - ts) / 1000)
	if secs < 60 {
		return fmt.Sprintf("%ds ago", secs)
	}
	mins := secs / 60
	if mins < 60 {
		return fmt.Sprintf("%dm ago", mins)
	}
	hrs := mins / 60
	if hrs < 24 {
		return fmt.Sprintf("%dh ago", hrs)
	}
	days := hrs / 24
	return fmt.Sprintf("%dd ago", days)
}

func boolPtr(b bool) *bool {
	return &b
}

func (c *commandContext) printSessionDiagnostic(s *session.Session, name, action string) {
	fmt.Fprintf(c.stdout, "Session %q %s command:%q cwd:%q cols:%d rows:%d", name, action, s.Command(), s.Cwd(), s.Cols(), s.Rows())
}

func (c *commandContext) warnSilentSession(name, action string, timeout int) {
	if c.agent {
		fmt.Fprintf(c.stderr, "Session %q %s, but produced no output within %dms; process running; use --no-wait for silent startup.", name, action, timeout)
		return
	}
	fmt.Fprintf(c.stderr, "Session %q %s, but produced no output within %dms.\nThe process is still running in the background.\nIf the command is expected to be silent at startup, pass --no-wait to skip this check.\n", name, action, timeout)
}
