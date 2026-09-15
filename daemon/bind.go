package daemon

import (
	"net"
	"strings"
)

// bindIsLoopback reports whether a configured bind address only accepts local
// connections. A host that cannot be parsed is treated as remote, because
// guessing the permissive way is the expensive mistake.
func bindIsLoopback(bind string) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(bind))
	if err != nil {
		host = strings.TrimSpace(bind)
	}
	switch host {
	case "localhost", "":
		return host == "localhost"
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
