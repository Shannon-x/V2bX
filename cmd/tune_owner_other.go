//go:build !unix

package cmd

import "os"

// Non-Unix targets do not expose POSIX uid/gid ownership through FileInfo.
func preserveTunedConfigOwner(*os.File, os.FileInfo) error { return nil }
