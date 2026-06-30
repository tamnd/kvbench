package engine

import "testing"

func TestClassOf(t *testing.T) {
	cases := []struct {
		name string
		meta Meta
		want Class
	}{
		{"explicit class wins over mode", Meta{Mode: ModeNetwork, Class: ClassRedisPersistent}, ClassRedisPersistent},
		{"in-proc defaults to embedded", Meta{Mode: ModeInProc}, ClassEmbedded},
		{"cgo defaults to embedded", Meta{Mode: ModeCgo}, ClassEmbedded},
		{"subprocess defaults to embedded", Meta{Mode: ModeSubprocess}, ClassEmbedded},
		{"network without class defaults to redis-memory", Meta{Mode: ModeNetwork}, ClassRedisMemory},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ClassOf(c.meta); got != c.want {
				t.Fatalf("ClassOf(%+v) = %q, want %q", c.meta, got, c.want)
			}
		})
	}
}
