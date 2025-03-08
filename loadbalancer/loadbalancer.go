package loadbalancer

import (
	"errors"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"sync/atomic"
	"time"
)

// Backend represents a server that the load balancer can forward requests to
type Backend struct {
	URL          *url.URL
	Alive        bool
	ReverseProxy *httputil.ReverseProxy
	FailCount    int
	LastChecked  time.Time
	mu           sync.RWMutex
}

// SetAlive updates the alive status of the backend server
func (b *Backend) SetAlive(alive bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.Alive = alive
	if alive {
		b.FailCount = 0
	} else {
		b.FailCount++
	}
	b.LastChecked = time.Now()
}

// IsAlive returns true if the backend is currently marked as alive
func (b *Backend) IsAlive() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.Alive
}

// LoadBalancer represents a load balancer for distributing requests across backends
type LoadBalancer struct {
	backends        []*Backend
	current         uint64
	healthCheckPath string
	healthCheckFreq time.Duration
	algorithm       string
	maxRetries      int
	serverTimeout   time.Duration
	mu              sync.RWMutex
	healthCheckStop chan struct{}
}

// Algorithm constants
const (
	AlgorithmRoundRobin = "round-robin"
	AlgorithmLeastConn  = "least-connections"
	AlgorithmIPHash     = "ip-hash"
)

// New creates a new load balancer with the specified configuration
func New(algorithm string, healthCheckPath string, healthCheckFreq time.Duration, maxRetries int, serverTimeout time.Duration) *LoadBalancer {
	lb := &LoadBalancer{
		backends:        make([]*Backend, 0),
		current:         0,
		healthCheckPath: healthCheckPath,
		healthCheckFreq: healthCheckFreq,
		algorithm:       algorithm,
		maxRetries:      maxRetries,
		serverTimeout:   serverTimeout,
		healthCheckStop: make(chan struct{}),
	}

	// Start health checking if a frequency is provided
	if healthCheckFreq > 0 {
		go lb.healthCheck()
	}

	return lb
}

// AddBackend adds a new backend server to the load balancer
func (lb *LoadBalancer) AddBackend(serverURL string) error {
	url, err := url.Parse(serverURL)
	if err != nil {
		return err
	}

	proxy := httputil.NewSingleHostReverseProxy(url)

	// Configure proxy error handling
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		// Mark the backend as down
		for _, b := range lb.backends {
			if b.URL.String() == url.String() {
				b.SetAlive(false)
				break
			}
		}

		// Try a different backend
		retries := getRetryCount(r)
		if retries < lb.maxRetries {
			r.Header.Set("X-Retry-Count", string(rune(retries+1)))
			lb.ServeHTTP(w, r)
			return
		}

		// If we've exhausted retries, return an error
		http.Error(w, "Service unavailable", http.StatusServiceUnavailable)
	}

	// Add connection and response timeouts
	proxy.Transport = &http.Transport{
		ResponseHeaderTimeout: lb.serverTimeout,
	}

	backend := &Backend{
		URL:          url,
		Alive:        true,
		ReverseProxy: proxy,
		FailCount:    0,
		LastChecked:  time.Now(),
	}

	lb.mu.Lock()
	defer lb.mu.Unlock()
	lb.backends = append(lb.backends, backend)

	return nil
}

// RemoveBackend removes a backend server from the load balancer
func (lb *LoadBalancer) RemoveBackend(serverURL string) bool {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	for i, backend := range lb.backends {
		if backend.URL.String() == serverURL {
			lb.backends = append(lb.backends[:i], lb.backends[i+1:]...)
			return true
		}
	}
	return false
}

// NextBackend returns the next available backend based on the configured algorithm
func (lb *LoadBalancer) NextBackend(r *http.Request) (*Backend, error) {
	lb.mu.RLock()
	defer lb.mu.RUnlock()

	if len(lb.backends) == 0 {
		return nil, errors.New("no backends available")
	}

	// Check if any backends are alive
	aliveCount := 0
	for _, b := range lb.backends {
		if b.IsAlive() {
			aliveCount++
		}
	}

	if aliveCount == 0 {
		return nil, errors.New("all backends are down")
	}

	switch lb.algorithm {
	case AlgorithmRoundRobin:
		return lb.roundRobin()
	case AlgorithmLeastConn:
		return lb.leastConnections()
	case AlgorithmIPHash:
		return lb.ipHash(r)
	default:
		return lb.roundRobin()
	}
}

