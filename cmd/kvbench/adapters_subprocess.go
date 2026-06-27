//go:build subprocess_engines

package main

// Subprocess adapters are compiled in only under the subprocess_engines build
// tag. They drive a non-Go engine over a pipe and need the kvbench-rs helper on
// PATH at run time, so they stay out of the default build.
import _ "github.com/tamnd/kvbench/adapters/rustkv"
