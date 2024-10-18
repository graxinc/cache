package counting_test

import (
	"sync"
	"testing"

	"github.com/graxinc/cache/counting"
)

func TestNode_incRelease(t *testing.T) {
	t.Parallel()

	v := &releaseVal{}
	n := counting.NewNode(v)

	do := func() {
		var handles []counting.Handle[*releaseVal]
		for range 1000 {
			h, ok := n.Handle()
			if !ok {
				t.Error("did not increment")
				return
			}
			handles = append(handles, h)
		}
		for _, h := range handles {
			h.Release()
			h.Release() // should be idempotent
		}
	}

	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			do()
		}()
	}
	wg.Wait()

	// haven't released final n.Release.

	if n.Handles() != 0 {
		t.Fatal(n.Handles())
	}
	if v.rel != 0 {
		t.Fatal(v.rel)
	}
}

func TestNode_singleRelease(t *testing.T) {
	t.Parallel()

	v := &releaseVal{}
	n := counting.NewNode(v)

	var handles []counting.Handle[*releaseVal]
	for range 5 {
		h, _ := n.Handle()
		handles = append(handles, h)
	}

	var wg sync.WaitGroup
	for _, h := range handles {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.Release()
			h.Release() // should be idempotent
		}()
	}
	n.Release()
	n.Release() // should be idempotent

	// including final release in the concurrent releases
	wg.Wait()

	if n.Handles() != -1 {
		t.Fatal(n.Handles())
	}
	if v.releases() != 1 {
		t.Fatal(v.releases())
	}

	if _, ok := n.Handle(); ok {
		t.Fatal("should not get already released handle")
	}
}

func TestCache_alreadyRelease(t *testing.T) {
	t.Parallel()

	keys := []int{1, 2, 3, 4}

	// 1 for many evictions.
	o := counting.CacheOptions[int, *releaseVal]{Capacity: 1}
	c := counting.NewCache(o)

	// Targeting the optimistic loop in Get, where a node has no handles plus is
	// evicted by a Set while another goroutine Gets the same node.
	do := func() {
		for range 1000 {
			for _, k := range keys {
				h, ok := c.Get(k)
				if !ok {
					h = c.Set(k, &releaseVal{})
				}
				if h.Value().releases() > 0 {
					t.Error("should never be handed a released handle")
					return
				}
				h.Release()
				h.Release() // should be idempotent
			}
		}

		t.Log("handles", c.Handles())
	}

	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			do()
		}()
	}
	wg.Wait()

	if v := c.Handles(); v != 0 {
		t.Fatal(v)
	}
	c.Clear()
	if v := c.Size(); v != 0 {
		t.Fatal(v)
	}
}

func TestCache_setExisting(t *testing.T) {
	t.Parallel()

	o := counting.CacheOptions[int, *releaseVal]{Capacity: 99}
	c := counting.NewCache(o)

	v1 := &releaseVal{}
	c.Set(1, v1).Release()

	v2 := &releaseVal{}
	c.Set(1, v2).Release()

	if got := v1.releases(); got != 1 {
		t.Fatal("should have released on replacement", got)
	}
	if got := v2.releases(); got != 0 {
		t.Fatal("should not release, still in cache", got)
	}
}

func TestCache_sizer(t *testing.T) {
	t.Parallel()

	o := counting.CacheOptions[int, *releaseVal]{Capacity: 99}
	c := counting.NewCache(o)

	c.SetS(1, &releaseVal{}, 2)
	c.SetS(2, &releaseVal{}, 4)

	if v := c.Size(); v != 6 {
		t.Fatal(v)
	}
}

type releaseVal struct {
	mu  sync.Mutex
	rel int
}

func (r *releaseVal) Release() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rel++
}

func (r *releaseVal) releases() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rel
}
