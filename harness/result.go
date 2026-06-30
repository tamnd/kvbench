package harness

import (
	"github.com/tamnd/kvbench/engine"
	"github.com/tamnd/kvbench/env"
	"github.com/tamnd/kvbench/hdr"
)

// Result is one engine x workload x regime x profile cell. It is the unit of
// output and carries the metric bundle plus the setup and fairness asterisks.
type Result struct {
	Schema  string `json:"schema"`
	Kvbench string `json:"kvbench_version"`
	RunID   string `json:"run_id"`
	Seed    uint64 `json:"seed"`

	Engine struct {
		Name    string              `json:"name"`
		Family  string              `json:"family"`
		Mode    string              `json:"mode"`
		Class   string              `json:"class"`
		Version string              `json:"version"`
		Profile string              `json:"profile"`
		Caps    engine.Capabilities `json:"caps"`
	} `json:"engine"`

	Workload struct {
		Name        string `json:"name"`
		Regime      string `json:"regime"`
		Durability  string `json:"durability"`
		Concurrency int    `json:"concurrency"`
		ValueBytes  int    `json:"value_bytes"`
		Cardinality uint64 `json:"cardinality"`
		Operations  uint64 `json:"operations"`
	} `json:"workload"`

	Repetitions int `json:"repetitions"`

	Throughput struct {
		SustainedOps float64 `json:"sustained_ops"`
		Min          float64 `json:"min"`
		Max          float64 `json:"max"`
		ReadOps      float64 `json:"read_ops"`
		WriteOps     float64 `json:"write_ops"`
	} `json:"throughput"`

	LatencyNs hdr.Snapshot `json:"latency_ns"`

	Load struct {
		Ops       uint64  `json:"ops"`
		Seconds   float64 `json:"seconds"`
		OpsPerSec float64 `json:"ops_per_sec"`
	} `json:"load"`

	Amplification struct {
		SpaceAmp     float64 `json:"space_amp"`     // dir bytes / logical bytes, -1 unknown
		OnDiskBytes  int64   `json:"on_disk_bytes"` // -1 unknown
		LogicalBytes int64   `json:"logical_bytes"`
		WriteAmp     float64 `json:"write_amp"` // engine-reported, -1 unknown
		Tier         string  `json:"tier"`      // engine-reported | filesystem | unavailable
	} `json:"amplification"`

	GoRuntime struct {
		GCPauseP99Ns uint64  `json:"gc_pause_p99_ns"`
		GCPauseMaxNs uint64  `json:"gc_pause_max_ns"`
		GCCPUFrac    float64 `json:"gc_cpu_frac"`
		AllocPerOp   float64 `json:"alloc_per_op_bytes"`
		NumGC        uint32  `json:"num_gc"`
	} `json:"goruntime"`

	Asterisks   []engine.Asterisk `json:"asterisks"`
	SteadyState bool              `json:"steady_state"`
	Error       string            `json:"error,omitempty"`
	Environment env.Manifest      `json:"environment"`
}
