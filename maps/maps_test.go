package maps_test

import (
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/graxinc/cache/maps"
)

func TestBuiltin_random(t *testing.T) {
	m := maps.NewBuiltin[int, int]()
	testRandom(m, t)
}

func TestSync_random(t *testing.T) {
	var m maps.Sync[int, int]
	testRandom(&m, t)
}

func TestBucketed_random(t *testing.T) {
	m := maps.NewBucketed[int, int](0)
	testRandom(m, t)
}

func testRandom(m maps.Map[int, int], t *testing.T) {
	t.Parallel()

	rando := rand.New(rand.NewSource(5)) //nolint:gosec

	type kv struct {
		k, v int
	}
	var kvs []kv
	for range 100 {
		k := rando.Int()
		kvs = append(kvs, kv{k, k * 2})
	}

	var hits, miss atomic.Int64
	do := func(seed int64) {
		rando := rand.New(rand.NewSource(seed)) //nolint:gosec

		for range len(kvs) * 100 {
			idx := rando.Intn(len(kvs))
			kv := kvs[idx]

			var v int
			var ok bool
			switch rando.Intn(3) {
			case 0:
				v, ok = m.Get(kv.k)
			case 1:
				v, ok = m.Delete(kv.k)
			case 2:
				v, ok = m.Add(kv.k, kv.v)
			}
			if !ok {
				miss.Add(1)
				continue
			}
			hits.Add(1)
			if v != kv.k*2 {
				t.Fatal(kv, v)
			}
		}
	}

	var wg sync.WaitGroup
	for range 10 {
		seed := rando.Int63()
		wg.Add(1)
		go func() {
			defer wg.Done()
			do(seed)
		}()
	}
	wg.Wait()

	if hits.Load() < 40_000 || miss.Load() < 40_000 {
		t.Fatal(hits.Load(), miss.Load())
	}
	t.Log("hit/miss", hits.Load(), miss.Load())
}
