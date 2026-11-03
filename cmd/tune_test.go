package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestTunePreviewAndBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	original := []byte(`{"Cores":[{"Type":"xray","XrayConnectionConfig":{"bufferSize":256,"connIdle":300}}],"ApiKey":"must-not-print"}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	output := new(bytes.Buffer)
	c := newTuneCommand()
	c.SetOut(output)
	c.SetArgs([]string{"-c", path})
	if err := c.Execute(); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, original) || bytes.Contains(output.Bytes(), []byte("must-not-print")) {
		t.Fatal("preview wrote config or printed secret")
	}
	updated := []byte(`{"Cores":[]}`)
	backup, err := writeTunedConfig(path, original, updated)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(backup)
	if err != nil || !bytes.Equal(b, original) {
		t.Fatalf("backup: %s %v", b, err)
	}
	got, _ = os.ReadFile(path)
	info, _ := os.Stat(path)
	if !bytes.Equal(got, updated) || info.Mode().Perm() != 0600 {
		t.Fatal("write or permissions incorrect")
	}
}
