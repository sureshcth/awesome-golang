package loadbalancer

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	// Parse command line flags
	var (
		port             = flag.Int("port", 8080, "Port to serve on")
		algorithm        = flag.String("algorithm", "round-robin", "Load balancing algorithm (round-robin, least-connections, ip-hash)")
		healthCheckPath  = flag.String("health-path", "/health", "Path to use for health checks")
		healthCheckFreq  = flag.Duration("health-freq", 10*time.Second, "Frequency of health checks")
		maxRetries       = flag.Int("max-retries", 3, "Maximum retries for failed requests")
		serverTimeout    = flag.Duration("timeout", 30*time.Second, "Backend server timeout")
		backendAddresses = flag.String("backends", "http://localhost:8081,http://localhost:8082,http://localhost:8083", "Comma-separated list of backend addresses")
	)
	flag.Parse()

	// Create a new load balancer
	lb := NewLoadbalancer(
		*algorithm,
		*healthCheckPath,
		*healthCheckFreq,
		*maxRetries,
		*serverTimeout,
	)

	// Parse and add backend servers
	for _, serverURL := range parseBackendAddresses(*backendAddresses) {
		if err := lb.AddBackend(serverURL); err != nil {
			log.Fatalf("Error adding backend %s: %v", serverURL, err)
		}
		log.Printf("Added backend: %s", serverURL)
	}

	// Create server
	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", *port),
		Handler: lb,
	}

	// Start server in a goroutine
	go func() {
		log.Printf("Load balancer starting on port %d with %s algorithm", *port, *algorithm)
		if err := server.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatalf("Error starting server: %v", err)
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	// Graceful shutdown
	log.Println("Shutting down load balancer...")
	lb.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("Server shutdown failed: %v", err)
	}
	log.Println("Load balancer stopped")
}

// parseBackendAddresses parses a comma-separated list of backend addresses
func parseBackendAddresses(addresses string) []string {
	backends := []string{}
	for _, addr := range flag.Args() {
		backends = append(backends, addr)
	}
	return backends
}
