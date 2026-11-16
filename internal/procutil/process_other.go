//go:build !unix

package procutil

import (
	"os/exec"
	"time"
)

func ConfigureProcess(cmd *exec.Cmd) { cmd.WaitDelay = 2 * time.Second }
