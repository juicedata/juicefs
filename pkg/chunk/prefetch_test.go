package chunk

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestPrefetcher(t *testing.T) {
	t.Run("should fetch given keys", func(t *testing.T) {
		keys := []string{"source/1", "source/2", "source/3", "source/4"}
		chRes := make(chan string, len(keys))
		defer close(chRes)
		f := newPrefetcher(2, func(k string) {
			chRes <- k + "Done"
		})
		for _, k := range keys {
			f.fetch(k)
		}
		res := make(map[string]bool, len(keys))
		for range keys {
			res[<-chRes] = true
		}
		if len(res) != len(keys) {
			t.Errorf("Incorrect number of keys fetched, expect: %d, got: %d", len(keys), len(res))
		}
		for _, k := range keys {
			if !res[k+"Done"] {
				t.Errorf("Key not fetched: %s", k)
			}
		}
	})
	t.Run("should ignore duplicate keys", func(t *testing.T) {
		var counter int32
		f := newPrefetcher(4, func(k string) {
			// Introduce a little latency to mimic a slower fetch operation
			// so that our few duplicate keys can reach the prefetcher in the time period
			time.Sleep(time.Millisecond)
			atomic.AddInt32(&counter, 1)
		})
		for i := 0; i < 5; i++ {
			f.fetch("a")
		}
		if atomic.LoadInt32(&counter) > 1 {
			t.Errorf("Duplicate keys  fetched")
		}
	})
}
