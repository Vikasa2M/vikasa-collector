package adapter

import (
	"strings"
	"testing"
)

func TestOnlyKeys(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		m       map[string]any
		wantErr string // "" means accepted
	}{
		{"all known", map[string]any{"address": "h:161", "community": "c"}, ""},
		{"empty", map[string]any{}, ""},
		{"one unknown", map[string]any{"address": "h:161", "comunity": "c"}, "connection.snmp.comunity: unknown key (want one of address, community)"},
		// Sorted, so the error names the same key on every run.
		{"first unknown in order", map[string]any{"zz": 1, "aa": 1}, "connection.snmp.aa: unknown key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := OnlyKeys("connection.snmp", tc.m, "address", "community")
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("OnlyKeys: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("OnlyKeys err = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestOptString(t *testing.T) {
	t.Parallel()
	m := map[string]any{"address": "h:161", "version": 1}

	if got, err := OptString("connection.snmp", m, "address"); err != nil || got != "h:161" {
		t.Errorf("present string = %q, %v", got, err)
	}
	if got, err := OptString("connection.snmp", m, "community"); err != nil || got != "" {
		t.Errorf("absent = %q, %v; want \"\", nil", got, err)
	}
	// A YAML int where a string belongs is a misconfiguration, never a
	// silent fallback to the default (ADR 0014).
	_, err := OptString("connection.snmp", m, "version")
	if want := "connection.snmp.version must be a string, got int"; err == nil || err.Error() != want {
		t.Errorf("wrong type err = %v, want %q", err, want)
	}
}
