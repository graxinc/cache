package counting

import (
	"iter"
	"sync/atomic"
	"time"

	"github.com/graxinc/cache"
	"github.com/graxinc/cache/maps"
	"github.com/graxinc/cache/policy"
)

type Releaser interface {
	// Idempotent.
	Release()
}

type Handle[T any] interface {
	Releaser
	Value() T
}

// A node that tracks Releases from its Handles and only releases the underlying
// value once all Handles and the node itself have been released.
// Concurrent safe.
type CountingNode[T Releaser] struct {
	value T

	// First to hit -1 runs the Value.Release.
	handles atomic.Int64

	released atomic.Bool // for the node release
}

// v is Released after all Handles have been Released plus the node Release.
func NewCountingNode[T Releaser](v T) *CountingNode[T] {
	return &CountingNode[T]{value: v}
}

func (n *CountingNode[T]) Release() {
	if !n.released.Swap(true) {
		n.dec()
	}
}

// Node already released when !ok.
// Caller must release Handle.
func (n *CountingNode[T]) Handle() (_ Handle[T], ok bool) {
	if !n.inc() {
		return nil, false
	}
	return &handle[T]{n: n}, true
}

// Intended for metrics.
func (n *CountingNode[T]) Handles() int {
	return int(n.handles.Load())
}

func (n *CountingNode[T]) Value() T {
	return n.value
}

func (n *CountingNode[T]) inc() (ok bool) {
	for {
		old := n.handles.Load()
		if old < 0 {
			return false
		}
		if !n.handles.CompareAndSwap(old, old+1) {
			continue // concurrent, try again
		}
		return true
	}
}

func (n *CountingNode[T]) dec() {
	// going past -1 protected via bool swaps
	if v := n.handles.Add(-1); v < 0 {
		n.value.Release()
	}
}

type handle[T Releaser] struct {
	n        *CountingNode[T]
	released atomic.Bool
}

func (h *handle[T]) Value() T {
	return h.n.Value()
}

func (h *handle[T]) Release() {
	if !h.released.Swap(true) {
		h.n.dec()
	}
}

// Similar to cache.Cache except values are Released when evicted, but only after all the
// Handles of that value are released. This is useful when the value needs to track a reusable item
// to know all callers are done with the value.
// Concurrent safe.
type CountingCache[K comparable, V Releaser] struct {
	cache *cache.Cache[K, *CountingNode[V]]
}

type CountingCacheOptions[K any, V Releaser] struct {
	Expiration    time.Duration                                           // Defaults to forever.
	Capacity      int64                                                   // Defaults to 100.
	MapCreator    func() maps.Map[K, *cache.CacheValue[*CountingNode[V]]] // defaults to maps.Sync
	PolicyCreator func() policy.Policy[K]                                 // defaults to policy.NewARC
}

func NewCountingCache[K comparable, V Releaser](o CountingCacheOptions[K, V]) CountingCache[K, V] {
	evict := func(k K, v *CountingNode[V]) {
		v.Release()
	}

	c := cache.NewCache(cache.CacheOptions[K, *CountingNode[V]]{
		Expiration:    o.Expiration,
		Evict:         evict,
		Capacity:      o.Capacity,
		MapCreator:    o.MapCreator,
		PolicyCreator: o.PolicyCreator,
	})
	return CountingCache[K, V]{c}
}

// Results ordered by most->least. Will block.
// Caller must release each Handle.
func (a CountingCache[K, V]) All() iter.Seq2[K, Handle[V]] {
	return func(yield func(K, Handle[V]) bool) {
		for k, v := range a.cache.All() {
			h, ok := v.Handle()
			if !ok { // already released, skip
				continue
			}
			if !yield(k, h) {
				return
			}
		}
	}
}

// Caller must release Handle. Does not Promote.
func (a CountingCache[K, V]) Peek(k K) (Handle[V], bool) {
	for {
		v, ok := a.cache.Get(k)
		if !ok {
			return nil, false
		}
		if h, ok := v.Handle(); ok {
			return h, true
		} // else already released, get fresh
	}
}

func (a CountingCache[K, V]) Promote(k K) {
	a.cache.Promote(k)
}

// Caller must release Handle. Promotes.
func (a CountingCache[K, V]) Get(k K) (Handle[V], bool) {
	h, ok := a.Peek(k)
	if !ok {
		return nil, false
	}
	a.cache.Promote(k)
	return h, true
}

// Alias for SetS(k,v,1).
func (a CountingCache[K, V]) Set(k K, v V) Handle[V] {
	return a.SetS(k, v, 1)
}

// Replaces existing values, which are evicted.
// A min size of 1 will be used.
// Caller must release Handle.
func (a CountingCache[K, V]) SetS(k K, v V, size uint32) Handle[V] {
	n := NewCountingNode(v)
	h, _ := n.Handle()
	a.cache.SetS(k, n, size)
	return h
}

func (a CountingCache[K, V]) Len() int {
	return a.cache.Len()
}

func (a CountingCache[K, V]) Size() int64 {
	return a.cache.Size()
}

// Evicts all and resets. Does not change capacity. Will block.
func (a CountingCache[K, V]) Clear() {
	a.cache.Clear()
}

// Intended for metrics.
func (a CountingCache[K, V]) Handles() int {
	var c int
	for _, v := range a.cache.All() {
		h := v.Handles()
		if h > 0 {
			c += h
		}
	}
	return c
}

// Noop if smaller. available should not consider taken space in cache.
func (a CountingCache[K, V]) SetLargerCapacity(available, max int64) {
	a.cache.SetLargerCapacity(available, max)
}
