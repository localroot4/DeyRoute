package version

import "testing"

func TestCompatible(t *testing.T) {
	cases := []struct {
		hub, node string
		want      bool
	}{
		{"1.0.0", "1.0.5", true},
		{"1.0.0", "v1.0.0", true},
		{"1.1.0", "1.0.9", false},
		{"2.0.0", "1.0.0", false},
		{"dev", "dev", true},
		{"dev", "1.0.0", false},
		{"1.0.0-rc.1", "1.0.0", true},
	}
	for _, c := range cases {
		if got := Compatible(c.hub, c.node); got != c.want {
			t.Errorf("Compatible(%q,%q)=%v want %v", c.hub, c.node, got, c.want)
		}
	}
}

func TestSet(t *testing.T) {
	old := Version
	defer func() { Version = old }()
	Set("v1.2.3", "", "")
	if Version != "1.2.3" || Display() != "v1.2.3" {
		t.Fatalf("got %q %q", Version, Display())
	}
}
