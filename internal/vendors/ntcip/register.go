package ntcip

import (
	"errors"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
	"github.com/Vikasa2M/vikasa-collector/sdk/transport/snmp"
)

// RegisterTo registers ntcip-asc and ntcip-dms. The connection block is
//
//	connection:
//	  snmp: { address: "host:161", community: "public", version: "v1" }
//
// version is optional: ntcip-asc defaults to v2c, ntcip-dms defaults to v1.
func RegisterTo(r *adapter.Registry) {
	r.Register(ascDescriptor, func(deviceID string, conn map[string]any) (adapter.Adapter, error) {
		cfg, err := parseSNMPBlock(conn)
		if err != nil {
			return nil, err // Registry.Build adds the key; app adds the device
		}
		c, err := snmp.Dial(cfg)
		if err != nil {
			return nil, err
		}
		return NewASC(deviceID, c), nil
	})
	r.Register(dmsDescriptor, func(deviceID string, conn map[string]any) (adapter.Adapter, error) {
		cfg, err := parseSNMPBlock(conn)
		if err != nil {
			return nil, err
		}
		if cfg.Version == "" {
			cfg.Version = "v1"
		}
		c, err := snmp.Dial(cfg)
		if err != nil {
			return nil, err
		}
		return NewDMS(deviceID, c), nil
	})
}

// parseSNMPBlock is the trust boundary for the connection block, which
// config.Load passes through opaque (ADR 0014). A key it does not know, or a
// known key of the wrong type, fails boot: the lenient alternative silently
// substitutes a default — `version: 1` (a YAML int) would run as v2c.
func parseSNMPBlock(conn map[string]any) (snmp.DialConfig, error) {
	if err := adapter.OnlyKeys("connection", conn, "snmp"); err != nil {
		return snmp.DialConfig{}, err
	}
	raw, ok := conn["snmp"].(map[string]any)
	if !ok {
		return snmp.DialConfig{}, errors.New("connection.snmp block required")
	}
	if err := adapter.OnlyKeys("connection.snmp", raw, "address", "community", "version"); err != nil {
		return snmp.DialConfig{}, err
	}
	addr, err := adapter.OptString("connection.snmp", raw, "address")
	if err != nil {
		return snmp.DialConfig{}, err
	}
	if addr == "" {
		return snmp.DialConfig{}, errors.New("connection.snmp.address required")
	}
	community, err := adapter.OptString("connection.snmp", raw, "community")
	if err != nil {
		return snmp.DialConfig{}, err
	}
	if community == "" {
		community = "public"
	}
	version, err := adapter.OptString("connection.snmp", raw, "version")
	if err != nil {
		return snmp.DialConfig{}, err
	}
	if version != "" {
		if _, err := snmp.ParseVersion(version); err != nil {
			return snmp.DialConfig{}, err
		}
	}
	return snmp.DialConfig{
		Address: addr, Community: community, Version: version,
		Timeout: 2 * time.Second, Retries: 1,
	}, nil
}
