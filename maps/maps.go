package maps

import (
	"sync"

	"github.com/graxinc/syncmap"
	"golang.org/x/exp/constraints"
)

// Concurrent safe.
type Map[K, V any] interface {
	Get(K) (_ V, ok bool)

	// Replaces.
	Add(K, V) (_ V, exists bool)

	Delete(K) (_ V, exists bool)
}

type KMap[V any] interface {
	Get() (_ V, ok bool)
	Add(v V) (_ V, exists bool)
	Delete() (_ V, exists bool)
}

type builtin[K comparable, V any] map[K]V

func (m builtin[K, V]) Get(k K) (V, bool) {
	v, ok := m[k]
	return v, ok
}

func (m builtin[K, V]) Add(k K, v V) (V, bool) {
	ev, ok := m[k]
	m[k] = v
	return ev, ok
}

func (m builtin[K, V]) Delete(k K) (V, bool) {
	v, ok := m[k]
	delete(m, k)
	return v, ok
}

type kBuiltin[K comparable, V any] struct {
	k K
	m builtin[K, V]
}

func (m kBuiltin[K, V]) Get() (V, bool) {
	return m.m.Get(m.k)
}

func (m kBuiltin[K, V]) Add(v V) (V, bool) {
	return m.m.Add(m.k, v)
}

func (m kBuiltin[K, V]) Delete() (V, bool) {
	return m.m.Delete(m.k)
}

type Builtin[K comparable, V any] struct {
	mu sync.RWMutex
	m  builtin[K, V]
}

func NewBuiltin[K comparable, V any]() *Builtin[K, V] {
	return &Builtin[K, V]{m: make(map[K]V)}
}

func (m *Builtin[K, V]) Get(k K) (V, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.m.Get(k)
}

func (m *Builtin[K, V]) Add(k K, v V) (V, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.m.Add(k, v)
}

func (m *Builtin[K, V]) Delete(k K) (V, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.m.Delete(k)
}

type Sync[K comparable, V any] struct {
	// NOTE there exists zero happens-before when using
	// syncmap.Range even without concurrency, avoid.
	m syncmap.Map[K, V]
}

func (m *Sync[K, V]) Get(k K) (V, bool) {
	return m.m.Load(k)
}

func (m *Sync[K, V]) Add(k K, v V) (V, bool) {
	e, loaded := m.m.Swap(k, v)
	return e, loaded
}

func (m *Sync[K, V]) Delete(k K) (V, bool) {
	e, loaded := m.m.LoadAndDelete(k)
	return e, loaded
}

type Bucketed[K constraints.Integer, V any] struct {
	buckets    []*Builtin[K, V]
	bucketsLen uint64
}

// n defaults to 256.
func NewBucketed[K constraints.Integer, V any](n int) Bucketed[K, V] {
	if n <= 0 {
		n = 256
	}

	buckets := make([]*Builtin[K, V], n)
	for i := range buckets {
		buckets[i] = NewBuiltin[K, V]()
	}
	return Bucketed[K, V]{
		buckets:    buckets,
		bucketsLen: uint64(n),
	}
}

func (m Bucketed[K, V]) Get(k K) (V, bool) {
	return m.bucket(k).Get(k)
}

func (m Bucketed[K, V]) Add(k K, v V) (V, bool) {
	return m.bucket(k).Add(k, v)
}

func (m Bucketed[K, V]) Delete(k K) (V, bool) {
	return m.bucket(k).Delete(k)
}

func (m Bucketed[K, V]) bucket(k K) *Builtin[K, V] {
	idx := uint64(k) % m.bucketsLen
	return m.buckets[idx]
}

// Caller must unlock.
func (m Bucketed[K, V]) Bucket(k K) (_ KMap[V], unlock func()) {
	b := m.bucket(k)
	b.mu.Lock()
	return kBuiltin[K, V]{k, b.m}, b.mu.Unlock
}
