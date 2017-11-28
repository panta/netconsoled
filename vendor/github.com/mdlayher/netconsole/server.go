package netconsole

import (
	"context"
	"net"
	"os"
	"strings"
	"sync"
)

// A Server is a server that can receive and process logs generated by the
// netconsole Linux kernel module.
type Server struct {
	network string
	addr    string
	handle  func(addr net.Addr, l Log)

	// TODO(mdlayher): expose drop as a tunable externally?
	drop func(addr net.Addr, b []byte)
}

// NewServer creates a Server that listens for netconsole log messages using the
// specified network, address, and log handling function.
//
// For every Log successfully parsed from a log message, the handle function is
// invoked.  The address of the source of the log is also passed to handle.
// Logs are processed in a first-in, first-out fashion.
//
// If no handle function is specified, all logs are silently discarded.
func NewServer(network, addr string, handle func(addr net.Addr, l Log)) *Server {
	if handle == nil {
		// By default, do nothing with processed logs.
		handle = func(_ net.Addr, _ Log) {}
	}

	return &Server{
		network: network,
		addr:    addr,
		handle:  handle,
		// By default, do nothing with dropped logs.
		drop: func(_ net.Addr, _ []byte) {},
	}
}

// ListenAndServe starts the Server's netconsole log receiver.  To shut down the
// server, cancel the context passed to ListenAndServe.
func (s *Server) ListenAndServe(ctx context.Context) error {
	pc, err := net.ListenPacket(s.network, s.addr)
	if err != nil {
		return err
	}

	return s.serve(ctx, pc)
}

// serve is the internal entry point for tests.
func (s *Server) serve(ctx context.Context, pc net.PacketConn) error {
	// Make sure we clean up our goroutines appropriately.
	var wg sync.WaitGroup
	wg.Add(1)
	defer wg.Wait()

	go func() {
		defer wg.Done()

		<-ctx.Done()
		_ = pc.Close()
	}()

	b := make([]byte, os.Getpagesize())
	for {
		n, addr, err := pc.ReadFrom(b)
		if err != nil {
			// TODO(mdlayher): this is a hack. See: https://github.com/golang/go/issues/4373.
			if strings.Contains(err.Error(), "use of closed network connection") {
				return nil
			}

			return err
		}

		ll, err := ParseLog(string(b[:n]))
		if err != nil {
			s.drop(addr, b[:n])
			continue
		}

		s.handle(addr, ll)
	}
}
