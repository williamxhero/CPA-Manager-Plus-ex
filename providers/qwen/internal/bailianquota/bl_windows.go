package bailianquota

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

func blCommand(ctx context.Context, path string, args ...string) (*windowsBLCommand, error) {
	var cmd *exec.Cmd
	switch strings.ToLower(filepath.Ext(path)) {
	case ".cmd", ".bat":
		// Official npm installs use bl.cmd. cmd.exe is needed only for this
		// shim; reject expansion/control characters and keep arguments fixed.
		command := strings.Join(args, " ")
		if strings.ContainsAny(path, "\"\r\n&|<>^%!\x00") || command != "usage token-plan --output json" && command != "usage coding-plan --output json" {
			return nil, errors.New("unsupported bl shim path or arguments")
		}
		processor := filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
		cmd = exec.CommandContext(ctx, processor)
		cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd.exe /d /s /c ""` + path + `" ` + command + `"`}
	default:
		cmd = exec.CommandContext(ctx, path, args...)
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	// Assign the suspended process to its private job before any shim/CLI code
	// can create descendants. No breakaway flag is enabled on the job.
	cmd.SysProcAttr.CreationFlags |= 0x00000004 // CREATE_SUSPENDED
	cmd.SysProcAttr.HideWindow = true
	return &windowsBLCommand{Cmd: cmd, ctx: ctx}, nil
}

type windowsBLCommand struct {
	*exec.Cmd
	ctx context.Context
}

func (cmd *windowsBLCommand) Run() (err error) {
	job, err := newBLJob()
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := job.close(); closeErr != nil {
			err = closeErr
		}
	}()
	// CommandContext may invoke Cancel as soon as Start succeeds. Serialize it
	// with assignment/resume so cancellation cannot race uncontained startup.
	var startup sync.Mutex
	cmd.Cancel = func() error {
		startup.Lock()
		defer startup.Unlock()
		return job.terminate()
	}
	startup.Lock()
	if err := cmd.Start(); err != nil {
		startup.Unlock()
		return err
	}
	setupErr := job.assign(uint32(cmd.Process.Pid))
	if setupErr == nil {
		setupErr = cmd.ctx.Err()
	}
	if setupErr == nil {
		setupErr = resumeBLProcess(uint32(cmd.Process.Pid))
	}
	if setupErr != nil {
		// Stop the job even if resume was attempted. A root not yet assigned
		// is still suspended and cannot have created an uncontained child.
		setupErr = errors.Join(setupErr, job.terminate(), cmd.Process.Kill())
	}
	startup.Unlock()
	waitErr := cmd.Wait()
	stopErr := job.terminate()
	if lifecycleErr := errors.Join(setupErr, stopErr, job.reap()); lifecycleErr != nil {
		// Do not wrap an ExitError here: the legacy payload parser tolerates
		// one specific Windows teardown exit, never a lifecycle failure.
		return lifecycleErr
	}
	return waitErr
}
