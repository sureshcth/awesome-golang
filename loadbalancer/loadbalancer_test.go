package loadbalancer

import (
	//"fmt"
	"net/http"
	"net/http/httptest"

	//"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// Helper function to create a simple test backend server
func createTestServer(t *testing.T, response string, status int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("OK"))
			return
		}

		w.WriteHeader(status)
		w.Write([]byte(response))
	}))
}

// Helper function to create a failing test server
func createFailingServer(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Server Error"))
	}))
}

// TestAddBackend tests adding a backend to the load balancer
func TestAddBackend(t *testing.T) {
	lb := NewLoadbalancer(AlgorithmRoundRobin, "/health", 0, 3, 30*time.Second)

	// Test with valid URL
	err := lb.AddBackend("http://localhost:8080")
	if err != nil {
		t.Errorf("Failed to add valid backend: %v", err)
	}

	// Test with invalid URL
	err = lb.AddBackend("://invalid-url")
	if err == nil {
		t.Error("Expected error when adding invalid backend, got nil")
	}

	// Check the number of backends
	if len(lb.GetBackends()) != 1 {
		t.Errorf("Expected 1 backend, got %d", len(lb.GetBackends()))
	}
}

// TestRemoveBackend tests removing a backend from the load balancer
func TestRemoveBackend(t *testing.T) {
	lb := NewLoadbalancer(AlgorithmRoundRobin, "/health", 0, 3, 30*time.Second)

	// Add a backend
	backendURL := "http://localhost:8080"
	lb.AddBackend(backendURL)

	// Remove it
	success := lb.RemoveBackend(backendURL)
	if !success {
		t.Error("Failed to remove existing backend")
	}

	// Try to remove a non-existent backend
	success = lb.RemoveBackend("http://nonexistent:9999")
	if success {
		t.Error("Removing non-existent backend should return false")
	}

	// Check that we have 0 backends
	if len(lb.GetBackends()) != 0 {
		t.Errorf("Expected 0 backends, got %d", len(lb.GetBackends()))
	}
}

// TestRoundRobinAlgorithm tests the round-robin backend selection algorithm
func TestRoundRobinAlgorithm(t *testing.T) {
	lb := NewLoadbalancer(AlgorithmRoundRobin, "/health", 0, 3, 30*time.Second)

	// Create 3 test servers
	server1 := createTestServer(t, "Server 1", http.StatusOK)
	defer server1.Close()

	server2 := createTestServer(t, "Server 2", http.StatusOK)
	defer server2.Close()

	server3 := createTestServer(t, "Server 3", http.StatusOK)
	defer server3.Close()

	// Add them to the load balancer
	lb.AddBackend(server1.URL)
	lb.AddBackend(server2.URL)
	lb.AddBackend(server3.URL)

	// Create a dummy request for testing
	req, _ := http.NewRequest("GET", "/", nil)

	// Get each backend in turn and verify they follow round-robin order
	for i := 0; i < 9; i++ {
		backend, err := lb.NextBackend(req)
		if err != nil {
			t.Fatalf("Error getting next backend: %v", err)
		}

		expectedURL := ""
		switch i % 3 {
		case 0:
			expectedURL = server1.URL
		case 1:
			expectedURL = server2.URL
		case 2:
			expectedURL = server3.URL
		}

		if backend.URL.String() != expectedURL {
			t.Errorf("Iteration %d: Expected backend %s, got %s", i, expectedURL, backend.URL.String())
		}
	}
}

