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

type Builtin[K comparable, V any] struct {
	mu sync.RWMutex
	m  map[K]V
}

func NewBuiltin[K comparable, V any]() *Builtin[K, V] {
	return &Builtin[K, V]{m: make(map[K]V)}
}

func (m *Builtin[K, V]) Get(k K) (V, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.m[k]
	return v, ok
}

func (m *Builtin[K, V]) Add(k K, v V) (V, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ev, ok := m.m[k]
	m.m[k] = v
	return ev, ok
}

func (m *Builtin[K, V]) Delete(k K) (V, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.m[k]
	delete(m.m, k)
	return v, ok
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
