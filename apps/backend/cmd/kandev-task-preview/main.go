// kandev-task-preview is a host-owned TLS ingress for one reviewed grant.
package main

import (
	"context"
	"flag"
	"github.com/kandev/kandev/internal/taskpreview"
	"log"
	"net/http"
	"time"
)

func main() {
	state := flag.String("state-dir", "", "private operator state directory")
	listen := flag.String("listen", "", "exact approved LAN IP:port")
	cert := flag.String("tls-cert", "", "trusted TLS certificate path")
	key := flag.String("tls-key", "", "TLS private key path")
	flag.Parse()
	if *state == "" || *listen == "" || *cert == "" || *key == "" {
		log.Fatal("state, listener and trusted TLS required")
	}
	store := taskpreview.Store{Directory: *state}
	grant, _, err := store.Load()
	if err != nil || !grant.Active(time.Now()) || *listen != grant.ListenAddress {
		log.Fatal("preview grant unavailable")
	}
	// Bounded direct control traffic; no ambient HTTP proxy credentials or
	// unbounded HTTP/2 streams into the production authorization bridge.
	transport := &http.Transport{MaxConnsPerHost: 4, MaxIdleConnsPerHost: 4, ResponseHeaderTimeout: 5 * time.Second}
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	gateway := &taskpreview.Gateway{Store: store, Client: client, Now: time.Now}
	server := &http.Server{Addr: *listen, Handler: gateway, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16384}
	go func() {
		timer := time.NewTimer(time.Until(time.Unix(grant.ExpiresAt, 0)))
		defer timer.Stop()
		<-timer.C
		// Request/stream deadlines and the backend's clock enforce expiry.
		// The gateway cannot modify its read-only authority or revoke marker.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()
	// No access logging; request headers/bodies/tokens are never logged.
	if err := server.ListenAndServeTLS(*cert, *key); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
