//go:build !linux

package cpuset

// Available reports that CPU partitioning is not supported off Linux. taskset
// and the affinity model it relies on are Linux only, so a co-located run on
// another platform cannot pin the two sides apart.
func Available() bool { return false }

// Split is a no-op off Linux. It never re-execs and reports no split in effect,
// so the caller runs the load generator co-located with the server.
func Split(serverList, clientList string) (server, client string, active bool, err error) {
	return "", "", false, nil
}

// ServerWrap returns the launch command unchanged off Linux.
func ServerWrap(list, bin string, args []string) (string, []string) { return bin, args }
