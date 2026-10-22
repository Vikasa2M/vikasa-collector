package cameleon

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// RegisterTo registers cameleon-acs. The connection block is
//
//	connection:
//	  srtp: { address: "203.0.113.10:18245", timeout: "5s" }   # timeout optional
//	  dsw:  { start: 90, length: 110 }
//	  gates:
//	    - { id: "WG-1", dsw: 101, kind: "warning" }
//	  cabinet:                                         # optional
//	    lf_fault: 179
//	    sp_fault: 180
//	    door: 181
//	    gate_estop: 182
//	  plc:                                             # optional
//	    cabinet_id: 1                                  # fault if %R90 bits 1–6 differ
//	    program_crc: "0x00000000"                      # fault while live %R91/%R92 differ
//	    config_crc: "0x00000000"                       # fault while live %R93/%R94 differ
//	    watchdog_stall: "5s"                           # default 5s; 0 disables plc/not-scanning
//
// %R90–%R94 are the PLC status words when they fall inside dsw. Only bit 0 of
// each cabinet word is the input. %R95–%R99 (user name) are not read. %R183
// and every DCW address are never written.
func RegisterTo(r *adapter.Registry) {
	r.Register(acsDescriptor, func(deviceID string, conn map[string]any) (adapter.Adapter, error) {
		cfg, err := parseACSBlock(conn)
		if err != nil {
			return nil, fmt.Errorf("cameleon-acs %s: %w", deviceID, err)
		}
		// Lazy dial: unreachable cabinets must not fail collector boot.
		return newACSLazy(deviceID, cfg.addr, cfg.timeout, cfg.dswStart, cfg.dswLen, cfg.gates, cfg.cabinet, cfg.plc), nil
	})
}

type acsConn struct {
	addr             string
	timeout          time.Duration
	dswStart, dswLen int
	gates            []gateCfg
	cabinet          cabinetCfg
	plc              plcCfg
}

