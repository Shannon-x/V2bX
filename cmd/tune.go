package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/InazumaV/V2bX/conf"
	"github.com/spf13/cobra"
)

func newTuneCommand() *cobra.Command {
	var path, profile string
	var apply bool
	c := &cobra.Command{
		Use: "tune", Short: "Preview or apply an Xray memory profile to a config file",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			out, changes, err := conf.TuneXrayPerformance(data, profile)
			if err != nil {
				return err
			}
			if len(changes) == 0 {
				cmd.Println("No changes needed.")
				return nil
			}
			for _, change := range changes {
				cmd.Println(change)
			}
			if !apply {
				cmd.Println("Preview only. Use --apply to back up and update this file.")
				return nil
			}
			backup, err := writeTunedConfig(path, data, out)
			if err != nil {
				return err
			}
			cmd.Printf("Updated config; original saved to %s\n", backup)
			return nil
		},
	}
	c.Flags().StringVarP(&path, "config", "c", "/etc/V2bX/config.json", "config file path")
	c.Flags().StringVar(&profile, "profile", "balanced", "balanced (128 KiB) or low-memory (64 KiB)")
	c.Flags().BoolVar(&apply, "apply", false, "back up and atomically update the config")
	return c
}

func init() { command.AddCommand(newTuneCommand()) }

func writeTunedConfig(path string, original, updated []byte) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("config must be a regular file (resolve symlinks explicitly)")
	}
	backup := path + ".performance-backup-" + time.Now().UTC().Format("20060102T150405.000000000Z")
	f, err := os.OpenFile(backup, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	_, writeErr := f.Write(original)
	syncErr := f.Sync()
	closeErr := f.Close()
	if writeErr != nil {
		return backup, writeErr
	}
	if syncErr != nil {
		return backup, syncErr
	}
	if closeErr != nil {
		return backup, closeErr
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".v2bx-tune-*")
	if err != nil {
		return backup, err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if err := preserveTunedConfigOwner(tmp, info); err != nil {
		return backup, fmt.Errorf("preserve config ownership: %w", err)
	}
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		return backup, err
	}
	if _, err := tmp.Write(updated); err != nil {
		return backup, err
	}
	if err := tmp.Sync(); err != nil {
		return backup, err
	}
	if err := tmp.Close(); err != nil {
		return backup, err
	}
	current, err := os.ReadFile(path)
	if err != nil {
		return backup, err
	}
	if !bytes.Equal(current, original) {
		return backup, fmt.Errorf("config changed during tuning; original file left in place")
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return backup, err
	}
	return backup, nil
}
