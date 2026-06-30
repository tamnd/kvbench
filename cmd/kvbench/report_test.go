package main

import (
	"strings"
	"testing"

	"github.com/tamnd/kvbench/engine"
	"github.com/tamnd/kvbench/harness"
)

// result builds a minimal Result for one engine, with its class and mode set the
// way the harness records them.
func result(name, mode, class, workload string, ops float64) harness.Result {
	var r harness.Result
	r.Engine.Name = name
	r.Engine.Mode = mode
	r.Engine.Class = class
	r.Workload.Name = workload
	r.Throughput.SustainedOps = ops
	return r
}

func TestClassOfResult(t *testing.T) {
	// A tagged result keeps its class.
	if got := classOf(result("kv-redis", "network", "redis-persistent", "fillrandom", 1)); got != engine.ClassRedisPersistent {
		t.Fatalf("tagged result classOf = %q, want redis-persistent", got)
	}
	// An untagged network result (an older run) falls back to redis-memory.
	if got := classOf(result("valkey", "network", "", "fillrandom", 1)); got != engine.ClassRedisMemory {
		t.Fatalf("untagged network classOf = %q, want redis-memory", got)
	}
	// An untagged embedded result falls back to embedded.
	if got := classOf(result("kv-f2", "in-proc", "", "fillrandom", 1)); got != engine.ClassEmbedded {
		t.Fatalf("untagged in-proc classOf = %q, want embedded", got)
	}
}

func TestGroupByClassOrder(t *testing.T) {
	rs := []harness.Result{
		result("valkey", "network", "redis-memory", "fillrandom", 100),
		result("kv-f2", "in-proc", "embedded", "fillrandom", 200),
		result("kv-redis", "network", "redis-persistent", "fillrandom", 50),
	}
	classes, byClass := groupByClass(rs)
	// Present classes come back in published order, embedded first.
	want := []engine.Class{engine.ClassEmbedded, engine.ClassRedisMemory, engine.ClassRedisPersistent}
	if len(classes) != len(want) {
		t.Fatalf("got %d classes, want %d: %v", len(classes), len(want), classes)
	}
	for i := range want {
		if classes[i] != want[i] {
			t.Fatalf("class %d = %q, want %q", i, classes[i], want[i])
		}
	}
	if len(byClass[engine.ClassEmbedded]) != 1 || byClass[engine.ClassEmbedded][0].Engine.Name != "kv-f2" {
		t.Fatalf("embedded class should hold kv-f2, got %+v", byClass[engine.ClassEmbedded])
	}
}

func TestRenderMarkdownSplitsClasses(t *testing.T) {
	rs := []harness.Result{
		result("kv-f2", "in-proc", "embedded", "readrandom", 200),
		result("valkey", "network", "redis-memory", "readrandom", 100),
		result("kv-redis", "network", "redis-persistent", "readrandom", 50),
	}
	md := RenderMarkdown(rs)
	for _, heading := range []string{
		"## Class 1: embedded local KV engines",
		"## Class 2: Redis-compatible in-memory servers",
		"## Class 3: Redis-compatible persistent servers",
	} {
		if !strings.Contains(md, heading) {
			t.Fatalf("markdown missing heading %q\n%s", heading, md)
		}
	}
	// The embedded class heading must come before the in-memory one.
	if strings.Index(md, "Class 1") > strings.Index(md, "Class 2") {
		t.Fatalf("Class 1 should be rendered before Class 2")
	}
	// Class 4 has no rows, so its heading must not appear.
	if strings.Contains(md, "Class 4") {
		t.Fatalf("empty Class 4 should be skipped")
	}
}
