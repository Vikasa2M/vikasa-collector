package cameleon

import (
	"strings"
	"testing"
	"time"
)

// validACSConn is a minimal connection block that boots. Cases below mutate
// one field so each rejection names its own key.
func validACSConn() map[string]any {
	return map[string]any{
		"srtp":  map[string]any{"address": "203.0.113.1:18245"},
		"dsw":   map[string]any{"start": 90, "length": 110},
		"gates": []any{map[string]any{"id": "WG-1", "dsw": 101, "kind": "warning"}},
	}
}

// The connection block is opaque to config.Load, so this parser is the only
// place ADR 0014's "boot fails on the unrecognized" can be enforced for it.
func TestParseACSBlockRejectsUnrecognized(t *testing.T) {
	cases := []struct {
		name string
		mut  func(map[string]any)
		want string
	}{
		{"unknown connection key", func(c map[string]any) { c["mystery"] = true }, "mystery"},
		{"unknown srtp key", func(c map[string]any) {
			c["srtp"].(map[string]any)["community"] = "public"
		}, "community"},
		{"unknown dsw key", func(c map[string]any) {
			c["dsw"].(map[string]any)["offset"] = 1
		}, "offset"},
		{"unknown gate key", func(c map[string]any) {
			c["gates"].([]any)[0].(map[string]any)["alias"] = "x"
		}, "alias"},
		{"unknown cabinet key", func(c map[string]any) {
			c["cabinet"] = map[string]any{"lf_fault": 179, "extra": 1}
		}, "extra"},
		{"unknown plc key", func(c map[string]any) {
			c["plc"] = map[string]any{"nope": true}
		}, "nope"},
		{"address not a string", func(c map[string]any) {
			c["srtp"].(map[string]any)["address"] = 18245
		}, "address"},
		{"timeout not a string", func(c map[string]any) {
			c["srtp"].(map[string]any)["timeout"] = 5
		}, "timeout"},
		{"gate id not a string", func(c map[string]any) {
			c["gates"].([]any)[0].(map[string]any)["id"] = 1
		}, "id"},
		{"gate kind not a string", func(c map[string]any) {
			c["gates"].([]any)[0].(map[string]any)["kind"] = true
		}, "kind"},
		{"dsw.start not an int", func(c map[string]any) {
			c["dsw"].(map[string]any)["start"] = "90"
		}, "start"},
		{"dsw.length not an int", func(c map[string]any) {
			c["dsw"].(map[string]any)["length"] = "110"
		}, "length"},
		{"gate dsw not an int", func(c map[string]any) {
			c["gates"].([]any)[0].(map[string]any)["dsw"] = "101"
		}, "dsw"},
		{"cabinet lf_fault not an int", func(c map[string]any) {
			c["cabinet"] = map[string]any{"lf_fault": "179"}
		}, "lf_fault"},
		{"cabinet sp_fault not an int", func(c map[string]any) {
			c["cabinet"] = map[string]any{"sp_fault": "180"}
		}, "sp_fault"},
		{"cabinet door not an int", func(c map[string]any) {
			c["cabinet"] = map[string]any{"door": "181"}
		}, "door"},
		{"cabinet gate_estop not an int", func(c map[string]any) {
			c["cabinet"] = map[string]any{"gate_estop": "182"}
		}, "gate_estop"},
		{"missing srtp", func(c map[string]any) { delete(c, "srtp") }, "srtp"},
		{"missing dsw", func(c map[string]any) { delete(c, "dsw") }, "dsw"},
		{"missing gates", func(c map[string]any) { delete(c, "gates") }, "gates"},
		{"gate id empty", func(c map[string]any) {
			c["gates"].([]any)[0].(map[string]any)["id"] = ""
		}, "id"},
		{"gate id with space", func(c map[string]any) {
			c["gates"].([]any)[0].(map[string]any)["id"] = "WG 1"
		}, "id"},
		{"gate id with dot", func(c map[string]any) {
			c["gates"].([]any)[0].(map[string]any)["id"] = "WG.1"
		}, "id"},
		{"gate id too long", func(c map[string]any) {
			c["gates"].([]any)[0].(map[string]any)["id"] = strings.Repeat("A", 65)
		}, "id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn := validACSConn()
			tc.mut(conn)
			_, err := parseACSBlock(conn)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("parseACSBlock() error = %v, want one naming %q", err, tc.want)
			}
		})
	}
}

func TestParseACSBlockRejectsUnknownConnectionKey(t *testing.T) {
	conn := validACSConn()
	conn["timeout"] = "5s"
	_, err := parseACSBlock(conn)
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("parseACSBlock() error = %v, want one naming %q", err, "timeout")
	}
}

func TestParseACSBlockDefaults(t *testing.T) {
	cfg, err := parseACSBlock(validACSConn())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.timeout != 5*time.Second {
		t.Fatalf("timeout = %v, want 5s default", cfg.timeout)
	}
	if cfg.plc.watchdogStall != 5*time.Second {
		t.Fatalf("watchdogStall = %v, want 5s default", cfg.plc.watchdogStall)
	}
}
