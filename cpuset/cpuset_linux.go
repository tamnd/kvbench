//go:build linux

package cpuset

import (
	"os"
	"os/exec"
	"runtime"
	"syscall"
)

// The guard and carry environment variables. The first pass sets all three
// before exec'ing under taskset: pinnedEnv breaks the re-exec loop, and the two
// list variables carry the partition chosen from the full core count into the
// re-exec'd process. The second pass must not re-derive the split, because under
// taskset it now sees only the restricted cores and would pick a wrong, smaller
// partition; it reads the chosen lists back from these variables instead.
const (
	pinnedEnv = "KVBENCH_CPU_PINNED"
	serverEnv = "KVBENCH_CPU_SERVER"
	clientEnv = "KVBENCH_CPU_CLIENT"
)

// Available reports whether CPU partitioning can be applied here: this is Linux
// and the taskset tool is on PATH.
func Available() bool {
	_, err := exec.LookPath("taskset")
	return err == nil
}

// Split pins the current process, and so the go-redis client it runs, to a client
// core set and returns the disjoint server set that launched RESP servers are
// pinned to. serverList and clientList override the balanced partition when both
// are given; otherwise the split is derived from the full core count.
//
// On the first pass it replaces the process image with "taskset -c <client> <self>
// <same args>", carrying the chosen lists in the environment, so every OS thread
// the Go runtime later spawns inherits the client affinity mask. That call does
// not return on success. The re-exec'd second pass, recognized by the guard
// variable, reads the lists back, sets GOMAXPROCS to the client core count so the
// scheduler does not oversubscribe the partition, and returns active true.
//
// A re-exec failure is returned to the caller, which can carry on co-located
// rather than abort the run.
func Split(serverList, clientList string) (server, client string, active bool, err error) {
	if os.Getenv(pinnedEnv) != "" {
		server, client = os.Getenv(serverEnv), os.Getenv(clientEnv)
		if n, cerr := Count(client); cerr == nil && n > 0 {
			runtime.GOMAXPROCS(n)
		}
		return server, client, true, nil
	}
	if serverList == "" || clientList == "" {
		s, c, derr := Partition(runtime.NumCPU(), DefaultClientCores(runtime.NumCPU()))
		if derr != nil {
			return "", "", false, derr
		}
		serverList, clientList = s, c
	}
	taskset, err := exec.LookPath("taskset")
	if err != nil {
		return "", "", false, err
	}
	self, err := os.Executable()
	if err != nil {
		return "", "", false, err
	}
	argv := append([]string{"taskset", "-c", clientList, self}, os.Args[1:]...)
	env := append(os.Environ(), pinnedEnv+"=1", serverEnv+"="+serverList, clientEnv+"="+clientList)
	// Exec replaces this image; on success it never returns.
	if err := syscall.Exec(taskset, argv, env); err != nil {
		return "", "", false, err
	}
	return serverList, clientList, true, nil
}

// ServerWrap rewrites a launch command so the server runs pinned to the cores
// named by list. It returns the taskset binary and the original command folded
// into its arguments. An empty list, or a host without taskset, leaves the
// command untouched.
func ServerWrap(list, bin string, args []string) (string, []string) {
	if list == "" {
		return bin, args
	}
	if _, err := exec.LookPath("taskset"); err != nil {
		return bin, args
	}
	wrapped := append([]string{"-c", list, bin}, args...)
	return "taskset", wrapped
}
