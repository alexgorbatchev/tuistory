package session

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const runtimeHelperEnv = "TUISTORY_RUNTIME_HELPER"

func TestSessionRuntimeHelper(t *testing.T) {
	if os.Getenv(runtimeHelperEnv) != "argv" {
		return
	}
	fmt.Printf("ARGV=%q\n", os.Args)
	fmt.Printf("PATH=%s\n", os.Getenv("PATH"))
	os.Exit(0)
}

func TestLaunchResolvesRequestedPATH(t *testing.T) {
	const command = "tuistory-runtime-path-helper"
	cases := []struct {
		name string
		path string
	}{
		{name: "absolute"},
		{name: "relative", path: "bin"},
		{name: "empty component", path: ":missing"},
		{name: "dot", path: "."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cwd := t.TempDir()
			dir := cwd
			path := tc.path
			if tc.name == "absolute" || tc.name == "relative" {
				dir = filepath.Join(cwd, "bin")
				if err := os.Mkdir(dir, 0755); err != nil {
					t.Fatal(err)
				}
				if tc.name == "absolute" {
					path = dir
				}
			}
			if err := os.Symlink(os.Args[0], filepath.Join(dir, command)); err != nil {
				t.Fatal(err)
			}
			args := []string{"-test.run=^TestSessionRuntimeHelper$", "--", "with spaces", "$literal", "a;b"}
			s := launchTestSession(t, LaunchOptions{
				Command: command, Args: args, Cwd: cwd,
				Env: map[string]string{"PATH": path, runtimeHelperEnv: "argv"},
			})
			if !s.WaitForExit(3 * time.Second) {
				t.Fatal("helper did not exit")
			}
			select {
			case <-s.readDone:
			case <-time.After(time.Second):
				t.Fatal("helper output did not finish")
			}
			wantArgs := append([]string{command}, args...)
			want := fmt.Sprintf("ARGV=%q", wantArgs)
			if got := s.ReadAll(); !strings.Contains(got, want) || !strings.Contains(got, "PATH="+path) {
				t.Fatalf("literal arguments/environment not preserved: %q, want %q and PATH=%s", got, want, path)
			}
			if !reflect.DeepEqual(s.cmd.Args, wantArgs) {
				t.Fatalf("command argv = %q, want %q", s.cmd.Args, wantArgs)
			}
		})
	}
}

func TestLaunchPATHDoesNotFallBackToDaemonPATH(t *testing.T) {
	for _, path := range []string{"", t.TempDir()} {
		if s, err := New(LaunchOptions{Command: "echo", Env: map[string]string{"PATH": path}}); err == nil {
			s.Close("test")
			t.Fatalf("echo resolved using daemon PATH when requested PATH=%q", path)
		}
	}
}

func TestLaunchExplicitPathUsesRequestedCwd(t *testing.T) {
	cwd := t.TempDir()
	const command = "./runtime-helper"
	if err := os.Symlink(os.Args[0], filepath.Join(cwd, command)); err != nil {
		t.Fatal(err)
	}
	s := launchTestSession(t, LaunchOptions{
		Command: command, Args: []string{"-test.run=^TestSessionRuntimeHelper$"}, Cwd: cwd,
		Env: map[string]string{"PATH": "", runtimeHelperEnv: "argv"},
	})
	if _, err := s.WaitForText(fmt.Sprintf("ARGV=[%q", command), 3*time.Second); err != nil {
		t.Fatal(err)
	}
}