// TestIPHashAlgorithm tests the IP hash backend selection algorithm
func TestIPHashAlgorithm(t *testing.T) {
	lb := NewLoadbalancer(AlgorithmIPHash, "/health", 0, 3, 30*time.Second)

	// Create 3 test servers
	server1 := createTestServer(t, "Server 1", http.StatusOK)
	defer server1.Close()

	server2 := createTestServer(t, "Server 2", http.StatusOK)
	defer server2.Close()

	server3 := createTestServer(t, "Server 3", http.StatusOK)
	defer server3.Close()

	// Add them to the load balancer
	lb.AddBackend(server1.URL)
	lb.AddBackend(server2.URL)
	lb.AddBackend(server3.URL)

	// Test with different IP addresses
	testCases := []struct {
		ip string
	}{
		{"192.168.1.1"},
		{"10.0.0.1"},
		{"172.16.0.1"},
	}

	for _, tc := range testCases {
		// Create a request with the specific IP
		req, _ := http.NewRequest("GET", "/", nil)
		req.Header.Set("X-Forwarded-For", tc.ip)

		// Same IP should always route to the same backend
		firstBackend, err := lb.NextBackend(req)
		if err != nil {
			t.Fatalf("Error getting next backend: %v", err)
		}

		// Try multiple times to ensure consistency
		for i := 0; i < 5; i++ {
			backend, err := lb.NextBackend(req)
			if err != nil {
				t.Fatalf("Error getting next backend: %v", err)
			}

			if backend.URL.String() != firstBackend.URL.String() {
				t.Errorf("IP %s: Expected consistent backend %s, got %s on iteration %d",
					tc.ip, firstBackend.URL.String(), backend.URL.String(), i)
			}
		}
	}
}

// TestLeastConnectionsAlgorithm tests the least connections backend selection algorithm
func TestLeastConnectionsAlgorithm(t *testing.T) {
	lb := NewLoadbalancer(AlgorithmLeastConn, "/health", 0, 3, 30*time.Second)

	// Create 3 test servers
	server1 := createTestServer(t, "Server 1", http.StatusOK)
	defer server1.Close()

	server2 := createTestServer(t, "Server 2", http.StatusOK)
	defer server2.Close()

	server3 := createTestServer(t, "Server 3", http.StatusOK)
	defer server3.Close()

	// Add them to the load balancer
	lb.AddBackend(server1.URL)
	lb.AddBackend(server2.URL)
	lb.AddBackend(server3.URL)

	// Create a dummy request for testing
	req, _ := http.NewRequest("GET", "/", nil)

	// Get the backends and manually set their fail counts
	backends := lb.GetBackends()

	// First backend has lowest fail count (0)
	backend, err := lb.NextBackend(req)
	if err != nil {
		t.Fatalf("Error getting next backend: %v", err)
	}

	// Check it selected the backend with the lowest fail count
	if backend.URL.String() != backends[0].URL.String() {
		t.Errorf("Expected backend with lowest fail count to be selected")
	}

	// Now increase the fail count of the first backend
	backends[0].FailCount = 3

	// Next request should select the second backend
	backend, err = lb.NextBackend(req)
	if err != nil {
		t.Fatalf("Error getting next backend: %v", err)
	}

	// Check it selected the backend with the now-lowest fail count
	if backend.URL.String() != backends[1].URL.String() {
		t.Errorf("Expected backend with lowest fail count to be selected")
	}
}

// TestHealthCheck tests the health checking functionality
func TestHealthCheck(t *testing.T) {
	// Create load balancer with health checking every 100ms
	lb := NewLoadbalancer(AlgorithmRoundRobin, "/health", 100*time.Millisecond, 3, 30*time.Second)

	// Create a healthy server
	healthyServer := createTestServer(t, "Healthy", http.StatusOK)
	defer healthyServer.Close()

	// Create an unhealthy server that returns 500
	unhealthyServer := createFailingServer(t)
	defer unhealthyServer.Close()

	// Add both to the load balancer
	lb.AddBackend(healthyServer.URL)
	lb.AddBackend(unhealthyServer.URL)

	// Wait for health checks to run (200ms should be enough for 2 checks)
	time.Sleep(200 * time.Millisecond)

	// Check backend statuses
	backends := lb.GetBackends()

	// Healthy server should be marked as alive
	if !backends[0].IsAlive() {
		t.Error("Healthy server incorrectly marked as down")
	}

	// Unhealthy server should be marked as down
	if backends[1].IsAlive() {
		t.Error("Unhealthy server incorrectly marked as alive")
	}

	// Cleanup
	lb.Close()
}

