// Package manage is the collector's local management surface
// (docs/specs/2026-08-09-management-surface-design.md §1): an HTTP listener
// on loopback with three read-only endpoints.
//
//   - /readyz — has this version proven itself? Booted, config accepted, and
//     at least one successful device poll. It latches, and it does not depend
//     on the broker; a host supervisor gates rollback on it (ADR 0012).
//   - /livez — is the process responsive? Deliberately weak and unlatched:
//     for restarts, never for judging a rollout.
//   - /status — what is going on in this cabinet, for a technician on site.
package manage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/Vikasa2M/vikasa-collector/internal/stats"
)

// Source is what the surface reports on.
type Source interface {
	Ready() bool
	Status() Status
}

// Status is the /status document.
type Status struct {
	Version        string               `json:"version"`
	ConfigRevision string               `json:"config_revision"`
	BootID         string               `json:"boot_id"`
	StartedAt      time.Time            `json:"started_at"`
	UptimeSeconds  int64                `json:"uptime_s"`
	Ready          bool                 `json:"ready"`
	Broker         Broker               `json:"broker"`
	Counters       stats.Snapshot       `json:"counters"`
	Devices        []stats.DeviceStatus `json:"devices"`
}

// Broker is the publisher's view of the local broker.
type Broker struct {
	Connected bool `json:"connected"`
	Queued    int  `json:"queued"`
}

// Handler serves the three endpoints. Every route is GET-only.
func Handler(src Source) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if !src.Ready() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("not ready: no device has been polled successfully yet\n"))
			return
		}
		_, _ = w.Write([]byte("ready\n"))
	})
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(src.Status())
	})
	return mux
}

// ValidateAddr accepts "" (the surface disabled) or a host:port whose host
// is loopback. The surface is unauthenticated and carries device errors and
// identity, so it is refused on any other interface at boot rather than
// exposed to the cabinet network.
func ValidateAddr(addr string) error {
	if addr == "" {
		return nil
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("status address %q: %w", addr, err)
	}
	if p, err := strconv.Atoi(port); err != nil || p < 1 || p > 65535 {
		return fmt.Errorf("status address %q: port must be 1-65535", addr)
	}
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("status address %q: must be loopback (127.0.0.1, ::1 or localhost); the surface is unauthenticated", addr)
}

// Server is a running management surface.
type Server struct {
	srv  *http.Server
	done chan struct{}
}

// Start binds addr (which must already pass ValidateAddr) and serves in the
// background. A bind failure — the port in use, say — is returned, so it
// fails boot instead of leaving a supervisor polling a surface that is not
// there.
func Start(ctx context.Context, addr string, src Source, log *slog.Logger) (*Server, error) {
	l, err := new(net.ListenConfig).Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("status listener: %w", err)
	}
	s := &Server{
		srv: &http.Server{
			Handler:           Handler(src),
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       10 * time.Second,
			WriteTimeout:      10 * time.Second,
			IdleTimeout:       60 * time.Second,
		},
		done: make(chan struct{}),
	}
	go func() {
		defer close(s.done)
		if err := s.srv.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("status listener stopped", "err", err)
		}
	}()
	log.Info("status surface listening", "addr", l.Addr().String())
	return s, nil
}

// Shutdown stops the listener, waiting for in-flight requests until ctx ends.
func (s *Server) Shutdown(ctx context.Context) {
	_ = s.srv.Shutdown(ctx)
	<-s.done
}
