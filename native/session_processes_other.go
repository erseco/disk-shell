//go:build !linux

package main

import (
	"syscall"
	"time"
)

// Local development fallback. Installed DSM packages always target Linux.
func terminateShellProcesses(sessionID int) {
	_ = syscall.Kill(-sessionID, syscall.SIGTERM)
	time.Sleep(2 * time.Second)
	_ = syscall.Kill(-sessionID, syscall.SIGKILL)
}
