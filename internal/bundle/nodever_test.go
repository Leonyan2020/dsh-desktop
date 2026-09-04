package bundle

import "testing"

func TestSemverAtLeast(t *testing.T) {
	cases := []struct {
		s   nodeSemver
		min string
		ok  bool
		want bool
	}{
		{nodeSemver{22, 15, 0, true}, MinNodeSemver, true, true},
		{nodeSemver{22, 14, 9, true}, MinNodeSemver, true, false},
		{nodeSemver{24, 19, 0, true}, MinNodeSemver, true, true},
		{nodeSemver{20, 18, 3, true}, MinNodeSemver, true, false},
		{nodeSemver{23, 0, 0, true}, MinNodeSemver, true, true},
		{nodeSemver{26, 0, 0, true}, MinNodeSemver, true, true},
		{nodeSemver{}, MinNodeSemver, false, false},
	}
	for _, c := range cases {
		if c.ok != c.s.ok {
			t.Fatalf("case malformed: %+v", c)
		}
		if got := c.s.ok && semverAtLeast(c.s, c.min); got != c.want {
			t.Errorf("semverAtLeast(v%d.%d.%d, %s) = %v, want %v",
				c.s.major, c.s.minor, c.s.patch, c.min, got, c.want)
		}
	}
}

func TestNodeSatisfiesPath(t *testing.T) {
	if NodeSatisfiesPath("") {
		t.Error("empty path must not satisfy")
	}
	if NodeSatisfiesPath("Z:\\definitely\\not\\a\\node.exe") {
		t.Error("missing binary must not satisfy")
	}
}
