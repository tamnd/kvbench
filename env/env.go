// Package env captures the run environment so every result is reproducible and
// no number ships detached from its setup.
package env

import (
	"os/exec"
	"runtime"
	"strings"
)

type Manifest struct {
	OS         string `json:"os"`
	Arch       string `json:"arch"`
	NumCPU     int    `json:"num_cpu"`
	GoVersion  string `json:"go_version"`
	GOMAXPROCS int    `json:"gomaxprocs"`
	CPUModel   string `json:"cpu_model"`
	MemBytes   int64  `json:"mem_bytes"`
	Hostname   string `json:"hostname,omitempty"`
}

func Capture() Manifest {
	m := Manifest{
		OS:         runtime.GOOS,
		Arch:       runtime.GOARCH,
		NumCPU:     runtime.NumCPU(),
		GoVersion:  runtime.Version(),
		GOMAXPROCS: runtime.GOMAXPROCS(0),
		CPUModel:   cpuModel(),
		MemBytes:   memBytes(),
	}
	if h, err := exec.Command("hostname").Output(); err == nil {
		m.Hostname = strings.TrimSpace(string(h))
	}
	return m
}

func cpuModel() string {
	switch runtime.GOOS {
	case "darwin":
		if out, err := exec.Command("sysctl", "-n", "machdep.cpu.brand_string").Output(); err == nil {
			return strings.TrimSpace(string(out))
		}
	case "linux":
		if out, err := exec.Command("sh", "-c", "grep -m1 'model name' /proc/cpuinfo | cut -d: -f2").Output(); err == nil {
			return strings.TrimSpace(string(out))
		}
	}
	return "unknown"
}

func memBytes() int64 {
	switch runtime.GOOS {
	case "darwin":
		if out, err := exec.Command("sysctl", "-n", "hw.memsize").Output(); err == nil {
			return parseInt(strings.TrimSpace(string(out)))
		}
	case "linux":
		if out, err := exec.Command("sh", "-c", "grep MemTotal /proc/meminfo | awk '{print $2*1024}'").Output(); err == nil {
			return parseInt(strings.TrimSpace(string(out)))
		}
	}
	return -1
}

func parseInt(s string) int64 {
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int64(c-'0')
	}
	return n
}
