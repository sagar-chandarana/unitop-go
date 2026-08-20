package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// blockingBin writes a fake NAME whose invocation records its own $$ as the
// DIRECT exec.Cmd child and blocks — no backgrounding, no nested
// pipe-holders — plus an optional canary that proves a refused launch never
// ran anything.
func blockingBin(t *testing.T, name, trigger string) (pid func() int, canary string) {
	t.Helper()
	bin := t.TempDir()
	pidFile := filepath.Join(bin, name+".pid")
	canary = filepath.Join(bin, name+".ran")
	script := fmt.Sprintf(`#!/bin/sh
touch %s
case "$*" in
*%s*) echo $$ > %s; exec sleep 300 ;;
esac
exit 0
`, canary, trigger, pidFile)
	if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() int {
		b, err := os.ReadFile(pidFile)
		if err != nil {
			return 0
		}
		f := strings.Fields(string(b))
		if len(f) == 0 {
			return 0
		}
		var n int
		fmt.Sscanf(f[0], "%d", &n)
		return n
	}, canary
}

func ownerModel(t *testing.T, host string) *model {
	t.Helper()
	mm := newModel(testRunner(t, host), "h", time.Second, sortCPU, false, false, false, "")
	m := &mm
	m.width, m.height, m.ready, m.connected = 140, 30, true, true
	m.units = testUnits()
	m.rebuild()
	return m
}

func waitFor(t *testing.T, what string, get func() int) int {
	t.Helper()
	for i := 0; i < 250; i++ {
		if p := get(); p != 0 {
			return p
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s never started", what)
	return 0
}

// A poll's systemctl — the direct child — is reaped before the quit
// keypress returns, locally and over the fake ssh alike.
func TestQuitReapsThePollChild(t *testing.T) {
	for _, host := range []string{"", "root@fake"} {
		var pid func() int
		if host == "" {
			pid, _ = blockingBin(t, "systemctl", "--version")
		} else {
			pid, _ = blockingBin(t, "ssh", "--version")
		}
		m := ownerModel(t, host)
		go m.pollCmd()() // blocks on the sleeping child until cancelled

		p := waitFor(t, "the poll child", pid)
		if _, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlC}); cmd == nil {
			t.Fatal("ctrl-c returned no command")
		}
		if err := syscall.Kill(p, 0); !errors.Is(err, syscall.ESRCH) {
			t.Errorf("host %q: the poll child (pid %d) outlived the quit: %v", host, p, err)
		}
	}
}

// An action must not keep mutating a unit after the screen is gone: the
// direct systemctl (and the ssh carrying a remote one) is reaped before
// shutdown returns.
func TestShutdownReapsTheActionChild(t *testing.T) {
	for _, host := range []string{"", "root@fake"} {
		var pid func() int
		if host == "" {
			pid, _ = blockingBin(t, "systemctl", "start")
		} else {
			pid, _ = blockingBin(t, "ssh", "start")
		}
		m := ownerModel(t, host)
		go m.runAction("nginx.service", unitActions[0])()

		p := waitFor(t, "the action child", pid)
		m.shutdown()
		if err := syscall.Kill(p, 0); !errors.Is(err, syscall.ESRCH) {
			t.Errorf("host %q: the action child (pid %d) outlived shutdown: %v", host, p, err)
		}
	}
}

// A Cmd constructed before shutdown but invoked after it launches NOTHING:
// the canary executable never runs, and the Cmd answers nil.
func TestLateCmdIsRefused(t *testing.T) {
	_, canary := blockingBin(t, "systemctl", "--version")
	m := ownerModel(t, "")
	poll := m.pollCmd()
	act := m.runAction("nginx.service", unitActions[0])
	m.shutdown()

	if msg := poll(); msg != nil {
		t.Errorf("a refused poll returned %v", msg)
	}
	if msg := act(); msg != nil {
		t.Errorf("a refused action returned %v", msg)
	}
	if _, err := os.Stat(canary); !errors.Is(err, os.ErrNotExist) {
		t.Error("a refused Cmd still launched the executable")
	}
}

// begin versus shutdown, raced hard under the race detector: the mutex
// protocol means Add never races Wait, whichever side wins.
func TestBeginShutdownRace(t *testing.T) {
	for i := 0; i < 200; i++ {
		w := newProgWork()
		done := make(chan struct{})
		go func() {
			defer close(done)
			for j := 0; j < 8; j++ {
				if _, ok := w.begin(); ok {
					w.done()
				}
			}
		}()
		w.shutdown()
		<-done
		w.shutdown() // and idempotently again
	}
}

// The mux socket directory outlives the drain: shutdown reaps the tracked
// remote work while the runner's directory still exists; only the explicit
// close that main runs afterwards removes it.
func TestMuxOutlivesTheDrain(t *testing.T) {
	pid, _ := blockingBin(t, "ssh", "--version")
	mm := newModel(newRunner("root@fake"), "h", time.Second, sortCPU, false, false, false, "")
	m := &mm
	m.width, m.height, m.ready, m.connected = 140, 30, true, true
	m.units = testUnits()
	m.rebuild()
	go m.pollCmd()()

	waitFor(t, "the remote poll child", pid)
	m.shutdown()
	if _, err := os.Stat(m.r.ctlDir); err != nil {
		t.Errorf("the mux directory vanished before the drain finished: %v", err)
	}
	m.r.close()
	if _, err := os.Stat(m.r.ctlDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("close did not remove the mux directory: %v", err)
	}
}

// The unconditional post-Run route, against a REAL Program: no renderer, no
// input, externally Killed — not the graceful in-model quit — and the
// runProgram helper main uses may not return until the poll child is
// already reaped.
func TestPostRunShutdownReapsThroughARealProgram(t *testing.T) {
	pid, _ := blockingBin(t, "systemctl", "--version")
	m := ownerModel(t, "")

	in, _ := io.Pipe() // never written, never EOF: input stays silent
	p := tea.NewProgram(m, tea.WithoutRenderer(), tea.WithInput(in), tea.WithoutSignalHandler())

	type result struct {
		err     error
		reapErr error
	}
	done := make(chan result, 1)
	go func() {
		err := runProgram(p, m)
		// Captured at the instant the helper returns: the contract is
		// reaped BEFORE return, not eventually.
		done <- result{err, syscall.Kill(waitPidOnce(pid), 0)}
	}()

	waitFor(t, "the program's poll child", pid)
	p.Kill()
	res := <-done
	if !errors.Is(res.reapErr, syscall.ESRCH) {
		t.Errorf("the poll child survived runProgram's return: %v", res.reapErr)
	}
	if res.err == nil {
		t.Error("an externally killed program should report its termination")
	}
}

// waitPidOnce reads whatever pid the fixture recorded, without waiting.
func waitPidOnce(get func() int) int { return get() }
