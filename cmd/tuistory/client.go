package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/remorses/tuistory/internal/agent"
	"github.com/remorses/tuistory/internal/app"
	"github.com/remorses/tuistory/internal/client"
	"github.com/remorses/tuistory/internal/relay"
	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

type commandExitError struct{ code int }

func (e commandExitError) Error() string { return fmt.Sprintf("command exited with code %d", e.code) }

func runClient(port int) {
	cmd := newClientCommand(port, os.Args)
	cmd.SetArgs(os.Args[1:])
	if err := cmd.Execute(); err != nil {
		var exitErr commandExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.code)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newClientCommand(port int, argv []string) *cobra.Command {
	root := newRootCommand()
	forward := func(cmd *cobra.Command, args []string) error {
		isLaunch := cmd == root || cmd.Name() == "launch"
		if cmd == root && len(args) == 0 {
			return cmd.Help()
		}
		if isLaunch && (os.Getenv("TRAFORO_URL") != "" || os.Getenv("SIGILLO") != "" || os.Getenv("TUISTORY_SESSION") != "") {
			return runPassthrough(cmd, args)
		}
		if err := client.EnsureRelayRunning(port, version); err != nil {
			printRelayLogTail()
			return err
		}
		name, _ := cmd.Flags().GetString("session")
		if cmd.Name() == "attach" {
			return app.RunAttach(port, name)
		}
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("reading working directory: %w", err)
		}
		res, err := client.ForwardCLI(port, relay.CLIRequest{Argv: argv, Cwd: cwd, Env: envToMap(os.Environ())})
		if err != nil {
			printRelayLogTail()
			return err
		}
		if res.Stderr != "" {
			fmt.Fprintln(cmd.ErrOrStderr(), res.Stderr)
		}
		if res.ExitCode != 0 {
			if res.Stdout != "" {
				fmt.Fprintln(cmd.OutOrStdout(), res.Stdout)
			}
			return commandExitError{res.ExitCode}
		}
		background, _ := cmd.Flags().GetBool("background")
		if isLaunch && term.IsTerminal(int(os.Stdout.Fd())) && !agent.IsAgentMode() && !background {
			if name == "" {
				targetCwd, _ := cmd.Flags().GetString("cwd")
				if targetCwd == "" {
					targetCwd = cwd
				} else if !filepath.IsAbs(targetCwd) {
					targetCwd = filepath.Join(cwd, targetCwd)
				}
				_, _, label := app.LaunchCommand(cmd, args)
				name = client.GetDefaultSessionName(label, targetCwd)
			}
			return app.RunAttach(port, name)
		}
		if res.Stdout != "" {
			_, err = fmt.Fprintln(cmd.OutOrStdout(), res.Stdout)
		}
		return err
	}
	root.RunE = forward
	for _, cmd := range root.Commands() {
		if cmd.Name() == "skill" {
			continue
		}
		if cmd.Name() == "daemon-stop" {
			cmd.RunE = func(cmd *cobra.Command, args []string) error {
				if client.ProbeRelay(port).Kind == client.StatusNoListener {
					_, err := fmt.Fprintln(cmd.OutOrStdout(), "No daemon running")
					return err
				}
				freed, err := client.KillRelay(port)
				if err != nil {
					return err
				}
				if !freed {
					return fmt.Errorf("Failed to stop daemon: port %d is still in use", port)
				}
				_, err = fmt.Fprintln(cmd.OutOrStdout(), "Daemon stopped")
				return err
			}
		} else {
			cmd.RunE = forward
		}
	}
	return root
}

func runPassthrough(cmd *cobra.Command, args []string) error {
	program, argv, label := app.LaunchCommand(cmd, args)
	if label == "" {
		return fmt.Errorf("missing command")
	}
	child := exec.Command(program, argv...)
	child.Stdin, child.Stdout, child.Stderr = cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr()
	cwd, err := cmd.Flags().GetString("cwd")
	if err != nil {
		return err
	}
	child.Dir = cwd
	env, err := cmd.Flags().GetStringArray("env")
	if err != nil {
		return err
	}
	child.Env = append(os.Environ(), env...)
	nested := os.Getenv("TUISTORY_SESSION") != ""
	if !nested {
		child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	if err := child.Start(); err != nil {
		return fmt.Errorf("spawning command: %w", err)
	}
	signals := make(chan os.Signal, 1)
	done := make(chan struct{})
	finished := make(chan struct{})
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		defer close(finished)
		for {
			select {
			case <-done:
				return
			case sig := <-signals:
				if nested {
					_ = child.Process.Signal(sig)
				} else {
					_ = unix.Kill(-child.Process.Pid, sig.(syscall.Signal))
				} // Child may already have exited.
			}
		}
	}()
	err = child.Wait()
	signal.Stop(signals)
	close(done)
	<-finished
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return commandExitError{exitErr.ExitCode()}
		}
		return fmt.Errorf("waiting for command: %w", err)
	}
	return nil
}
