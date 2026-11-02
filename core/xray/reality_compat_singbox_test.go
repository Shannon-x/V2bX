//go:build with_utls

package xray

import (
	"context"
	"net"
	"testing"
	"time"

	sbtls "github.com/sagernet/sing-box/common/tls"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/logger"
)

// sing-box's REALITY client strips the X25519MLKEM768 key share on purpose
// (common/tls/reality_client.go), so it is the client the 8cdf7bf check broke
// for every fingerprint. Needs -tags with_utls, like the release build.
func TestRealityCompatSingBoxClient(t *testing.T) {
	srv := startRealityCompatServer(t)
	for _, fp := range []string{"chrome", "firefox", "safari", "edge", "ios"} {
		t.Run(fp, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			cfg, err := sbtls.NewRealityClient(ctx, logger.NOP(), realityCompatSNI, option.OutboundTLSOptions{
				Enabled:    true,
				ServerName: realityCompatSNI,
				UTLS:       &option.OutboundUTLSOptions{Enabled: true, Fingerprint: fp},
				Reality: &option.OutboundRealityOptions{
					Enabled:   true,
					PublicKey: srv.publicKey,
					ShortID:   realityCompatShortID,
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			client, ok := cfg.(*sbtls.RealityClientConfig)
			if !ok {
				t.Fatalf("unexpected sing-box REALITY config type %T", cfg)
			}
			raw, err := net.Dial("tcp", srv.addr)
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			raw.SetDeadline(time.Now().Add(20 * time.Second))
			c, err := client.ClientHandshake(ctx, raw)
			if err != nil {
				t.Fatalf("REALITY handshake with sing-box fingerprint %q failed: %v", fp, err)
			}
			if err := realityCompatPing(c); err != nil {
				t.Fatalf("sing-box fingerprint %q: %v", fp, err)
			}
		})
	}
}
