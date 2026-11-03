package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"

	"github.com/InazumaV/V2bX/conf"
	vCore "github.com/InazumaV/V2bX/core"
	"github.com/InazumaV/V2bX/node"
	coreConf "github.com/xtls/xray-core/infra/conf"
)

type managedRuntime interface {
	Start() error
	Close() error
}

type runtimeConfig struct {
	cores []conf.CoreConfig
	nodes []conf.NodeConfig
}

func snapshotRuntimeConfig(c *conf.Conf) runtimeConfig {
	return runtimeConfig{cloneConfig(c.CoresConfig), cloneConfig(c.NodeConfig)}
}

// Configuration consists of acyclic data structs, maps, slices and pointers.
// Clone in memory rather than JSON: custom unmarshalling would fetch Includes,
// discard json:"-" fields, and change already-normalized configuration values.
// This runs only during startup/reload, never on the connection path.
func cloneConfig[T any](value T) T {
	return cloneConfigValue(reflect.ValueOf(value)).Interface().(T)
}

func cloneConfigValue(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		r := reflect.New(v.Type().Elem())
		r.Elem().Set(cloneConfigValue(v.Elem()))
		return r
	case reflect.Slice:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		r := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			r.Index(i).Set(cloneConfigValue(v.Index(i)))
		}
		return r
	case reflect.Map:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		r := reflect.MakeMapWithSize(v.Type(), v.Len())
		it := v.MapRange()
		for it.Next() {
			r.SetMapIndex(it.Key(), cloneConfigValue(it.Value()))
		}
		return r
	case reflect.Struct:
		r := reflect.New(v.Type()).Elem()
		for i := 0; i < v.NumField(); i++ {
			r.Field(i).Set(cloneConfigValue(v.Field(i)))
		}
		return r
	case reflect.Interface:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		r := reflect.New(v.Type()).Elem()
		r.Set(cloneConfigValue(v.Elem()))
		return r
	default:
		return v
	}
}

type serverRuntime struct {
	core   vCore.Core
	nodes  *node.Node
	config runtimeConfig
}

func newServerRuntime(c runtimeConfig) (result managedRuntime, err error) {
	// Some core builders panic for malformed routing/TLS configuration.
	// A rejected reload must not take down the currently serving instance.
	defer func() {
		if p := recover(); p != nil {
			result = nil
			err = fmt.Errorf("build core: %v", p)
		}
	}()
	c = runtimeConfig{cloneConfig(c.cores), cloneConfig(c.nodes)}
	core, err := vCore.NewCore(c.cores)
	if err != nil {
		return nil, err
	}
	return &serverRuntime{core: core, nodes: node.New(), config: c}, nil
}

func (r *serverRuntime) Start() (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("start runtime: %v", p)
		}
	}()
	if err := r.core.Start(); err != nil {
		return err
	}
	return r.nodes.Start(r.config.nodes, r.core)
}

func (r *serverRuntime) Close() error {
	r.nodes.Close()
	return r.core.Close()
}

// Validate without constructing cores: constructors install process-wide log
// handlers and asset paths, and must never run beside the serving instance.
func validateRuntimeConfig(c runtimeConfig) error {
	known := make(map[string]bool)
	for _, typ := range vCore.RegisteredCore() {
		known[typ] = true
	}
	if len(c.cores) == 0 {
		return fmt.Errorf("no cores configured")
	}
	names := make(map[string]bool)
	for _, core := range c.cores {
		if !known[core.Type] {
			return fmt.Errorf("unknown core type %q", core.Type)
		}
		name := core.Name
		if name == "" {
			name = core.Type
		}
		if names[name] {
			return fmt.Errorf("duplicate core name %q", name)
		}
		names[name] = true
		if core.Type == "xray" {
			if core.XrayConfig == nil || core.XrayConfig.ConnectionConfig == nil || core.XrayConfig.LogConfig == nil {
				return fmt.Errorf("missing Xray configuration")
			}
			x := core.XrayConfig
			if err := validateXrayFiles(x); err != nil {
				return err
			}
		}
	}
	for _, n := range c.nodes {
		if n.Options.XrayOptions != nil {
			if err := n.Options.XrayOptions.Hysteria2QUIC.Validate(); err != nil {
				return err
			}
		}
	}
	return nil
}

// Decode file shapes and routing targets before stopping listeners. Full core
// Build methods can consult process-wide asset paths and are deliberately left
// to the serialized handover; this is not complete semantic validation.
func validateXrayFiles(x *conf.XrayConfig) error {
	for _, input := range []struct {
		path   string
		target any
	}{
		{x.RouteConfigPath, &coreConf.RouterConfig{}},
		{x.InboundConfigPath, &[]coreConf.InboundDetourConfig{}},
		{x.OutboundConfigPath, &[]coreConf.OutboundDetourConfig{}},
		{x.DnsConfigPath, &coreConf.DNSConfig{}},
	} {
		if input.path == "" {
			continue
		}
		data, err := os.ReadFile(input.path)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(data, input.target); err != nil {
			return fmt.Errorf("invalid configuration in %s: %w", input.path, err)
		}
		if router, ok := input.target.(*coreConf.RouterConfig); ok {
			for i, raw := range router.RuleList {
				var rule coreConf.RouterRule
				if err := json.Unmarshal(raw, &rule); err != nil {
					return fmt.Errorf("invalid routing rule %d in %s: %w", i, input.path, err)
				}
				if rule.OutboundTag == "" && rule.BalancerTag == "" {
					return fmt.Errorf("routing rule %d in %s has no target", i, input.path)
				}
			}
		}
	}
	return nil
}

// Same-address listeners require a handover. Failed construction or startup
// reconstructs the old config; connections cannot survive that full reload.
func replaceRuntime(current managedRuntime, validate func() error, candidate, previous func() (managedRuntime, error)) (managedRuntime, bool, error) {
	if err := validate(); err != nil {
		return current, false, err
	}
	var closeErr error
	if current != nil {
		closeErr = current.Close()
	}
	next, err := candidate()
	if err == nil {
		err = next.Start()
		if err == nil {
			return next, true, closeErr
		}
		err = errors.Join(err, next.Close())
	}
	err = errors.Join(fmt.Errorf("apply new configuration: %w", err), closeErr)
	restored, restoreErr := previous()
	if restoreErr == nil {
		restoreErr = restored.Start()
		if restoreErr == nil {
			return restored, false, err
		}
		restoreErr = errors.Join(restoreErr, restored.Close())
	}
	return nil, false, errors.Join(err, fmt.Errorf("restore previous configuration: %w", restoreErr))
}
