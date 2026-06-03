package conf

import (
	"net"
	"testing"
)

// TestIsUnsafeIP guards the W4.1 / audit #9 DNS-rebinding-resistant SSRF
// filter. The previous filter only rejected literal-IP hosts; this test
// pins the explicit IP-classification logic that's now applied to every
// resolved address from the LookupIPAddr step in DialContext.
func TestIsUnsafeIP(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
		note string
	}{
		{"127.0.0.1", true, "v4 loopback"},
		{"::1", true, "v6 loopback"},
		{"10.0.0.1", true, "RFC1918 v4"},
		{"172.16.0.1", true, "RFC1918 v4"},
		{"192.168.1.1", true, "RFC1918 v4"},
		{"169.254.169.254", true, "AWS IMDS link-local"},
		{"fe80::1", true, "v6 link-local"},
		{"0.0.0.0", true, "unspecified"},
		{"::", true, "v6 unspecified"},
		{"224.0.0.1", true, "v4 multicast"},
		{"fd00::1", true, "v6 ULA private"},
		// Public, allowed addresses
		{"1.1.1.1", false, "public v4"},
		{"8.8.8.8", false, "public v4"},
		{"2001:4860:4860::8888", false, "public v6"},
	}
	for _, c := range cases {
		ip := net.ParseIP(c.ip)
		if got := isUnsafeIP(ip); got != c.want {
			t.Errorf("isUnsafeIP(%s [%s]) = %v, want %v", c.ip, c.note, got, c.want)
		}
	}
}
