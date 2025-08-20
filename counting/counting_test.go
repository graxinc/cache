package counting_test

import (
	"slices"
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

func TestCache_evict(t *testing.T) {
	t.Parallel()

	var evicts []int
	var evictReleases []func()
	evict := func(k int, v *releaseVal, release func()) {
		evicts = append(evicts, k)
		evictReleases = append(evictReleases, release)
	}
	o := counting.CacheOptions[int, *releaseVal]{Capacity: 2, Evict: evict}
	c := counting.NewCache(o)

	v1 := &releaseVal{}
	c.Set(1, v1).Release()

	v2 := &releaseVal{}
	c.Set(2, v2).Release()

	v3 := &releaseVal{}
	c.Set(3, v3).Release()

	v4 := &releaseVal{}
	c.Set(4, v4).Release()

	if !slices.Equal(evicts, []int{1, 2}) {
		t.Fatal("bad evicts", evicts)
	}

	for _, v := range []*releaseVal{v1, v2, v3, v4} {
		if got := v.releases(); got != 0 {
			t.Fatal("should not release yet", got)
		}
	}

	for _, r := range evictReleases {
		r()
	}

	if got := v1.releases(); got != 1 {
		t.Fatal("bad release", got)
	}
	if got := v2.releases(); got != 1 {
		t.Fatal("bad release", got)
	}
}

func TestCache_evictSkip(t *testing.T) {
	t.Parallel()

	var evicts []int
	evict := func(k int, v *releaseVal, release func()) {
		evicts = append(evicts, k)
		release()
	}
	o := counting.CacheOptions[int, *releaseVal]{Capacity: 5, Evict: evict, EvictSkip: true}
	c := counting.NewCache(o)

	vals := make(map[int]*releaseVal)
	checkRel := func(i int, want int) {
		t.Helper()
		r := vals[i].releases()
		if r != want {
			t.Fatal("bad releases", i, r)
		}
	}
	addVal := func(i int) {
		v := &releaseVal{}
		c.Set(i, v).Release()
		vals[i] = v
	}
	addVal(1)
	addVal(2)
	addVal(3)
	addVal(4)
	addVal(5)

	get := func(k int) {
		h, ok := c.Get(k)
		if !ok {
			t.Fatal("missing", k)
		}
		h.Release()
	}
	getHold := func(k int) counting.Releaser {
		h, ok := c.Get(k)
		if !ok {
			t.Fatal("missing", k)
		}
		return h
	}

	get(3)
	get(4)
	h := getHold(1)
	get(3)
	get(3)

	if len(evicts) > 0 {
		t.Fatal(evicts)
	}

	addVal(6)
	get(6)
	addVal(7)
	get(7)

	if !slices.Equal(evicts, []int{2, 5}) {
		t.Fatal("bad evicts", evicts)
	}

	checkRel(1, 0)

	h.Release()

	addVal(8)
	get(8)
	addVal(9)
	get(9)

	if !slices.Equal(evicts, []int{2, 5, 4, 1}) {
		t.Fatal("bad evicts", evicts)
	}

	for _, i := range evicts {
		checkRel(i, 1)
		delete(vals, i)
	}
	for i := range vals {
		checkRel(i, 0)
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
