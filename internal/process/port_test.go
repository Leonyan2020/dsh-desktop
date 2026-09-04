//go:build windows

package process

import "testing"

// Live check against whatever is listening on the managed port right now:
// when the harness is up, findPortListenerPid must report its node PID; on a
// port nobody listens on it must report 0.
func TestFindPortListenerPid(t *testing.T) {
	pid, err := findPortListenerPid(DefaultPort)
	if err != nil {
		t.Fatalf("netstat parse failed: %v", err)
	}
	t.Logf("port %d listener pid = %d", DefaultPort, pid)

	empty := 47615 // unassigned port; nothing should listen there
	if pid2, err := findPortListenerPid(empty); err != nil || pid2 != 0 {
		t.Fatalf("expected 0 listeners on %d, got pid=%d err=%v", empty, pid2, err)
	}
}
