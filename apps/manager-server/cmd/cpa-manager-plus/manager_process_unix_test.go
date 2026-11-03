//go:build !windows

package main

import (
	"os"
	"os/exec"
)

func configureManagerServerProcess(cmd *exec.Cmd) {}

func signalManagerServerProcess(cmd *exec.Cmd) error {
	return cmd.Process.Signal(os.Interrupt)
}
