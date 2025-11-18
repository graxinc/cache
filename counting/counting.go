package counting

import (
	"iter"
	"math"
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
type Node[T Releaser] struct {
	value T

	// First to hit -1 runs the Value.Release.
	handles atomic.Int32

	// 0 not released, 1 node released, 2 value released.
	released atomic.Int32
}

// v is Released after all Handles have been Released plus the node Release.
func NewNode[T Releaser](v T) *Node[T] {
	return &Node[T]{value: v}
}

func (n *Node[T]) Release() {
	if n.released.CompareAndSwap(0, 1) {
		n.dec()
	}
}

// Node already released when !ok.
// Caller must release Handle.
func (n *Node[T]) Handle() (_ Handle[T], ok bool) {
	if !n.inc() {
		return nil, false
	}
	return &handle[T]{n: n}, true
}

// Node already released when !ok.
// Caller must release Handle, ONLY once.
func (n *Node[T]) OnceHandle() (_ Handle[T], ok bool) {
	if !n.inc() {
		return nil, false
	}
	return onceHandle[T]{n}, true
}

// Intended for metrics.
func (n *Node[T]) Handles() int {
	return int(n.handles.Load())
}

func (n *Node[T]) Value() T {
	return n.value
}

func (n *Node[T]) inc() (ok bool) {
	for {
		old := n.handles.Load()
		if old < 0 {
			return false
		}
		if old >= math.MaxInt32-2 {
			continue // max handles, busy wait
		}
		if !n.handles.CompareAndSwap(old, old+1) {
			continue // concurrent, try again
		}
		return true
	}
}

func (n *Node[T]) dec() {
	v := n.handles.Add(-1)
	if v < 0 && n.released.CompareAndSwap(1, 2) {
		n.value.Release()
	}
}

type handle[T Releaser] struct {
	n        *Node[T]
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

type onceHandle[T Releaser] struct {
	n *Node[T]
}

func (h onceHandle[T]) Value() T {
	return h.n.Value()
}

func (h onceHandle[T]) Release() {
	h.n.dec()
}

// Similar to cache.Cache except values are Released when evicted, but only after all the
// Handles of that value are released. This is useful when the value needs to track a reusable item
// to know all callers are done with the value.
// Concurrent safe.
type Cache[K comparable, V Releaser] struct {
	cache *cache.Cache[K, *Node[V]]
}

type CacheOptions[K any, V Releaser] struct {
	Expiration    time.Duration                                   // Defaults to forever.
	Capacity      int64                                           // Defaults to 100.
	MapCreator    func() maps.Map[K, *cache.CacheValue[*Node[V]]] // defaults to maps.Sync
	PolicyCreator func() policy.Policy[K]                         // defaults to policy.NewARC
	Evict         func(_ K, _ V, Release func())                  // Caller must Release, not V.Release.
	EvictSkip     bool
}

func NewCache[K comparable, V Releaser](o CacheOptions[K, V]) Cache[K, V] {
	evict := func(k K, v *Node[V]) {
		v.Release()
	}
	if o.Evict != nil {
		evict = func(k K, v *Node[V]) {
			o.Evict(k, v.Value(), v.Release)
		}
	}

	var evictSkip func(K, *Node[V]) bool
	if o.EvictSkip {
		evictSkip = func(k K, n *Node[V]) bool {
			return n.Handles() > 0
		}
	}

	c := cache.NewCache(cache.CacheOptions[K, *Node[V]]{
		Expiration:    o.Expiration,
		Evict:         evict,
		Capacity:      o.Capacity,
		MapCreator:    o.MapCreator,
		PolicyCreator: o.PolicyCreator,
		EvictSkip:     evictSkip,
	})
	return Cache[K, V]{c}
}

// Results ordered by most->least. Will block.
// Caller must release each Handle.
func (a Cache[K, V]) All() iter.Seq2[K, Handle[V]] {
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
func (a Cache[K, V]) Peek(k K) (Handle[V], bool) {
	for {
		v, ok := a.cache.Peek(k)
		if !ok {
			return nil, false
		}
		if h, ok := v.Handle(); ok {
			return h, true
		} // else already released, get fresh
	}
}

// Caller must release Handle. Does not Promote.
func (a Cache[K, V]) OncePeek(k K) (Handle[V], bool) {
	for {
		v, ok := a.cache.Peek(k)
		if !ok {
			return nil, false
		}
		if h, ok := v.OnceHandle(); ok {
			return h, true
		} // else already released, get fresh
	}
}

func (a Cache[K, V]) Promote(k K) {
	a.cache.Promote(k)
}

// Caller must release Handle. Promotes.
func (a Cache[K, V]) Get(k K) (Handle[V], bool) {
	h, ok := a.Peek(k)
	if !ok {
		return nil, false
	}
	a.cache.Promote(k)
	return h, true
}

// Caller must release Handle, ONLY once. Promotes.
func (a Cache[K, V]) OnceGet(k K) (Handle[V], bool) {
	h, ok := a.OncePeek(k)
	if !ok {
		return nil, false
	}
	a.cache.Promote(k)
	return h, true
}

// Alias for SetS(k,v,1).
func (a Cache[K, V]) Set(k K, v V) Handle[V] {
	return a.SetS(k, v, 1)
}

// Alias for OnceSetS(k,v,1).
func (a Cache[K, V]) OnceSet(k K, v V) Handle[V] {
	return a.OnceSetS(k, v, 1)
}

// Replaces existing values, which are evicted.
// A min size of 1 will be used.
// Caller must release Handle.
func (a Cache[K, V]) SetS(k K, v V, size uint32) Handle[V] {
	n := NewNode(v)
	h, _ := n.Handle()
	a.cache.SetS(k, n, size)
	return h
}

// Replaces existing values, which are evicted.
// A min size of 1 will be used.
// Caller must release Handle, ONLY once.
func (a Cache[K, V]) OnceSetS(k K, v V, size uint32) Handle[V] {
	n := NewNode(v)
	h, _ := n.OnceHandle()
	a.cache.SetS(k, n, size)
	return h
}

func (a Cache[K, V]) Evict() (noSpace bool) {
	return a.cache.Evict()
}

func (a Cache[K, V]) Len() int {
	return a.cache.Len()
}

func (a Cache[K, V]) Size() int64 {
	return a.cache.Size()
}

// Evicts all and resets. Does not change capacity. Will block.
func (a Cache[K, V]) Clear() {
	a.cache.Clear()
}

// Intended for metrics.
func (a Cache[K, V]) Handles() int {
	var c int
	for _, v := range a.cache.All() {
		h := v.Handles()
		if h > 0 {
			c += h
		}
	}
	return c
}

func (a Cache[K, V]) Capacity() int64 {
	return a.cache.Capacity()
}

func (a Cache[K, V]) SetCapacity(new int64) (old int64) {
	return a.cache.SetCapacity(new)
}

func (a Cache[K, V]) SwapCapacity(old, new int64) (swapped bool) {
	return a.cache.SwapCapacity(old, new)
}

// available (+/-) should not consider taken space in cache.
func (a Cache[K, V]) SetAvailableCapacity(available, max int64) {
	a.cache.SetAvailableCapacity(available, max)
}
