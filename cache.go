package cache

import (
	"iter"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/graxinc/cache/maps"
	"github.com/graxinc/cache/policy"
	"github.com/graxinc/errutil"
)

type CacheValue[V any] struct {
	expire uint32 // seconds since expirationEpoch
	size   uint32 // store so it doesn't change before applying to Cache.size.
	v      V
}

type CacheOptions[K, V any] struct {
	Expiration    time.Duration                      // Defaults to forever.
	Evict         func(K, V)                         // Might be called concurrently.
	Capacity      int64                              // Defaults to 100.
	RLock         bool                               // Whether to use an RLock when possible. Defaults to false.
	MapCreator    func() maps.Map[K, *CacheValue[V]] // defaults to maps.Sync.
	PolicyCreator func() policy.Policy[K]            // defaults to policy.NewARC.
}

type locker interface {
	RLock()
	RUnlock()
	TryLock() bool
	Lock()
	Unlock()
}

type mutexLocker struct {
	sync.Mutex
}

func (l *mutexLocker) RLock()   { l.Mutex.Lock() }
func (l *mutexLocker) RUnlock() { l.Mutex.Unlock() }

// Concurrent safe.
type Cache[K, V any] struct {
	// immutable
	zero            V
	policyMu        locker
	expiration      uint32
	expirationEpoch time.Time
	evictBool       atomic.Bool
	evict           func(K, V)
	items           maps.Map[K, *CacheValue[V]]
	policy          policy.Policy[K]

	cap    atomic.Int64
	size   atomic.Int64
	length atomic.Int64
}

func NewCache[K comparable, V any](o CacheOptions[K, V]) *Cache[K, V] {
	if o.Capacity <= 0 {
		o.Capacity = 100
	}
	if o.Evict == nil {
		o.Evict = func(K, V) {}
	}
	var policyMu locker
	if o.RLock {
		policyMu = &sync.RWMutex{}
	} else {
		policyMu = &mutexLocker{}
	}

	var expiration uint32
	if o.Expiration > 0 {
		secs := o.Expiration / time.Second
		if secs > math.MaxUint32 {
			expiration = 0 // forever
		} else {
			expiration = max(1, uint32(secs))
		}
	}

	if o.MapCreator == nil {
		o.MapCreator = func() maps.Map[K, *CacheValue[V]] { return &maps.Sync[K, *CacheValue[V]]{} }
	}
	if o.PolicyCreator == nil {
		o.PolicyCreator = func() policy.Policy[K] { return policy.NewARC[K]() }
	}

	c := &Cache[K, V]{
		expiration:      expiration,
		expirationEpoch: time.Now(),
		evict:           o.Evict,
		items:           o.MapCreator(),
		policy:          o.PolicyCreator(),
		policyMu:        policyMu,
	}
	c.cap.Store(o.Capacity)
	return c
}

// Does not Promote.
func (a *Cache[K, V]) Peek(k K) (V, bool) {
	v, ok := a.get(k)
	if !ok {
		return a.zero, false
	}
	return v.v, true
}

func (a *Cache[K, V]) Promote(k K) {
	if a.policyMu.TryLock() {
		defer a.policyMu.Unlock()
		a.policy.Promote(k)
	} // fast path for high contention, that do not promote.
}

// Promotes.
func (a *Cache[K, V]) Get(k K) (_ V, ok bool) {
	v, ok := a.get(k)
	if !ok {
		return a.zero, false
	}

	a.Promote(k)

	return v.v, true
}

// Alias for SetS(k,v,1).
func (a *Cache[K, V]) Set(k K, v V) {
	a.SetS(k, v, 1)
}

// Replaces existing values, which are evicted.
// A min size of 1 will be used.
func (a *Cache[K, V]) SetS(k K, v V, size uint32) {
	// items.Add replaces, and we return if exists. That ensures only one
	// caller will get past items.Add until items.Delete (after eviction),
	// keeping the set of keys between policy and items consistent.

	size = max(1, size)

	av := &CacheValue[V]{expire: a.expire(), size: size, v: v}

	if p, ok := a.items.Add(k, av); ok {
		a.size.Add(int64(size) - int64(p.size)) // remove+add
		a.evict(k, p.v)
		return
	}

	a.evicts()

	a.length.Add(1)
	a.size.Add(int64(size))
	a.panicPolicyAdd(k)
}

