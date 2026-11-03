package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

const managerCtrlBreakPID = "CPA_MANAGER_TEST_CTRL_BREAK_PID"

func configureManagerServerProcess(cmd *exec.Cmd) {
	// An independent hidden console also works when the test runner has no
	// console. CREATE_NEW_PROCESS_GROUP is ignored with CREATE_NEW_CONSOLE;
	// isolation here is by console, not an assumed process-group ID.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_CONSOLE,
		HideWindow:    true,
	}
}

func signalManagerServerProcess(cmd *exec.Cmd) error {
	// AttachConsole resets handlers process-wide. Never attach/detach the test
	// runner itself: use a short-lived sender with no initial console instead.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sender := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestManagerServerControlBreakProcess$")
	sender.Env = append(os.Environ(), managerCtrlBreakPID+"="+strconv.Itoa(cmd.Process.Pid))
	sender.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NO_WINDOW,
		HideWindow:    true,
	}
	if output, err := sender.CombinedOutput(); err != nil {
		return fmt.Errorf("send Ctrl-Break to owned helper pid=%d: %w\n%s", cmd.Process.Pid, err, output)
	}
	return nil
}

func TestManagerServerControlBreakProcess(t *testing.T) {
	pidText := os.Getenv(managerCtrlBreakPID)
	if pidText == "" {
		return // Only the dedicated sender invocation has work to do.
	}
	pid, err := strconv.ParseUint(pidText, 10, 32)
	if err != nil || pid == 0 {
		t.Fatalf("invalid manager helper PID %q: %v", pidText, err)
	}
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	// Some hosts still associate the sender with a console despite
	// CREATE_NO_WINDOW. Detach only this disposable sender before attaching;
	// failure here simply means it did not have a console in the first place.
	kernel32.NewProc("FreeConsole").Call()
	attached, _, err := kernel32.NewProc("AttachConsole").Call(uintptr(pid))
	if attached == 0 {
		t.Fatalf("attach to owned manager helper console: %v", err)
	}
	// Attaching resets this sender's Go console handler. Protect the sender
	// from the broadcast Ctrl-Break; the manager keeps its real Go handler.
	handled := make(chan struct{}, 1)
	ignoreBreak := syscall.NewCallback(func(event uint32) uintptr {
		if event == windows.CTRL_BREAK_EVENT {
			select {
			case handled <- struct{}{}:
			default:
			}
			return 1
		}
		return 0
	})
	installed, _, err := kernel32.NewProc("SetConsoleCtrlHandler").Call(ignoreBreak, 1)
	if installed == 0 {
		t.Fatalf("install sender Ctrl-Break handler: %v", err)
	}
	// Group 0 reaches this isolated console, not the runner or other tests.
	// Go maps CTRL_BREAK_EVENT to os.Interrupt, exercising runServer's existing
	// signal.NotifyContext and graceful shutdown without any product changes.
	if err := windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, 0); err != nil {
		t.Fatalf("deliver manager helper Ctrl-Break: %v", err)
	}
	// Delivery is asynchronous. Keep the handler/console intact until our own
	// event has arrived; FreeConsole would reset handlers before delivery.
	// Process exit detaches this disposable sender automatically.
	select {
	case <-handled:
	case <-time.After(time.Second):
		t.Fatal("sender did not receive its own Ctrl-Break")
	}
}
