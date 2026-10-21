package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestProcessStatSessionWithComplexName(t *testing.T) {
	process, ok := parseShellProcess(123, "123 (job (with) spaces)) S 1 321 456 "+strings.Repeat("0 ", 15)+"98765")
	if !ok || process.group != 321 || process.session != 456 || process.started != "98765" {
		t.Fatalf("incorrect stat parsing: %+v %v", process, ok)
	}
}

func TestClosingSessionTerminatesJobControlGroups(t *testing.T) {
	for _, persistent := range []bool{false, true} {
		t.Run(fmt.Sprintf("persistent=%v", persistent), func(t *testing.T) {
			manager := newSessionManager()
			t.Cleanup(manager.shutdown)
			account := currentTestIdentity(t)
			session, attachment, _, err := manager.open(account, "", "Job cleanup", false, "")
			if err != nil {
				t.Fatal(err)
			}
			go func(attachment *sessionAttachment) {
				for {
					select {
					case <-attachment.output:
					case <-attachment.info:
					case <-attachment.done:
						return
					}
				}
			}(attachment)
			root := t.TempDir()
			helper := filepath.Join(root, "job.sh")
			if err := os.WriteFile(helper, []byte("#!/bin/sh\ntrap '' HUP TERM\necho $$ > \"$1\"\nwhile :; do sleep 1; done\n"), 0700); err != nil {
				t.Fatal(err)
			}
			background, foreground := filepath.Join(root, "bg.pid"), filepath.Join(root, "fg.pid")
			nested := filepath.Join(root, "nested.pid")
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			session.write([]byte(fmt.Sprintf("sh %s %s & DISKSHELL_TEST_SCRIPT=%s DISKSHELL_TEST_PID=%s %s -test.run=^TestNestedSessionChild$ & sh %s %s\n", helper, background, helper, nested, binary, helper, foreground)))
			pids := []int{waitJobPID(t, background), waitJobPID(t, foreground), waitJobPID(t, nested)}
			for _, pid := range pids {
				t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
				group, err := syscall.Getpgid(pid)
				if err != nil || group == session.command.Process.Pid {
					t.Fatalf("job control not active: pid=%d group=%d err=%v", pid, group, err)
				}
			}
			// Another session must never be caught by the cleanup scan.
			other := exec.Command("sleep", "120")
			other.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			if err := other.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = other.Process.Kill(); _ = other.Wait() })
			if persistent {
				session.setPersistent(true)
			}
			session.detach(attachment)
			if persistent {
				time.Sleep(2300 * time.Millisecond)
				for _, pid := range pids {
					if !jobAlive(pid) {
						session.mu.Lock()
						output := string(session.output)
						running := session.running
						terminating := session.terminating
						session.mu.Unlock()
						t.Fatalf("persistent job %d exited (pids=%v running=%v terminating=%v): %s", pid, pids, running, terminating, output)
					}
				}
				if manager.find(account.username, session.id) == nil {
					t.Fatal("persistent session removed")
				}
				// Turning persistence off restores normal close semantics.
				attachment, _ = session.attach()
				session.setPersistent(false)
				session.detach(attachment)
			}
			select {
			case <-session.finished:
			case <-time.After(6 * time.Second):
				t.Fatal("session cleanup stalled")
			}
			for _, pid := range pids {
				// SIGKILL delivery is asynchronous, even after the leader is reaped.
				deadline := time.Now().Add(time.Second)
				for jobAlive(pid) && time.Now().Before(deadline) {
					time.Sleep(10 * time.Millisecond)
				}
				if jobAlive(pid) {
					t.Errorf("job %d survived tab close", pid)
				}
			}
			if !jobAlive(other.Process.Pid) {
				t.Error("unrelated session was killed")
			}
		})
	}
}

func waitJobPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(path)
		if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 1 {
			return pid
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("job did not start: %s", path)
	return 0
}

func jobAlive(pid int) bool {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	fields := strings.Fields(string(data)[strings.LastIndexByte(string(data), ')')+1:])
	return len(fields) > 0 && fields[0] != "Z"
}

func TestNestedSessionChild(t *testing.T) {
	script := os.Getenv("DISKSHELL_TEST_SCRIPT")
	if script == "" {
		return
	}
	command := exec.Command("sh", script, os.Getenv("DISKSHELL_TEST_PID"))
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	_ = command.Run()
}