func (a *Cache[K, V]) evicts() {
	if !a.evictBool.CompareAndSwap(false, true) {
		return
	}
	defer a.evictBool.Store(false)

	for s := a.size.Load(); s >= a.cap.Load(); s = a.size.Load() {
		k, ok := a.policyEvict()
		if !ok {
			break
		}
		v := a.panicDelete(k)

		a.length.Add(-1)
		a.size.Add(-int64(v.size))
		a.evict(k, v.v)
	}
}

// Results ordered by hot->cold. Will block.
func (a *Cache[K, V]) All() iter.Seq2[K, V] {
	return func(yield func(K, V) bool) {
		a.policyMu.RLock()
		defer a.policyMu.RUnlock()

		for k := range a.policy.Values() {
			v := a.panicGet(k)
			if a.expired(v.expire) {
				continue
			}
			if !yield(k, v.v) {
				return
			}
		}
	}
}

func (a *Cache[K, V]) Len() int {
	return int(a.length.Load())
}

func (a *Cache[K, V]) Size() int64 {
	return a.size.Load()
}

// Evicts all and resets. Does not change capacity. Will block.
func (a *Cache[K, V]) Clear() {
	a.policyMu.Lock()
	defer a.policyMu.Unlock()

	for k := range a.policy.Values() {
		v := a.panicDelete(k)
		a.size.Add(-int64(v.size))
		a.evict(k, v.v)
	}
	a.policy.Clear()
}

// Noop if smaller. available (+/-) should not consider taken space in cache.
// DEPRECATED. Please use SetAvailableCapacity.
func (a *Cache[K, V]) SetLargerCapacity(available, max int64) {
	// same as before commit e4e057.
	for {
		cap := a.cap.Load()
		size := a.size.Load()

		// If size is over capacity, use capacity as base
		// to prevent repeated increases even with zero delta.
		base := min(size, cap)

		new := base + available

		new = min(max, new)

		if new <= cap {
			return
		}

		if a.cap.CompareAndSwap(cap, new) {
			return
		}
	}
}

func (a *Cache[K, V]) SetCapacity(c int64) {
	a.cap.Store(c)
}

// available (+/-) should not consider taken space in cache.
func (a *Cache[K, V]) SetAvailableCapacity(available, max int64) {
	new := a.size.Load() + available

	if new > max {
		new = max
	}
	if new <= 0 {
		new = 1
	}

	a.cap.Store(new)
}

func (a *Cache[K, V]) get(k K) (*CacheValue[V], bool) {
	v, ok := a.items.Get(k)
	if !ok || a.expired(v.expire) {
		return nil, false
	}
	return v, true
}

func (a *Cache[K, V]) panicGet(k K) *CacheValue[V] {
	v, ok := a.items.Get(k)
	if !ok {
		panic(errutil.New(errutil.Tags{"missingValue": k}))
	}
	return v
}

func (a *Cache[K, V]) panicDelete(k K) *CacheValue[V] {
	v, exists := a.items.Delete(k)
	if !exists {
		panic(errutil.New(errutil.Tags{"notInItems": k}))
	}
	return v
}

func (a *Cache[K, V]) policyEvict() (_ K, ok bool) {
	a.policyMu.Lock()
	defer a.policyMu.Unlock()

	return a.policy.Evict()
}

func (a *Cache[K, V]) panicPolicyAdd(k K) {
	a.policyMu.Lock()
	defer a.policyMu.Unlock()

	if !a.policy.Add(k) {
		panic(errutil.New(errutil.Tags{"alreadyInPolicy": k}))
	}
}

func (a *Cache[K, V]) secsAfterExpireEpoch() uint32 {
	return uint32(time.Since(a.expirationEpoch) / time.Second)
}

func (a *Cache[K, V]) expire() uint32 {
	if a.expiration <= 0 {
		return 0
	}
	return a.secsAfterExpireEpoch() + a.expiration
}

func (a *Cache[K, V]) expired(expire uint32) bool {
	if expire == 0 {
		return false
	}
	return a.secsAfterExpireEpoch() >= expire
}
