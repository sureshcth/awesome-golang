package util

import (
	"fmt"
	"net"
	"time"
)

// checkPort checks if a TCP port is open on a given host.
// It returns true if the port is open, and false otherwise.
func CheckPort(host string, port int, timeout time.Duration) bool {
	target := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	conn, err := net.DialTimeout("tcp", target, timeout)
	if err != nil {
		return false
	}
	defer conn.Close()
	return true
}
