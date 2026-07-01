package cpuset

import "testing"

func TestPartitionDisjoint(t *testing.T) {
	cases := []struct {
		numCPU, client         int
		wantServer, wantClient string
	}{
		{6, 3, "0-2", "3-5"},
		{6, 2, "0-3", "4-5"},
		{4, 2, "0-1", "2-3"},
		{2, 1, "0", "1"},
		// clientCores clamps so neither set is ever empty
		{4, 9, "0", "1-3"},
		{4, 0, "0-2", "3"},
	}
	for _, c := range cases {
		s, cl, err := Partition(c.numCPU, c.client)
		if err != nil {
			t.Fatalf("Partition(%d,%d): %v", c.numCPU, c.client, err)
		}
		if s != c.wantServer || cl != c.wantClient {
			t.Fatalf("Partition(%d,%d) = %q,%q; want %q,%q", c.numCPU, c.client, s, cl, c.wantServer, c.wantClient)
		}
	}
}

func TestPartitionTooFewCores(t *testing.T) {
	if _, _, err := Partition(1, 1); err == nil {
		t.Fatal("Partition(1,1) should error: nothing to split")
	}
}

func TestDefaultClientCoresBalanced(t *testing.T) {
	cases := []struct{ numCPU, want int }{
		{2, 1}, {4, 2}, {6, 3}, {8, 4}, {16, 8},
	}
	for _, c := range cases {
		if got := DefaultClientCores(c.numCPU); got != c.want {
			t.Fatalf("DefaultClientCores(%d) = %d; want %d", c.numCPU, got, c.want)
		}
	}
}

func TestCount(t *testing.T) {
	ok := []struct {
		list string
		want int
	}{
		{"0", 1}, {"3-5", 3}, {"0-3,6", 5}, {" 0 - 2 , 4 ", 4},
	}
	for _, c := range ok {
		got, err := Count(c.list)
		if err != nil {
			t.Fatalf("Count(%q): %v", c.list, err)
		}
		if got != c.want {
			t.Fatalf("Count(%q) = %d; want %d", c.list, got, c.want)
		}
	}
	for _, bad := range []string{"", "  ", "x", "5-2"} {
		if _, err := Count(bad); err == nil {
			t.Fatalf("Count(%q) should error", bad)
		}
	}
}
