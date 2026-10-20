package adapter

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// The connection block reaches a Factory opaque: config.Load checks only
// that vendor and device_kind resolve (ADR 0014), so the Factory is the
// trust boundary for everything inside it. These helpers make the strict
// reading the easy one. The lenient alternative — `addr, _ :=
// m["address"].(string)` — turns a wrong-typed value into "absent", and an
// absent value into a default the operator never chose.

// OnlyKeys rejects any key in m outside allowed. path names the block in
// errors ("connection.snmp"). The first offender in sorted order is named, so
// the error is the same on every run.
func OnlyKeys(path string, m map[string]any, allowed ...string) error {
	for _, k := range slices.Sorted(maps.Keys(m)) {
		if !slices.Contains(allowed, k) {
			return fmt.Errorf("%s.%s: unknown key (want one of %s)", path, k, strings.Join(allowed, ", "))
		}
	}
	return nil
}

// OptString reads an optional string key from the block at path: absent is
// "", and present but not a string is an error, never a silent default.
func OptString(path string, m map[string]any, key string) (string, error) {
	v, ok := m[key]
	if !ok {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%s.%s must be a string, got %T", path, key, v)
	}
	return s, nil
}