// roundRobin implements the round-robin algorithm for backend selection
func (lb *LoadBalancer) roundRobin() (*Backend, error) {
	next := atomic.AddUint64(&lb.current, 1) % uint64(len(lb.backends))

	// Try each backend in turn
	for i := 0; i < len(lb.backends); i++ {
		idx := (next + uint64(i)) % uint64(len(lb.backends))
		if lb.backends[idx].IsAlive() {
			return lb.backends[idx], nil
		}
	}

	return nil, errors.New("no backends available")
}

// leastConnections selects the backend with the fewest active connections
// This is a simplified implementation for study purposes
func (lb *LoadBalancer) leastConnections() (*Backend, error) {
	var selectedBackend *Backend
	lowestLoad := int(^uint(0) >> 1) // Max int value

	for _, b := range lb.backends {
		if !b.IsAlive() {
			continue
		}

		// In a real implementation, you would track active connections
		// For this example, we're just using FailCount as a proxy for load
		// (lower is better)
		if b.FailCount < lowestLoad {
			lowestLoad = b.FailCount
			selectedBackend = b
		}
	}

	if selectedBackend == nil {
		return nil, errors.New("no backends available")
	}

	return selectedBackend, nil
}

// ipHash routes requests from the same IP to the same backend
func (lb *LoadBalancer) ipHash(r *http.Request) (*Backend, error) {
	clientIP := getClientIP(r)

	// Simple hash of the IP address
	var hash uint64
	for i := 0; i < len(clientIP); i++ {
		hash = hash*31 + uint64(clientIP[i])
	}

	// Try the hashed backend first
	idx := hash % uint64(len(lb.backends))
	if lb.backends[idx].IsAlive() {
		return lb.backends[idx], nil
	}

	// Fall back to round-robin if the preferred backend is down
	return lb.roundRobin()
}

// ServeHTTP handles the HTTP requests and routes them to the appropriate backend
func (lb *LoadBalancer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	backend, err := lb.NextBackend(r)
	if err != nil {
		http.Error(w, "Service unavailable", http.StatusServiceUnavailable)
		return
	}

	// Add headers for tracking and debugging
	r.Header.Add("X-Forwarded-For", getClientIP(r))
	r.Header.Add("X-Proxy-Time", time.Now().Format(time.RFC3339))

	// Forward the request to the selected backend
	backend.ReverseProxy.ServeHTTP(w, r)
}

// healthCheck runs periodic health checks on all backends
func (lb *LoadBalancer) healthCheck() {
	ticker := time.NewTicker(lb.healthCheckFreq)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			lb.checkBackendHealth()
		case <-lb.healthCheckStop:
			return
		}
	}
}

// checkBackendHealth checks the health of all backend servers
func (lb *LoadBalancer) checkBackendHealth() {
	lb.mu.RLock()
	backends := make([]*Backend, len(lb.backends))
	copy(backends, lb.backends)
	lb.mu.RUnlock()

	for _, b := range backends {
		go func(backend *Backend) {
			// Create a health check request with a timeout
			client := &http.Client{
				Timeout: 5 * time.Second,
			}

			resp, err := client.Get(backend.URL.String() + lb.healthCheckPath)
			if err != nil {
				backend.SetAlive(false)
				return
			}
			defer resp.Body.Close()

			// Consider the backend alive if response status is 2xx
			backend.SetAlive(resp.StatusCode >= 200 && resp.StatusCode < 300)
		}(b)
	}
}

// Close stops the load balancer's health checking goroutine
func (lb *LoadBalancer) Close() {
	close(lb.healthCheckStop)
}

// GetBackends returns a copy of the current backends list for inspection
func (lb *LoadBalancer) GetBackends() []*Backend {
	lb.mu.RLock()
	defer lb.mu.RUnlock()

	backends := make([]*Backend, len(lb.backends))
	copy(backends, lb.backends)
	return backends
}

// Helpers

// getClientIP extracts the client IP from the request
func getClientIP(r *http.Request) string {
	// Try standard headers
	ip := r.Header.Get("X-Forwarded-For")
	if ip == "" {
		ip = r.Header.Get("X-Real-IP")
	}
	if ip == "" {
		ip = r.RemoteAddr
	}
	return ip
}

// getRetryCount gets the current retry count from request headers
func getRetryCount(r *http.Request) int {
	retries := r.Header.Get("X-Retry-Count")
	if retries == "" {
		return 0
	}
	return int(retries[0])
}
