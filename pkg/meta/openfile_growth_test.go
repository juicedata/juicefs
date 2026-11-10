package meta

import (
	"runtime"
	"testing"
	"time"
)

func TestOpenfilesNoGoroutineLeakOnShutdown(t *testing.T) {
	settle := func() {
		runtime.GC()
		time.Sleep(300 * time.Millisecond)
		runtime.GC()
	}

	settle()
	base := runtime.NumGoroutine()

	const cycles = 100
	for i := 0; i < cycles; i++ {
		m := NewClient("memkv://", nil)
		if err := m.Shutdown(); err != nil {
			t.Fatalf("shutdown: %v", err)
		}
	}

	settle()
	if leaked := runtime.NumGoroutine() - base; leaked > cycles/10 {
		t.Fatalf("leaked %d goroutines after %d NewClient+Shutdown cycles", leaked, cycles)
	}
}
