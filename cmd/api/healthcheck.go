package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"
)

// healthcheckArg turns the binary into its own health probe.
const healthcheckArg = "-healthcheck"

// healthcheckTimeout is short: this runs every few seconds and a slow answer
// is a failed answer as far as the orchestrator is concerned.
const healthcheckTimeout = 3 * time.Second

// runHealthcheck asks the running server whether it is up and returns a
// process exit code.
//
// It exists because the runtime image is FROM scratch (spec §12.6): there is
// no curl, no wget and no shell for Docker's HEALTHCHECK to invoke, so the
// only executable available to probe the server is the server itself.
func runHealthcheck() int {
	ctx, cancel := context.WithTimeout(context.Background(), healthcheckTimeout)
	defer cancel()

	url := "http://" + net.JoinHostPort("127.0.0.1", healthcheckPort()) + "/healthz"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck: building request: %v\n", err)
		return 1
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck: %v\n", err)
		return 1
	}
	// The process is about to exit either way; there is nothing to recover
	// from and nothing to leak.
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "healthcheck: %s returned %d\n", url, res.StatusCode)
		return 1
	}

	return 0
}

// healthcheckPort reads the port the server listens on. ADDR is a listen
// address, so it may name a host or a wildcard ("0.0.0.0:8080", ":8080") —
// only the port is useful for dialling back in over loopback.
func healthcheckPort() string {
	addr := envOr("ADDR", defaultAddr)

	_, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		_, port, err = net.SplitHostPort(defaultAddr)
		if err != nil {
			return "8080"
		}
	}

	return port
}
