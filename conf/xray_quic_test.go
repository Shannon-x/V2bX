package conf

import "testing"

func TestXrayQUICValidation(t *testing.T) {
	for _, c := range []XrayQUICConfig{
		{InitStreamReceiveWindow: 1}, {MaxStreamReceiveWindow: 1 << 20},
		{InitConnectionReceiveWindow: 21 << 20}, {MaxIncomingStreams: -1},
		{MaxIdleTimeout: 121}, {InitStreamReceiveWindow: 1 << 62},
	} {
		if err := c.Validate(); err == nil {
			t.Errorf("accepted invalid windows: %+v", c)
		}
	}
	if err := (&XrayQUICConfig{}).Validate(); err != nil {
		t.Fatal(err)
	}
}
