package main

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"reflect"
	"syscall"
	"testing"
	"time"
)

func TestSudoShellStartsAsAuthenticatedAccount(t *testing.T) {
	account := &identity{username: "admin", uid: 12345, gid: 12346, groups: []uint32{12346, 100}, home: "/tmp"}
	for _, sudo := range []bool{false, true} {
		command := sessionCommand(account, sudo)
		expected := []string{"/bin/sh", "-l"}
		if sudo {
			expected = []string{"/usr/bin/sudo", "-S", "-k", "-H", "-p", "", "--", "/bin/sh", "-c", `cd "$HOME" && exec /bin/sh -l </dev/tty`}
		}
		if !reflect.DeepEqual(command.Args, expected) {
			t.Fatalf("unexpected command: %v", command.Args)
		}
		credential := command.SysProcAttr.Credential
		if credential == nil || credential.Uid != account.uid || credential.Gid != account.gid || !reflect.DeepEqual(credential.Groups, account.groups) {
			t.Fatalf("shell bypassed caller identity: %+v", credential)
		}
		if command.Dir != account.home {
			t.Fatal("unexpected working directory")
		}
	}
}

func TestReattachKeepsOriginalSudoMode(t *testing.T) {
	manager := newSessionManager()
	for _, sudo := range []bool{false, true} {
		session := &shellSession{manager: manager, id: "existing", owner: "admin", sudo: sudo, running: true}
		manager.sessions[session.id] = session
		opened, attachment, _, err := manager.open(&identity{username: "admin"}, session.id, "ignored", !sudo, "")
		if err != nil || opened != session || opened.info().Sudo != sudo {
			t.Fatalf("reattach changed session mode: %v", err)
		}
		attachment.stop()
		if _, _, _, err := manager.open(&identity{username: "other"}, session.id, "", true, ""); err == nil {
			t.Fatal("foreign session attached")
		}
	}
}

func TestSudoPasswordUsesSeparatePipe(t *testing.T) {
	for _, consume := range []bool{false, true} {
		t.Run(fmt.Sprintf("passwordRequired=%v", consume), func(t *testing.T) {
			script := `exec /bin/sh </dev/tty`
			if consume {
				script = `IFS= read -r password; unset password; ` + script
			}
			command := exec.Command("/bin/sh", "-c", script)
			command.SysProcAttr = &syscall.SysProcAttr{}
			terminal, err := startSessionCommand(command, true, "echo SECRET_MUST_NOT_APPEAR")
			if err != nil {
				t.Fatal(err)
			}
			defer terminal.Close()
			defer command.Process.Kill()
			output := make(chan []byte, 1)
			go func() { data, _ := io.ReadAll(terminal); output <- data }()
			_, err = terminal.Write([]byte("printf '__SUDO_PIPE_OK__\\n'; exit\n"))
			if err != nil {
				t.Fatal(err)
			}
			select {
			case data := <-output:
				if !bytes.Contains(data, []byte("__SUDO_PIPE_OK__")) || bytes.Contains(data, []byte("SECRET_MUST_NOT_APPEAR")) {
					t.Fatalf("password leaked or terminal failed: %q", data)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("PTY stdin was not restored")
			}
			_ = command.Wait()
		})
	}
}
