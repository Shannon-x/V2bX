//go:build unix

package cmd

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestTunePreservesOwnerAndGroup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	original := []byte(`{"Cores":[]}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() == 0 {
		// Exercise the original failure mode when tests have root privileges.
		if err := os.Chown(path, 65534, 65534); err != nil {
			t.Fatal(err)
		}
	} else {
		// A normal user can exercise the actual chown path with a secondary
		// group, without requiring root or changing ownership of external files.
		groups, err := os.Getgroups()
		if err != nil {
			t.Fatal(err)
		}
		changedGroup := false
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, gid := range groups {
			if gid != int(info.Sys().(*syscall.Stat_t).Gid) && os.Chown(path, -1, gid) == nil {
				changedGroup = true
				break
			}
		}
		if !changedGroup {
			t.Log("no writable secondary group; checking unchanged uid/gid")
		}
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writeTunedConfig(path, original, []byte(`{"Cores":[],"updated":true}`)); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	want, got := before.Sys().(*syscall.Stat_t), after.Sys().(*syscall.Stat_t)
	if got.Uid != want.Uid || got.Gid != want.Gid || after.Mode().Perm() != 0600 {
		t.Fatalf("ownership/mode changed: %d:%d %v -> %d:%d %v", want.Uid, want.Gid, before.Mode(), got.Uid, got.Gid, after.Mode())
	}
}

type overriddenOwnerInfo struct {
	os.FileInfo
	owner *syscall.Stat_t
}

func (i overriddenOwnerInfo) Sys() any { return i.owner }

func TestTuneOwnerFailureIsReturned(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can change file ownership")
	}
	f, err := os.CreateTemp(t.TempDir(), "temp")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	owner := *info.Sys().(*syscall.Stat_t)
	owner.Uid++
	if err := preserveTunedConfigOwner(f, overriddenOwnerInfo{FileInfo: info, owner: &owner}); err == nil {
		t.Fatal("ownership failure was silently ignored")
	}
}
