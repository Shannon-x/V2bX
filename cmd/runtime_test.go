package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/InazumaV/V2bX/conf"
)

type fakeRuntime struct {
	name     string
	events   *[]string
	startErr error
}

func TestRuntimeSnapshotOwnsMutableConfiguration(t *testing.T) {
	c := conf.New()
	c.CoresConfig = []conf.CoreConfig{{Type: "xray", XrayConfig: conf.NewXrayConfig()}}
	c.NodeConfig = []conf.NodeConfig{{Options: conf.Options{
		RawOptions:  []byte("original"),
		CertConfig:  &conf.CertConfig{TlsCert: "in-memory-cert", DNSEnv: map[string]string{"key": "original"}},
		XrayOptions: &conf.XrayOptions{TrustedXForwardedFor: []string{"original"}},
	}}}
	snapshot := snapshotRuntimeConfig(c)
	c.CoresConfig[0].XrayConfig.ConnectionConfig.BufferSize = 4096
	c.NodeConfig[0].Options.RawOptions[0] = 'x'
	c.NodeConfig[0].Options.CertConfig.DNSEnv["key"] = "changed"
	c.NodeConfig[0].Options.XrayOptions.TrustedXForwardedFor[0] = "changed"
	if snapshot.cores[0].XrayConfig.ConnectionConfig.BufferSize != 128 || string(snapshot.nodes[0].Options.RawOptions) != "original" || snapshot.nodes[0].Options.CertConfig.DNSEnv["key"] != "original" || snapshot.nodes[0].Options.XrayOptions.TrustedXForwardedFor[0] != "original" || snapshot.nodes[0].Options.CertConfig.TlsCert != "in-memory-cert" {
		t.Fatal("snapshot aliases mutable data or lost private config")
	}
}

func (r *fakeRuntime) Start() error {
	*r.events = append(*r.events, r.name+" start")
	return r.startErr
}
func (r *fakeRuntime) Close() error { *r.events = append(*r.events, r.name+" close"); return nil }

func TestReloadPreparationKeepsServingAndFailureRollsBack(t *testing.T) {
	var events []string
	old := &fakeRuntime{name: "old", events: &events}
	invalid := func() (managedRuntime, error) { return nil, errors.New("invalid config") }
	got, applied, err := replaceRuntime(old, func() error { return errors.New("invalid config") }, invalid, invalid)
	if err == nil || applied || got != old || len(events) != 0 {
		t.Fatal("preparation failure stopped old runtime")
	}
	next := &fakeRuntime{name: "new", events: &events, startErr: errors.New("port in use")}
	rollback := &fakeRuntime{name: "rollback", events: &events}
	got, applied, err = replaceRuntime(old, func() error { return nil }, func() (managedRuntime, error) { return next, nil }, func() (managedRuntime, error) { return rollback, nil })
	if err == nil || applied || got != rollback {
		t.Fatal("rollback did not remain serving")
	}
	want := []string{"old close", "new start", "new close", "rollback start"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("%v, want %v", events, want)
	}
}

func TestReloadRejectsInvalidRoutingTargetBeforeStopping(t *testing.T) {
	path := filepath.Join(t.TempDir(), "route.json")
	x := conf.NewXrayConfig()
	x.RouteConfigPath = path
	for _, data := range []string{
		`{"rules":[{"outboundTag":123}]}`,
		`{"rules":[null]}`,
		`{"rules":[{"domain":["example.com"]}]}`,
	} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		var events []string
		old := &fakeRuntime{name: "old", events: &events}
		unexpected := func() (managedRuntime, error) {
			t.Fatal("invalid route reached core construction")
			return nil, nil
		}
		got, applied, err := replaceRuntime(old, func() error { return validateXrayFiles(x) }, unexpected, unexpected)
		if err == nil || applied || got != old || len(events) != 0 {
			t.Fatalf("invalid route stopped serving: %s", data)
		}
	}
	if err := os.WriteFile(path, []byte(`{"rules":[{"outboundTag":"direct","domain":["example.com"]}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateXrayFiles(x); err != nil {
		t.Fatal(err)
	}
}

func TestReloadConstructorFailureRestoresPrevious(t *testing.T) {
	var events []string
	old := &fakeRuntime{name: "old", events: &events}
	restored := &fakeRuntime{name: "restored", events: &events}
	got, applied, err := replaceRuntime(old, func() error { return nil }, func() (managedRuntime, error) {
		events = append(events, "construct new")
		return nil, errors.New("constructor failed")
	}, func() (managedRuntime, error) { return restored, nil })
	if err == nil || applied || got != restored || !reflect.DeepEqual(events, []string{"old close", "construct new", "restored start"}) {
		t.Fatalf("constructor rollback: got %v, applied %v, error %v, events %v", got, applied, err, events)
	}
}
