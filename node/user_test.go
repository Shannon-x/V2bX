package node

import (
	"testing"

	"github.com/InazumaV/V2bX/api/panel"
)

func TestBuildOnlineDeviceReportReturnsNonNilEmptySnapshot(t *testing.T) {
	data, reported := buildOnlineDeviceReport(nil, nil, 0)
	if data == nil {
		t.Fatal("empty full snapshot must encode as {}, not null")
	}
	if len(data) != 0 || reported != 0 {
		t.Fatalf("unexpected empty snapshot result: data=%v reported=%d", data, reported)
	}
}

func TestBuildOnlineDeviceReportFiltersLowTrafficUsers(t *testing.T) {
	devices := []panel.OnlineUser{{UID: 1, IP: "1.1.1.1"}, {UID: 2, IP: "2.2.2.2"}}
	traffic := []panel.UserTraffic{{UID: 1, Upload: 100, Download: 100}, {UID: 2, Upload: 1000, Download: 1000}}

	data, reported := buildOnlineDeviceReport(devices, traffic, 1)
	if reported != 1 || len(data) != 1 || len(data[2]) != 1 {
		t.Fatalf("unexpected filtered snapshot: data=%v reported=%d", data, reported)
	}
}