// gateIDPattern matches openits-reversible-lane-types:gate-id (length 1..64,
// pattern [a-zA-Z0-9_-]+) in the pinned openits-models v0.8.0 YANG.
var gateIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// parseACSBlock is the trust boundary for the ACS connection block, which
// config.Load passes through opaque (ADR 0014). A key it does not know, or a
// known key of the wrong type, fails boot.
func parseACSBlock(conn map[string]any) (acsConn, error) {
	var out acsConn
	if err := adapter.OnlyKeys("connection", conn, "srtp", "dsw", "gates", "cabinet", "plc"); err != nil {
		return out, err
	}

	rawSRTP, ok := conn["srtp"].(map[string]any)
	if !ok {
		return out, fmt.Errorf("connection.srtp block required")
	}
	if err := adapter.OnlyKeys("connection.srtp", rawSRTP, "address", "timeout"); err != nil {
		return out, err
	}
	addr, err := adapter.OptString("connection.srtp", rawSRTP, "address")
	if err != nil {
		return out, err
	}
	if addr == "" {
		return out, fmt.Errorf("connection.srtp.address required")
	}
	if err := validateSRTPAddress(addr); err != nil {
		return out, err
	}
	out.addr = addr
	out.timeout = 5 * time.Second
	timeout, err := adapter.OptString("connection.srtp", rawSRTP, "timeout")
	if err != nil {
		return out, err
	}
	if timeout != "" {
		d, err := time.ParseDuration(timeout)
		if err != nil {
			return out, fmt.Errorf("connection.srtp.timeout: %w", err)
		}
		if d <= 0 {
			return out, fmt.Errorf("connection.srtp.timeout must be > 0")
		}
		out.timeout = d
	}

	rawDSW, ok := conn["dsw"].(map[string]any)
	if !ok {
		return out, fmt.Errorf("connection.dsw block required")
	}
	if err := adapter.OnlyKeys("connection.dsw", rawDSW, "start", "length"); err != nil {
		return out, err
	}
	start, err := requireInt("connection.dsw", rawDSW, "start")
	if err != nil {
		return out, err
	}
	if start < 1 || start > 65535 {
		return out, fmt.Errorf("connection.dsw.start must be an integer 1..65535")
	}
	length, err := requireInt("connection.dsw", rawDSW, "length")
	if err != nil {
		return out, err
	}
	if length < 1 || length > 512 {
		return out, fmt.Errorf("connection.dsw.length must be an integer 1..512")
	}
	if start+length-1 > 65535 {
		return out, fmt.Errorf("connection.dsw.start+length-1 must be <= 65535")
	}
	out.dswStart, out.dswLen = start, length
	out.plc.watchdogStall = 5 * time.Second

	rawGates, ok := conn["gates"].([]any)
	if !ok || len(rawGates) == 0 {
		return out, fmt.Errorf("connection.gates must list at least one gate")
	}
	seen := map[string]bool{}
	for i, raw := range rawGates {
		path := fmt.Sprintf("connection.gates[%d]", i)
		m, ok := raw.(map[string]any)
		if !ok {
			return out, fmt.Errorf("%s: must be a map", path)
		}
		if err := adapter.OnlyKeys(path, m, "id", "dsw", "kind"); err != nil {
			return out, err
		}
		id, err := adapter.OptString(path, m, "id")
		if err != nil {
			return out, err
		}
		if id == "" {
			return out, fmt.Errorf("%s.id required", path)
		}
		if len(id) > 64 || !gateIDPattern.MatchString(id) {
			return out, fmt.Errorf("%s.id %q: want 1..64 chars matching [a-zA-Z0-9_-]+", path, id)
		}
		if seen[id] {
			return out, fmt.Errorf("connection.gates: duplicate id %q", id)
		}
		seen[id] = true
		dsw, err := requireInt(path, m, "dsw")
		if err != nil {
			return out, err
		}
		if dsw < start || dsw >= start+length {
			return out, fmt.Errorf("%s.dsw must fall inside the DSW block", path)
		}
		kindStr, err := adapter.OptString(path, m, "kind")
		if err != nil {
			return out, err
		}
		kind, err := parseGateKind(kindStr)
		if err != nil {
			return out, fmt.Errorf("%s.kind: %w", path, err)
		}
		out.gates = append(out.gates, gateCfg{id: id, kind: kind, dsw: dsw})
	}

	if rawCab, ok := conn["cabinet"]; ok {
		block, ok := rawCab.(map[string]any)
		if !ok {
			return out, fmt.Errorf("connection.cabinet must be a map")
		}
		if err := adapter.OnlyKeys("connection.cabinet", block, "lf_fault", "sp_fault", "door", "gate_estop"); err != nil {
			return out, err
		}
		out.cabinet.lfFault, err = optionalAddr(block, "lf_fault", start, length)
		if err != nil {
			return out, err
		}
		out.cabinet.spFault, err = optionalAddr(block, "sp_fault", start, length)
		if err != nil {
			return out, err
		}
		out.cabinet.door, err = optionalAddr(block, "door", start, length)
		if err != nil {
			return out, err
		}
		out.cabinet.gateEstop, err = optionalAddr(block, "gate_estop", start, length)
		if err != nil {
			return out, err
		}
	}

	if rawPLC, ok := conn["plc"]; ok {
		block, ok := rawPLC.(map[string]any)
		if !ok {
			return out, fmt.Errorf("connection.plc must be a map")
		}
		if err := parsePLCBlock(&out, block); err != nil {
			return out, err
		}
	}
	return out, nil
}

