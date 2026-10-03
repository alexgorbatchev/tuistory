package main

import (
	"bytes"
	"strings"
	"testing"
)

func executeCommand(args ...string) (string, error) {
	buf := new(bytes.Buffer)
	cmd := newRootCommand()
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(args)

	err := cmd.Execute()
	return buf.String(), err
}

func TestRootCommand_Help(t *testing.T) {
	t.Setenv("AGENT", "0")
	out, err := executeCommand("--help")
	if err != nil {
		t.Fatalf("help failed: %v", err)
	}
	if !strings.Contains(out, "launch [command]") {
		t.Errorf("expected launch command in help, got %q", out)
	}
	if !strings.Contains(out, "snapshot") {
		t.Errorf("expected snapshot in help, got %q", out)
	}
	if !strings.Contains(out, "read") {
		t.Errorf("expected read in help, got %q", out)
	}
	if !strings.Contains(out, "skill") {
		t.Errorf("expected skill in help, got %q", out)
	}
}

func TestRootCommand_Version(t *testing.T) {
	outVer, errVer := executeCommand("--version")
	if errVer != nil {
		t.Fatalf("version failed: %v", errVer)
	}
	if outVer != version+"\n" {
		t.Errorf("expected '%s\\n', got %q", version, outVer)
	}
}

func TestSkillCommand(t *testing.T) {
	out, err := executeCommand("skill")
	if err != nil {
		t.Fatalf("skill command failed: %v", err)
	}
	if !strings.Contains(out, "name: tuistory") {
		t.Errorf("expected frontmatter in skill, got %q", out)
	}
	if !strings.Contains(out, "Command Reference") {
		t.Errorf("expected Command Reference in skill, got %q", out)
	}
}

func TestAgentModeHelpAlert(t *testing.T) {
	t.Setenv("AGENT", "1")
	out, err := executeCommand("--help")
	if err != nil {
		t.Fatalf("help failed: %v", err)
	}
	if !strings.Contains(out, "ALERT: Agents must read `AGENT=1 tuistory skill` before using this tool.") {
		t.Errorf("expected alert banner in agent mode, got %q", out)
	}
}
