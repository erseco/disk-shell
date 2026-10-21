package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type shellProcess struct {
	pid, parent, group, session int
	started                     string
}

// Job control creates separate process groups. Commands such as sudo can also
// create nested sessions. Track both terminal-session members and descendants,
// retaining process identities if they become reparented during shutdown.
func terminateShellProcesses(sessionID int) {
	tracked := map[int]shellProcess{}
	collectShellProcesses(sessionID, tracked)
	signalShellProcesses(tracked, syscall.SIGTERM)
	time.Sleep(2 * time.Second)
	collectShellProcesses(sessionID, tracked)
	signalShellProcesses(tracked, syscall.SIGKILL)
	// The caller keeps the original leader unreaped until we return, reserving
	// its PID/session ID throughout both scans.
}

func collectShellProcesses(sessionID int, tracked map[int]shellProcess) {
	entries, _ := os.ReadDir("/proc")
	processes := map[int]shellProcess{}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		if process, ok := readShellProcess(pid); ok {
			processes[pid] = process
		}
	}
	for pid, process := range processes {
		if process.session == sessionID || pid == sessionID {
			tracked[pid] = process
		}
	}
	// Include descendants even when they have moved into a new session.
	for changed := true; changed; {
		changed = false
		for pid, process := range processes {
			if old, ok := tracked[pid]; ok && old.started == process.started {
				continue
			}
			parent, ok := tracked[process.parent]
			liveParent, alive := processes[process.parent]
			if ok && alive && parent.started == liveParent.started {
				tracked[pid] = process
				changed = true
			}
		}
	}
}

func signalShellProcesses(processes map[int]shellProcess, signal syscall.Signal) {
	for pid, process := range processes {
		// Recheck the start time to reject PIDs reused since the collection scan.
		current, ok := readShellProcess(pid)
		if !ok || pid <= 1 || current.started != process.started {
			continue
		}
		_ = syscall.Kill(pid, signal)
		if signal == syscall.SIGTERM {
			_ = syscall.Kill(pid, syscall.SIGCONT)
		}
	}
}

func readShellProcess(pid int) (shellProcess, bool) {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return shellProcess{}, false
	}
	return parseShellProcess(pid, string(data))
}

func parseShellProcess(pid int, stat string) (shellProcess, bool) {
	// comm can contain spaces and parentheses; after its final ')' come state,
	// ppid, pgrp, session, ... and starttime at offset 19.
	end := strings.LastIndexByte(stat, ')')
	if end < 0 {
		return shellProcess{}, false
	}
	fields := strings.Fields(stat[end+1:])
	if len(fields) < 20 {
		return shellProcess{}, false
	}
	parent, e1 := strconv.Atoi(fields[1])
	group, e2 := strconv.Atoi(fields[2])
	session, e3 := strconv.Atoi(fields[3])
	return shellProcess{pid: pid, parent: parent, group: group, session: session, started: fields[19]}, e1 == nil && e2 == nil && e3 == nil
}
