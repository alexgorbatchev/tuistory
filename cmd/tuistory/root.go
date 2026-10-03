package main

import (
	"bytes"
	"os"
	"strings"

	"github.com/remorses/tuistory/internal/app"
	"github.com/remorses/tuistory/internal/relay"
	"github.com/spf13/cobra"
)

var version = "0.0.1"

func newRootCommand() *cobra.Command {
	reg := relay.NewSessionRegistry()
	cwd, _ := os.Getwd()
	var stdout, stderr bytes.Buffer

	cmd := app.NewCommand(reg, cwd, envToMap(os.Environ()), &stdout, &stderr)
	cmd.SetOut(os.Stdout)
	cmd.SetErr(os.Stderr)
	cmd.Version = version
	cmd.SetVersionTemplate("{{.Version}}\n")
	cmd.AddCommand(newSkillCommand())

	return cmd
}

func envToMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, e := range env {
		if idx := strings.Index(e, "="); idx > 0 {
			m[e[:idx]] = e[idx+1:]
		}
	}
	return m
}
