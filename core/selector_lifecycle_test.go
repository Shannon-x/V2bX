package core

import (
	"errors"
	"reflect"
	"testing"

	"github.com/InazumaV/V2bX/conf"
)

type selectorProbeCore struct {
	Core
	name     string
	events   *[]string
	startErr error
	closeErr error
}

func (c *selectorProbeCore) Start() error {
	*c.events = append(*c.events, c.name+" start")
	return c.startErr
}

func (c *selectorProbeCore) Close() error {
	*c.events = append(*c.events, c.name+" close")
	return c.closeErr
}

func registerSelectorProbe(t *testing.T, name string, factory func(*conf.CoreConfig) (Core, error)) {
	t.Helper()
	previous, existed := cores[name]
	RegisterCore(name, factory)
	t.Cleanup(func() {
		if existed {
			cores[name] = previous
		} else {
			delete(cores, name)
		}
	})
}

func TestSelectorConstructionRollsBackEarlierCores(t *testing.T) {
	for _, failure := range []string{"error", "partial", "panic", "unknown", "duplicate", "nil"} {
		t.Run(failure, func(t *testing.T) {
			var events []string
			closeErr := errors.New("close failure")
			buildErr := errors.New("construction failure")
			registerSelectorProbe(t, "lifecycle-first", func(*conf.CoreConfig) (Core, error) {
				return &selectorProbeCore{name: "first", events: &events, closeErr: closeErr}, nil
			})
			registerSelectorProbe(t, "lifecycle-second", func(*conf.CoreConfig) (Core, error) {
				switch failure {
				case "panic":
					panic("constructor panic")
				case "partial":
					return &selectorProbeCore{name: "partial", events: &events}, buildErr
				case "nil":
					return nil, nil
				default:
					return nil, buildErr
				}
			})
			configs := []conf.CoreConfig{{Type: "lifecycle-first"}, {Type: "lifecycle-second"}}
			if failure == "unknown" {
				configs[1].Type = "unregistered-lifecycle-core"
			}
			if failure == "duplicate" {
				configs[1].Name = "lifecycle-first"
			}
			got, err := NewSelector(configs)
			if got != nil || err == nil || !errors.Is(err, closeErr) {
				t.Fatalf("constructor must roll back and retain cleanup errors: %v %v", got, err)
			}
			if (failure == "error" || failure == "partial") && !errors.Is(err, buildErr) {
				t.Fatalf("lost construction error: %v", err)
			}
			want := []string{"first close"}
			if failure == "partial" {
				want = []string{"partial close", "first close"}
			}
			if !reflect.DeepEqual(events, want) {
				t.Fatalf("cleanup events = %v, want %v", events, want)
			}
		})
	}
}

func TestSelectorStartFailureClosesAllCoresOnce(t *testing.T) {
	var events []string
	startErr, closeErr := errors.New("start failed"), errors.New("close failed")
	registerSelectorProbe(t, "lifecycle-probe", func(c *conf.CoreConfig) (Core, error) {
		probe := &selectorProbeCore{name: c.Name, events: &events}
		if c.Name == "second" {
			probe.startErr = startErr
		}
		if c.Name == "third" {
			probe.closeErr = closeErr
		}
		return probe, nil
	})
	s, err := NewSelector([]conf.CoreConfig{{Type: "lifecycle-probe", Name: "first"}, {Type: "lifecycle-probe", Name: "second"}, {Type: "lifecycle-probe", Name: "third"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Start(); !errors.Is(err, startErr) || !errors.Is(err, closeErr) {
		t.Fatalf("Start must retain startup and cleanup errors: %v", err)
	}
	if err := s.Close(); !errors.Is(err, closeErr) {
		t.Fatalf("Close did not retain previous cleanup error: %v", err)
	}
	if err := s.Start(); err == nil {
		t.Fatal("closed selector restarted")
	}
	want := []string{"first start", "second start", "third close", "second close", "first close"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events=%v, want=%v", events, want)
	}
}