// TestFailover tests failover to another backend when one fails
func TestFailover(t *testing.T) {
	lb := NewLoadbalancer(AlgorithmRoundRobin, "/health", 0, 3, 30*time.Second)

	// Create a healthy server
	healthyServer := createTestServer(t, "Healthy", http.StatusOK)
	defer healthyServer.Close()

	// Create an unhealthy server
	unhealthyServer := createFailingServer(t)
	defer unhealthyServer.Close()

	// Add both to the load balancer
	lb.AddBackend(healthyServer.URL)
	lb.AddBackend(unhealthyServer.URL)

	// Mark the unhealthy server as down
	backends := lb.GetBackends()
	backends[1].SetAlive(false)

	// Create a test request
	req, _ := http.NewRequest("GET", "/", nil)

	// Try to get a backend multiple times - it should always be the healthy one
	for i := 0; i < 5; i++ {
		backend, err := lb.NextBackend(req)
		if err != nil {
			t.Fatalf("Failed to get backend: %v", err)
		}

		if backend.URL.String() != healthyServer.URL {
			t.Errorf("Expected to get the healthy backend, got: %s", backend.URL.String())
		}
	}
}

// TestConcurrentRequests tests handling concurrent requests
func TestConcurrentRequests(t *testing.T) {
	lb := NewLoadbalancer(AlgorithmRoundRobin, "/health", 0, 3, 30*time.Second)

	// Create 3 test servers
	server1 := createTestServer(t, "Server 1", http.StatusOK)
	defer server1.Close()

	server2 := createTestServer(t, "Server 2", http.StatusOK)
	defer server2.Close()

	server3 := createTestServer(t, "Server 3", http.StatusOK)
	defer server3.Close()

	// Add them to the load balancer
	lb.AddBackend(server1.URL)
	lb.AddBackend(server2.URL)
	lb.AddBackend(server3.URL)

	// Try making multiple concurrent requests
	var wg sync.WaitGroup
	requestCount := 100

	serverCounts := make(map[string]int)
	var countMu sync.Mutex

	for i := 0; i < requestCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			req, _ := http.NewRequest("GET", "/", nil)
			backend, err := lb.NextBackend(req)

			if err != nil {
				t.Errorf("Error getting backend: %v", err)
				return
			}

			countMu.Lock()
			serverCounts[backend.URL.String()]++
			countMu.Unlock()
		}()
	}

	wg.Wait()

	// Check that all servers were used and the distribution is roughly even
	if len(serverCounts) != 3 {
		t.Errorf("Not all servers were used, got %d out of 3", len(serverCounts))
	}

	// Each server should have approximately 1/3 of requests
	expectedCount := requestCount / 3
	tolerance := requestCount / 10 // Allow 10% tolerance

	for server, count := range serverCounts {
		if count < expectedCount-tolerance || count > expectedCount+tolerance {
			t.Errorf("Server %s got %d requests, expected roughly %d (±%d)",
				server, count, expectedCount, tolerance)
		}
	}
}

// TestServeHTTP tests the HTTP handler functionality
func TestServeHTTP(t *testing.T) {
	lb := NewLoadbalancer(AlgorithmRoundRobin, "/health", 0, 3, 30*time.Second)

	// Create a test server with a unique response
	testResponse := "Hello from backend!"
	server := createTestServer(t, testResponse, http.StatusOK)
	defer server.Close()

	// Add it to the load balancer
	lb.AddBackend(server.URL)

	// Create a test HTTP recorder
	recorder := httptest.NewRecorder()

	// Create a test request
	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Forwarded-For", "192.168.1.1")

	// Serve the request
	lb.ServeHTTP(recorder, req)

	// Check the response
	if recorder.Code != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, recorder.Code)
	}

	if !strings.Contains(recorder.Body.String(), testResponse) {
		t.Errorf("Expected response body to contain '%s', got '%s'",
			testResponse, recorder.Body.String())
	}
}

// TestAllBackendsDown tests behavior when all backends are down
// func TestAllBackendsDown(t *testing.T) {
// 	lb := NewLoadbalancer(AlgorithmRoundRobin, "/health", 0, 3, 30*time.Second)

// 	// Create a server but mark it as down
// 	server := createTestServer(t, "Test", http.StatusOK)
// 	defer server.Close()

// 	lb.AddBackend(server.URL)

// 	// Mark the server as down
// 	backends := lb.GetBackends()
// 	backends[0].SetAlive(false)

// 	// Create a test HTTP recorder
// 	recorder := httptest.NewRecorder()
// 	// Create a test request
// 	req, _ := http.NewRequest("GET", "/test", nil)
// }
