// Command 'receiver' provides a server that can receive logs relayed by netconsoled via TCP/TLS
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"sync"
)

func main() {
	const defaultConfig = "netconsoled.yml"

	var (
		config = flag.String("c", defaultConfig, "location of netconsoled's configuration file")
	)

	flag.Parse()
	ll := log.New(os.Stderr, "", log.Ldate|log.Ltime)

	// Read and validate configuration.
	ll.Printf("starting netconsoled tcp 'receiver' with configuration file %q", *config)

	cfg, err := parseConfig(ll, *config)
	if err != nil {
		ll.Fatalf("failed to initialize: %v", err)
	}

	// Notify goroutines of halt signal by canceling this context when a
	// signal is received.
	ctx, cancel := context.WithCancel(context.Background())

	sigC := make(chan os.Signal, 1)
	// TODO(mdlayher): portable version of syscall.SIGTERM if possible.
	signal.Notify(sigC, os.Interrupt)

	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()

		sig := <-sigC
		ll.Printf("caught signal %q, stopping", sig.String())
		cancel()
	}()

	serve(ctx, ll, cfg)

	wg.Wait()

	ll.Println("stopped netconsoled")
}
