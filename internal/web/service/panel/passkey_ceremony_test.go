package panel

import (
	"sync"
	"testing"
	"time"
)

func TestPasskeyStoreAtomicConsumptionAndExpiry(t *testing.T) {
	store := NewPasskeyStore()
	now := time.Now()
	store.now = func() time.Time { return now }
	id, err := store.Put(PasskeyCeremony{Binding: "browser", Purpose: "login"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Take(id, "other-browser", "login"); err == nil {
		t.Fatal("cross-browser challenge accepted")
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0
	for range 12 {
		wg.Go(func() {
			if _, err := store.Take(id, "browser", "login"); err == nil {
				mu.Lock()
				successes++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if successes != 1 {
		t.Fatalf("successes=%d", successes)
	}
	id, _ = store.Put(PasskeyCeremony{Binding: "browser", Purpose: "register"}, time.Minute)
	if _, err := store.Take(id, "browser", "login"); err == nil {
		t.Fatal("cross-purpose challenge accepted")
	}
	id, _ = store.Put(PasskeyCeremony{Binding: "browser", Purpose: "login"}, time.Minute)
	now = now.Add(time.Minute)
	if _, err := store.Take(id, "browser", "login"); err == nil {
		t.Fatal("expired challenge accepted")
	}
}
