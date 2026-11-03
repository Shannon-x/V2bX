package conf

import (
	"bytes"
	"encoding/json/v2"
	"os"
	"regexp"
	"strconv"
	"testing"
)

func TestPerformanceTuningPreservesOtherSettings(t *testing.T) {
	input := []byte(`{"Cores":[{"Type":"xray","XrayConnectionConfig":{"bufferSize":256,"connIdle":300,"handshake":10},"Log":{"AccessPath":"none"}},{"Type":"sing","extra":123}],"Nodes":[{"Include":"https://example.invalid/private"}],"extra":{"keep":true}}`)
	out, changes, err := TuneXrayPerformance(input, "low-memory")
	if err != nil || len(changes) != 2 {
		t.Fatalf("%v, %v", changes, err)
	}
	var root map[string]any
	if err := json.Unmarshal(out, &root); err != nil {
		t.Fatal(err)
	}
	cores := root["Cores"].([]any)
	x := cores[0].(map[string]any)
	p := x["XrayConnectionConfig"].(map[string]any)
	if p["bufferSize"] != float64(64) || p["connIdle"] != float64(120) || p["handshake"] != float64(10) {
		t.Fatalf("bad settings: %v", p)
	}
	if x["Log"].(map[string]any)["AccessPath"] != "none" || cores[1].(map[string]any)["extra"] != float64(123) || root["extra"].(map[string]any)["keep"] != true {
		t.Fatal("unrelated fields changed")
	}
	out2, changes, err := TuneXrayPerformance(out, "low-memory")
	if err != nil || len(changes) != 0 || string(out2) != string(out) {
		t.Fatal("not idempotent")
	}
}

func TestGeneratedXrayPolicyMatchesDefaults(t *testing.T) {
	c := NewXrayConfig().ConnectionConfig
	for _, script := range scripts {
		data, err := os.ReadFile(script)
		if err != nil {
			t.Fatal(err)
		}
		for key, want := range map[string]int{"bufferSize": int(c.BufferSize), "connIdle": int(c.ConnIdle), "handshake": int(c.Handshake)} {
			re := regexp.MustCompile(key + `[\\"]*:\s*(\d+)`)
			m := re.FindSubmatch(data)
			if m == nil {
				t.Fatalf("%s missing %s", script, key)
			}
			got, _ := strconv.Atoi(string(m[1]))
			if got != want {
				t.Fatalf("%s %s = %d, want %d", script, key, got, want)
			}
		}
	}
}

func TestPerformanceTuningMatchesRuntimeFieldAliases(t *testing.T) {
	input := []byte(`{"Cores":[{"tYpE":"xray","xrayconnectionconfig":{"buffersize":256,"CONNIDLE":300,"handshake":10,"custom":{"keep":true}},"unknown":9007199254740993}],"cores":{"untouched":true}}`)
	before := New()
	if err := json.Unmarshal(input, before); err != nil {
		t.Fatal(err)
	}
	if before.CoresConfig[0].XrayConfig.ConnectionConfig.BufferSize != 256 {
		t.Fatal("fixture does not match the runtime's case-insensitive loader")
	}
	out, changes, err := TuneXrayPerformance(input, "low-memory")
	if err != nil || len(changes) != 2 {
		t.Fatalf("tune: %v %v", changes, err)
	}
	path := t.TempDir() + "/config.json"
	if err := os.WriteFile(path, out, 0600); err != nil {
		t.Fatal(err)
	}
	loaded := New()
	if err := loaded.LoadFromPath(path); err != nil {
		t.Fatal(err)
	}
	settings := loaded.CoresConfig[0].XrayConfig.ConnectionConfig
	if settings.BufferSize != 64 || settings.ConnIdle != 120 || settings.Handshake != 10 {
		t.Fatalf("effective runtime policy does not match tuning: %+v", settings)
	}
	if bytes.Contains(out, []byte(`"bufferSize"`)) || bytes.Contains(out, []byte(`"XrayConnectionConfig"`)) {
		t.Fatal("tuning introduced a second spelling for an existing field")
	}
	if !bytes.Contains(out, []byte("9007199254740993")) || !bytes.Contains(out, []byte(`"custom"`)) || !bytes.Contains(out, []byte(`"untouched"`)) {
		t.Fatal("unknown settings or large integer were changed")
	}
}

func TestPerformanceTuningRejectsAmbiguousFieldAliases(t *testing.T) {
	for _, input := range []string{
		`{"Cores":[{"Type":"xray","type":"sing"}]}`,
		`{"Cores":[{"Type":"xray","XrayConnectionConfig":{},"xrayconnectionconfig":{}}]}`,
		`{"Cores":[{"Type":"xray","XrayConnectionConfig":{"bufferSize":256,"buffersize":128}}]}`,
		`{"Cores":[{"Type":"xray","XrayConnectionConfig":{"connIdle":300,"CONNIDLE":120}}]}`,
	} {
		if _, _, err := TuneXrayPerformance([]byte(input), "low-memory"); err == nil {
			t.Fatalf("accepted ambiguous config: %s", input)
		}
	}
}

func TestPerformanceTuningKeepsTopLevelCaseSensitivity(t *testing.T) {
	input := []byte(`{"cores":[{"Type":"xray"}]}`)
	loaded := New()
	if err := json.Unmarshal(input, loaded); err != nil {
		t.Fatal(err)
	}
	if len(loaded.CoresConfig) != 0 {
		t.Fatal("runtime unexpectedly accepts lowercase cores")
	}
	if _, _, err := TuneXrayPerformance(input, "low-memory"); err == nil {
		t.Fatal("tuning treated an unknown top-level field as Cores")
	}
}
