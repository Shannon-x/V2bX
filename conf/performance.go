package conf

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"io"
	"strings"

	"github.com/InazumaV/V2bX/common/json5"
)

// TuneXrayPerformance changes only explicit Xray pipe/idle settings. It does
// not load remote Includes or change protocol features, routing, or secrets.
func TuneXrayPerformance(data []byte, profile string) ([]byte, []string, error) {
	buffer := 128
	switch profile {
	case "balanced":
	case "low-memory":
		buffer = 64
	default:
		return nil, nil, fmt.Errorf("unknown profile %q (use balanced or low-memory)", profile)
	}
	clean, err := io.ReadAll(json5.NewTrimNodeReader(bytes.NewReader(data)))
	if err != nil {
		return nil, nil, err
	}
	var root map[string]jsontext.Value
	if err := json.Unmarshal(clean, &root); err != nil {
		return nil, nil, err
	}
	var cores []map[string]jsontext.Value
	if err := json.Unmarshal(root["Cores"], &cores); err != nil {
		return nil, nil, fmt.Errorf("decode Cores: %w", err)
	}
	var changes []string
	for i, core := range cores {
		_, typeRaw, err := performanceField(core, "Type")
		if err != nil {
			return nil, nil, fmt.Errorf("Cores[%d]: %w", i, err)
		}
		var typ string
		if err := json.Unmarshal(typeRaw, &typ); err != nil {
			return nil, nil, err
		}
		if typ != "xray" {
			continue
		}
		settings := make(map[string]jsontext.Value)
		settingsKey, raw, err := performanceField(core, "XrayConnectionConfig")
		if err != nil {
			return nil, nil, fmt.Errorf("Cores[%d]: %w", i, err)
		}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &settings); err != nil {
				return nil, nil, err
			}
			if settings == nil {
				settings = make(map[string]jsontext.Value)
			}
		}
		for _, setting := range []struct {
			name  string
			value int
		}{{"bufferSize", buffer}, {"connIdle", 120}} {
			key, raw, err := performanceField(settings, setting.name)
			if err != nil {
				return nil, nil, fmt.Errorf("Cores[%d].XrayConnectionConfig: %w", i, err)
			}
			var old int
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &old); err != nil {
					return nil, nil, fmt.Errorf("Cores[%d].%s: %w", i, setting.name, err)
				}
				if old == setting.value {
					continue
				}
			}
			settings[key], _ = json.Marshal(setting.value)
			changes = append(changes, fmt.Sprintf("Cores[%d].XrayConnectionConfig.%s -> %d", i, setting.name, setting.value))
		}
		core[settingsKey], _ = json.Marshal(settings, json.Deterministic(true))
	}
	if len(changes) == 0 {
		return data, nil, nil
	}
	root["Cores"], err = json.Marshal(cores, json.Deterministic(true))
	if err != nil {
		return nil, nil, err
	}
	out, err := json.Marshal(root, json.Deterministic(true), jsontext.WithIndent("  "))
	return append(out, '\n'), changes, err
}

// CoreConfig and XrayConfig use encoding/json v1, whose field matching is
// case-insensitive. Keep the existing spelling rather than introducing a second
// alias that could override the tuned value when the runtime loads the file.
// Conf itself uses json v2: the top-level Cores key remains case-sensitive.
func performanceField(object map[string]jsontext.Value, name string) (string, jsontext.Value, error) {
	key := ""
	for candidate := range object {
		if !strings.EqualFold(candidate, name) {
			continue
		}
		if key != "" {
			return "", nil, fmt.Errorf("multiple case-insensitive aliases for %s", name)
		}
		key = candidate
	}
	if key == "" {
		return name, nil, nil
	}
	return key, object[key], nil
}
