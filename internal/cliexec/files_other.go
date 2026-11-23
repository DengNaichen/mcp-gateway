//go:build !unix

package cliexec

import (
	"errors"
	"os"
)

func openNoFollow(string) (*os.File, error) { return nil, errors.ErrUnsupported }

func ownedByGateway(os.FileInfo) bool { return false }