func parsePLCBlock(out *acsConn, m map[string]any) error {
	if out.dswStart > 90 || 94 >= out.dswStart+out.dswLen {
		return fmt.Errorf("connection.plc requires %%R90-%%R94 inside the DSW block")
	}
	if err := adapter.OnlyKeys("connection.plc", m, "cabinet_id", "program_crc", "config_crc", "watchdog_stall"); err != nil {
		return err
	}
	if _, ok := m["cabinet_id"]; ok {
		id, err := requireInt("connection.plc", m, "cabinet_id")
		if err != nil {
			return err
		}
		if id < 1 || id > 63 {
			return fmt.Errorf("connection.plc.cabinet_id must be an integer 1..63")
		}
		out.plc.hasCabinetID = true
		out.plc.cabinetID = id
	}
	if _, ok := m["program_crc"]; ok {
		s, err := adapter.OptString("connection.plc", m, "program_crc")
		if err != nil {
			return err
		}
		crc, err := parseCRC(s)
		if err != nil {
			return fmt.Errorf("connection.plc.program_crc: %w", err)
		}
		out.plc.hasProgramCRC = true
		out.plc.programCRC = crc
	}
	if _, ok := m["config_crc"]; ok {
		s, err := adapter.OptString("connection.plc", m, "config_crc")
		if err != nil {
			return err
		}
		crc, err := parseCRC(s)
		if err != nil {
			return fmt.Errorf("connection.plc.config_crc: %w", err)
		}
		out.plc.hasConfigCRC = true
		out.plc.configCRC = crc
	}
	if _, ok := m["watchdog_stall"]; ok {
		s, err := adapter.OptString("connection.plc", m, "watchdog_stall")
		if err != nil {
			return err
		}
		d, err := time.ParseDuration(s)
		if err != nil || d < 0 {
			return fmt.Errorf("connection.plc.watchdog_stall must be a duration >= 0")
		}
		out.plc.watchdogStall = d
	}
	return nil
}

// validateSRTPAddress requires host:port with a non-empty host, no whitespace,
// and a TCP port in 1..65535.
func validateSRTPAddress(addr string) error {
	if strings.ContainsAny(addr, " \t\r\n") {
		return fmt.Errorf("connection.srtp.address must not contain whitespace")
	}
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("connection.srtp.address: %w", err)
	}
	if host == "" {
		return fmt.Errorf("connection.srtp.address: host required")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("connection.srtp.address: port must be 1..65535")
	}
	return nil
}

func parseCRC(s string) (uint32, error) {
	s = strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X")
	if s == "" || len(s) > 8 {
		return 0, fmt.Errorf("must be a 32-bit hex value")
	}
	n, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 0, fmt.Errorf("must be a 32-bit hex value")
	}
	return uint32(n), nil
}

func optionalAddr(m map[string]any, key string, start, length int) (int, error) {
	if _, ok := m[key]; !ok {
		return 0, nil
	}
	v, err := requireInt("connection.cabinet", m, key)
	if err != nil {
		return 0, err
	}
	if v < start || v >= start+length {
		return 0, fmt.Errorf("connection.cabinet.%s must fall inside the DSW block", key)
	}
	return v, nil
}

func parseGateKind(s string) (model.GateKind, error) {
	switch s {
	case "warning":
		return model.GateKindWarning, nil
	case "barrier":
		return model.GateKindBarrier, nil
	case "gate":
		// ACS Cameleon exports sometimes use bare "gate" instead of warning/barrier.
		return model.GateKindUnknown, nil
	case "unknown":
		return model.GateKindUnknown, nil
	case "":
		return 0, fmt.Errorf("required")
	default:
		return 0, fmt.Errorf("%q: want warning|barrier|gate", s)
	}
}

// requireInt reads a required integer key. YAML ints arrive as int, int64 or
// float64; any other type is an error that names the key.
func requireInt(path string, m map[string]any, key string) (int, error) {
	v, ok := m[key]
	if !ok {
		return 0, fmt.Errorf("%s.%s required", path, key)
	}
	n, err := asInt(v)
	if err != nil {
		return 0, fmt.Errorf("%s.%s must be an integer, got %T", path, key, v)
	}
	return n, nil
}

// asInt accepts YAML ints decoded as int or (from some loaders) int64/float64.
func asInt(v any) (int, error) {
	switch n := v.(type) {
	case int:
		return n, nil
	case int64:
		return int(n), nil
	case float64:
		if n != float64(int(n)) {
			return 0, fmt.Errorf("not an integer: %v", v)
		}
		return int(n), nil
	case uint64:
		return int(n), nil
	default:
		return 0, fmt.Errorf("not an integer: %T", v)
	}
}
