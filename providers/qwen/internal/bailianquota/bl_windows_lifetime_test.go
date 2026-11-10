package bailianquota

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// This subprocess deliberately blocks on a private loopback connection instead
// of exiting cooperatively. No installed CLI, credentials, or upstream is used.
func TestBLWindowsLifetimeHelperProcess(t *testing.T) {
	address := os.Getenv("BAILIAN_TEST_LIFETIME_READY")
	if address == "" {
		return
	}
	conn, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		os.Exit(2)
	}
	defer conn.Close()
	if _, err := fmt.Fprintf(conn, "%d %d\n", os.Getpid(), os.Getppid()); err != nil {
		os.Exit(2)
	}
	var stop [1]byte
	_, _ = conn.Read(stop[:])
	os.Exit(0)
}

func lifetimeReady(t *testing.T) *net.TCPListener {
	t.Helper()
	listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	if err := listener.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	return listener
}

func lifetimeProcess(t *testing.T, pid int) syscall.Handle {
	t.Helper()
	// Retain an OS handle, not merely a recyclable PID. Cleanup can terminate
	// only this exact test-owned process even when an assertion fails (RED).
	handle, err := syscall.OpenProcess(syscall.SYNCHRONIZE|syscall.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if state, _ := syscall.WaitForSingleObject(handle, 0); state != syscall.WAIT_OBJECT_0 {
			_ = syscall.TerminateProcess(handle, 1)
			_, _ = syscall.WaitForSingleObject(handle, 1000)
		}
		syscall.CloseHandle(handle)
	})
	return handle
}

func acceptLifetimeProcess(t *testing.T, listener *net.TCPListener, ownsParent bool) (syscall.Handle, syscall.Handle) {
	t.Helper()
	conn, err := listener.Accept()
	if err != nil {
		t.Fatal("synthetic subprocess did not become ready:", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var pid, parent int
	if _, err := fmt.Fscanln(conn, &pid, &parent); err != nil {
		t.Fatal(err)
	}
	t.Logf("synthetic helper pid=%d parent=%d", pid, parent)
	child := lifetimeProcess(t, pid)
	if ownsParent {
		return child, lifetimeProcess(t, parent)
	}
	return child, 0
}

func TestBLWindowsRunCLIDeadlineReapsShimTreeOnly(t *testing.T) {
	runBLWindowsLifetime(t, false)
}

func TestBLWindowsRunCLIParentCancellationReapsShimTreeOnly(t *testing.T) {
	runBLWindowsLifetime(t, true)
}

func runBLWindowsLifetime(t *testing.T, cancelWhenReady bool) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// An independently launched sibling must survive cleanup of the CLI tree.
	unrelatedReady := lifetimeReady(t)
	unrelated := exec.Command(executable, "-test.run=^TestBLWindowsLifetimeHelperProcess$")
	unrelated.Env = append(os.Environ(), "BAILIAN_TEST_LIFETIME_READY="+unrelatedReady.Addr().String())
	if err := unrelated.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unrelated.Process.Kill(); _ = unrelated.Wait() })
	unrelatedHandle, _ := acceptLifetimeProcess(t, unrelatedReady, false)

	// A private copied image makes the Windows executable lock observable,
	// independently of Go's own eventual temporary-build cleanup.
	dir := t.TempDir()
	image := filepath.Join(dir, "owned-helper.exe")
	input, err := os.Open(executable)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := os.Create(image)
	if err != nil {
		t.Fatal(err)
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil || closeErr != nil {
		t.Fatal(copyErr, closeErr)
	}
	// Validate the freshly copied fixture image before measuring CLI cleanup.
	// Startup includes Windows image scanning; tree ownership needs a ready child
	// before expiry. Cancellation is still triggered immediately after readiness.
	verifyCtx, verifyCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer verifyCancel()
	if err := exec.CommandContext(verifyCtx, image, "-test.run=^$").Run(); err != nil {
		t.Fatal("cannot execute synthetic helper image:", err)
	}
	shim := filepath.Join(dir, "fake-bl.cmd")
	body := "@echo off\r\n\"" + image + "\" -test.run=^TestBLWindowsLifetimeHelperProcess$ -- %*\r\n"
	if err := os.WriteFile(shim, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	ready := lifetimeReady(t)
	t.Setenv("BAILIAN_TEST_LIFETIME_READY", ready.Addr().String())
	var out, diagnostics bytes.Buffer
	done := make(chan int, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	start := time.Now()
	go func() {
		done <- RunCLI(ctx, []string{"--source", "bl", "--bl-path", shim, "--timeout", "2s"}, &out, &diagnostics)
	}()
	child, shell := acceptLifetimeProcess(t, ready, true)
	if cancelWhenReady {
		cancel()
	}
	select {
	case code := <-done:
		if code != 1 {
			t.Fatal(code, out.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("unbounded subprocess cleanup")
	}
	assertBLFailure(t, out.Bytes(), "timeout")
	for name, handle := range map[string]syscall.Handle{"shim": shell, "child": child} {
		if state, err := syscall.WaitForSingleObject(handle, 0); err != nil || state != syscall.WAIT_OBJECT_0 {
			t.Errorf("%s still alive after RunCLI returned: state=%d err=%v", name, state, err)
		}
	}
	if err := os.Remove(image); err != nil {
		t.Errorf("owned executable still locked after RunCLI returned: %v", err)
	}
	if state, err := syscall.WaitForSingleObject(unrelatedHandle, 0); err != nil || state != syscall.WAIT_TIMEOUT {
		t.Errorf("unrelated subprocess was killed: state=%d err=%v", state, err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatal("unbounded subprocess", elapsed)
	}
}

func TestBLWindowsRunCLICanceledBeforeStartDoesNotLaunch(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "must-not-start.txt")
	shim := filepath.Join(dir, "fake-bl.cmd")
	if err := os.WriteFile(shim, []byte("@echo off\r\necho started>\""+marker+"\"\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out, diagnostics bytes.Buffer
	if code := RunCLI(ctx, []string{"--source", "bl", "--bl-path", shim, "--timeout", "200ms"}, &out, &diagnostics); code != 1 {
		t.Fatal(code, out.String())
	}
	assertBLFailure(t, out.Bytes(), "timeout")
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("canceled invocation started its shim", err)
	}
}
