//go:build !unix

package gateway

import (
	"os/exec"
	"time"
)

func configureProcess(cmd *exec.Cmd) { cmd.WaitDelay = 2 * time.Second }
