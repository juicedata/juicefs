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

func TestOpenfilesPinnedEntryIsNotCleaned(t *testing.T) {
	o := &openfiles{
		limit: 1,
		files: make(map[Ino]*openFile),
		stop:  make(chan struct{}),
	}

	o.Open(1, &Attr{})
	o.Close(1)
	o.Open(2, &Attr{})
	o.Close(2)
	o.Open(3, &Attr{})
	o.Close(3)
	if of := o.acquire(1); of == nil {
		t.Fatal("failed to acquire open file")
	}

	go o.cleanup()
	defer o.close()
	defer o.releasePin(1)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		o.Lock()
		_, pinnedExists := o.files[1]
		length := len(o.files)
		o.Unlock()
		if pinnedExists && length < 3 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	o.Lock()
	defer o.Unlock()
	if _, ok := o.files[1]; !ok {
		t.Fatal("pinned open file was cleaned")
	}
	if len(o.files) >= 3 {
		t.Fatal("unpinned open files were not cleaned")
	}
}

func TestOpenfilesReadAttrAndTier(t *testing.T) {
	o := &openfiles{files: make(map[Ino]*openFile)}
	attr := Attr{Mtime: 1, Mtimensec: 2, Tier: 3}
	o.Open(1, &attr)

	var got Attr
	if !o.ReadAttr(1, &got) {
		t.Fatal("failed to read cached attr")
	}
	if got.Tier != attr.Tier || got.Mtime != attr.Mtime || got.Mtimensec != attr.Mtimensec {
		t.Fatalf("unexpected attr: %+v", got)
	}

	got.Tier = 7
	if tier, ok := o.Tier(1); !ok || tier != attr.Tier {
		t.Fatalf("unexpected tier: %d, %v", tier, ok)
	}
}
