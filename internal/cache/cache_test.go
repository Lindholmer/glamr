package cache

import (
	"sync"
	"testing"
	"time"

	"glamr/internal/provider"
)

func TestNewCacheIsEmptyAndStale(t *testing.T) {
	c := NewCache(time.Minute)
	if data, ok := c.Get(); ok || data != nil {
		t.Errorf("Get() on empty cache = (%v, %v), want (nil, false)", data, ok)
	}
	if !c.IsStale() {
		t.Error("empty cache should be stale")
	}
	if !c.LastRefresh().IsZero() {
		t.Error("LastRefresh should be zero before Set")
	}
}

func TestSetAndGet(t *testing.T) {
	c := NewCache(time.Minute)
	mrs := []provider.MergeRequest{{ID: "team/app!1"}, {ID: "team/app!2"}}

	before := time.Now()
	c.Set(mrs)

	data, ok := c.Get()
	if !ok || len(data) != 2 || data[0].ID != "team/app!1" {
		t.Errorf("Get() = (%v, %v)", data, ok)
	}
	if c.IsStale() {
		t.Error("freshly set cache should not be stale")
	}
	if c.LastRefresh().Before(before) {
		t.Error("LastRefresh should be updated by Set")
	}
}

func TestExpiry(t *testing.T) {
	c := NewCache(10 * time.Millisecond)
	c.Set([]provider.MergeRequest{{ID: "x"}})
	time.Sleep(20 * time.Millisecond)

	if _, ok := c.Get(); ok {
		t.Error("Get() should miss after TTL")
	}
	if !c.IsStale() {
		t.Error("cache should be stale after TTL")
	}
}

func TestConcurrentAccess(t *testing.T) {
	c := NewCache(time.Minute)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			c.Set([]provider.MergeRequest{{ID: "x"}})
		}()
		go func() {
			defer wg.Done()
			c.Get()
			c.IsStale()
			c.LastRefresh()
		}()
	}
	wg.Wait()
}
