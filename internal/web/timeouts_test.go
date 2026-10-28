package web

import (
	"testing"
	"time"
)

// TestServerTimeouts: the Fiber server must bound a wedged request; the event
// stream is the one long-lived route, so the write timeout is long, not absent.
func TestServerTimeouts(t *testing.T) {
	config := testApp(t).Config()
	if config.ReadTimeout <= 0 {
		t.Fatalf("ReadTimeout = %s, want a bound", config.ReadTimeout)
	}
	if config.WriteTimeout <= 0 {
		t.Fatalf("WriteTimeout = %s, want a long bound for SSE", config.WriteTimeout)
	}
	if config.IdleTimeout <= 0 {
		t.Fatalf("IdleTimeout = %s, want a bound", config.IdleTimeout)
	}
	if config.WriteTimeout < config.ReadTimeout {
		t.Fatalf("WriteTimeout %s < ReadTimeout %s", config.WriteTimeout, config.ReadTimeout)
	}
	if config.WriteTimeout < time.Minute {
		t.Fatalf("WriteTimeout = %s, want a long timeout for the stream", config.WriteTimeout)
	}
}
