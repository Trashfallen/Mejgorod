package main

import (
	"testing"
	"time"
)

func runAutoConnector(statuses []string, permanent, userStops bool) (attempts int, pauses []time.Duration) {
	ac := autoConnector{
		attempt: func() string {
			st := statuses[min(attempts, len(statuses)-1)]
			attempts++
			return st
		},
		permanent: func() bool { return permanent },
		pause: func(d time.Duration) bool {
			pauses = append(pauses, d)
			return !userStops
		},
		logf:   func(string, ...any) {},
		delays: []time.Duration{10 * time.Second, 20 * time.Second, 30 * time.Second},
	}
	ac.run()
	return attempts, pauses
}

func TestAutoConnectorSuccessFirstTry(t *testing.T) {
	n, p := runAutoConnector([]string{stRunning}, false, false)
	if n != 1 || len(p) != 0 {
		t.Fatalf("attempts=%d pauses=%v", n, p)
	}
}

func TestAutoConnectorRetriesUntilRunning(t *testing.T) {
	n, p := runAutoConnector([]string{stError, stError, stRunning}, false, false)
	if n != 3 || len(p) != 2 || p[0] != 10*time.Second || p[1] != 20*time.Second {
		t.Fatalf("attempts=%d pauses=%v", n, p)
	}
}

func TestAutoConnectorGivesUp(t *testing.T) {
	n, p := runAutoConnector([]string{stError}, false, false)
	if n != 4 || len(p) != 3 {
		t.Fatalf("attempts=%d pauses=%v", n, p)
	}
}

func TestAutoConnectorPermanentErrorNotRetried(t *testing.T) {
	n, p := runAutoConnector([]string{stError}, true, false)
	if n != 1 || len(p) != 0 {
		t.Fatalf("attempts=%d pauses=%v", n, p)
	}
}

func TestAutoConnectorStopsWhenUserActs(t *testing.T) {
	n, p := runAutoConnector([]string{stError}, false, true)
	if n != 1 || len(p) != 1 {
		t.Fatalf("attempts=%d pauses=%v", n, p)
	}
}

func TestAutoConnectorUserDisconnectEndsLoop(t *testing.T) {
	n, p := runAutoConnector([]string{stStopped}, false, false)
	if n != 1 || len(p) != 0 {
		t.Fatalf("attempts=%d pauses=%v", n, p)
	}
}
