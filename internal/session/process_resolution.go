package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// exec.LookPath reads the daemon's PATH. Search the requested environment and
// use LookPath on explicit candidates so Go checks executable permissions.
func resolveCommand(name, cwd string, env map[string]string) (string, error) {
	if strings.ContainsRune(name, filepath.Separator) {
		return name, nil
	}
	path, ok := env["PATH"]
	if !ok {
		path = os.Getenv("PATH")
	}
	for _, dir := range filepath.SplitList(path) {
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(cwd, dir)
		}
		candidate, err := filepath.Abs(filepath.Join(dir, name))
		if err != nil {
			return "", err
		}
		if resolved, err := exec.LookPath(candidate); err == nil {
			return resolved, nil
		}
	}
	return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
}
