package node

import (
	"path/filepath"
	"testing"
)

func Test_generateSelfSslCertificate(t *testing.T) {
	dir := t.TempDir()
	t.Log(generateSelfSslCertificate("domain.com",
		filepath.Join(dir, "1.pem"), filepath.Join(dir, "1.key")))
}
