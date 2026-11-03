//go:build unix

package cmd

import (
	"fmt"
	"os"
	"syscall"
)

func preserveTunedConfigOwner(file *os.File, original os.FileInfo) error {
	owner, ok := original.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("original file ownership is unavailable")
	}
	current, err := file.Stat()
	if err != nil {
		return err
	}
	stat, ok := current.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("temporary file ownership is unavailable")
	}
	if stat.Uid == owner.Uid && stat.Gid == owner.Gid {
		return nil
	}
	// Fail before replacing the original if the caller cannot retain its
	// ownership. In particular, sudo must not turn a service user's 0600
	// configuration into a root-owned file that the service can no longer read.
	return file.Chown(int(owner.Uid), int(owner.Gid))
}
