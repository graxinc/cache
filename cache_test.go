package cache_test

import (
	"fmt"
	"maps"
	"math/rand"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/graxinc/cache"
	cmaps "github.com/graxinc/cache/maps"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/pkg/profile"
)

func TestCache_getSet(t *testing.T) {
	t.Parallel()

	var evicts string
	evict := func(k string, v any) {
		evicts += fmt.Sprint(k) + "=" + fmt.Sprint(v) + ","
	}
	a := cache.NewCache(cache.CacheOptions[string, any]{Capacity: 2, Evict: evict})

	checkAll(t, a, nil)
	checkSize(t, a, 0, 0)

	a.Set("a", "aa")
	a.Set("a", "another")

	checkAll(t, a, map[string]any{
		"a": "another",
	})
	checkSize(t, a, 1, 1)

	a.Set("1", 11)
	a.Set("2", 22)
	a.Set("3", 33)

	checkAll(t, a, map[string]any{
		"2": 22,
		"3": 33,
	})
	checkSize(t, a, 2, 2)

	if evicts != "a=aa,a=another,1=11," {
		t.Fatal(evicts)
	}
}

func TestCache_promote(t *testing.T) {
	t.Parallel()

	a := cache.NewCache(cache.CacheOptions[string, any]{Capacity: 3})

	for _, k := range []string{"a", "b", "c"} {
		a.Set(k, k+k)
	}

	for _, k := range []string{"a", "b", "c"} {
		if v, ok := a.Peek(k); !ok || v != k+k {
			t.Fatal(k, ok, v)
		}
	}

	a.Promote("b")
	a.Set("d", "dd")
	a.Promote("b")
	a.Set("e", "ee")

	for _, k := range []string{"b", "d", "e"} {
		if v, ok := a.Peek(k); !ok || v != k+k {
			t.Fatal(k, ok, v)
		}
	}
	for _, k := range []string{"a", "c"} {
		if _, ok := a.Peek(k); ok {
			t.Fatal(k)
		}
	}
}

func TestCache_peek(t *testing.T) {
	t.Parallel()

	a := cache.NewCache(cache.CacheOptions[string, any]{Capacity: 3})

	for _, k := range []string{"a", "b", "c"} {
		a.Set(k, k+k)
	}

	for _, k := range []string{"a", "b", "c"} {
		if v, ok := a.Peek(k); !ok || v != k+k {
			t.Fatal(k, ok, v)
		}
	}

	a.Peek("b") // should not promote
	a.Set("d", "dd")
	a.Peek("b") // should not promote
	a.Set("e", "ee")

	for _, k := range []string{"c", "d", "e"} {
		if v, ok := a.Peek(k); !ok || v != k+k {
			t.Fatal(k, ok, v)
		}
	}
	for _, k := range []string{"a", "b"} {
		if _, ok := a.Peek(k); ok {
			t.Fatal(k)
		}
	}
}

func TestCache_random(t *testing.T) {
	t.Parallel()

	var evicts atomic.Int64
	evict := func(k, v int) {
		if v != k*2 {
			t.Error(k, v)
		}
		evicts.Add(1)
	}

	type kv struct {
		k, v int
	}

	var kvs []kv
	for i := range 10_000 {
		kvs = append(kvs, kv{i, i * 2})
	}

	o := cache.CacheOptions[int, int]{Capacity: int64(len(kvs) / 10), Evict: evict}
	a := cache.NewCache(o)

	do := func(seed int64) {
		rando := rand.New(rand.NewSource(seed)) //nolint:gosec
		for range len(kvs) * 10 {
			idx := rando.Intn(len(kvs))
			kv := kvs[idx]

			got, ok := a.Get(kv.k)
			if !ok {
				a.Set(kv.k, kv.v)
				continue
			}
			if got != kv.v {
				t.Fatal(kv, got)
			}
		}
	}

	rando := rand.New(rand.NewSource(1)) //nolint:gosec

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

	if evicts.Load() == 0 {
		t.Fatal("expected evicts")
	}
	t.Log("evicts", evicts.Load())
}

func TestCache_Sizer_race(t *testing.T) {
	t.Parallel()

	type cacheVal struct {
		size uint32
	}

	o := cache.CacheOptions[int, cacheVal]{Capacity: 1000}
	a := cache.NewCache(o)

	const goroutines = 10
	goLoop := func(id int) {
		for range 100 {
			val := cacheVal{uint32(1 + id%goroutines)}
			a.SetS(1, val, val.size)
		}
	}

	loopsAndCheck := func() {
		var wg sync.WaitGroup
		for i := range goroutines {
			wg.Add(1)
			go func() {
				defer wg.Done()
				goLoop(i)
			}()
		}
		wg.Wait()

		var size int64
		for _, v := range a.All() {
			size += int64(v.size)
		}
		if a.Size() != size {
			t.Fatal("expected equal, walked size", size, "cache size", a.Size())
		}
	}

	for range 1000 {
		loopsAndCheck()
	}
}

func TestCache_ExistingEvict(t *testing.T) {
	t.Parallel()

	var evicts int
	evict := func(k string, v any) {
		evicts++
	}
	a := cache.NewCache(cache.CacheOptions[string, any]{Capacity: 99, Evict: evict})

	checkAll(t, a, nil)
	checkSize(t, a, 0, 0)

	a.Set("a", "1")
	a.Set("a", "2")
	a.Set("a", "3")

	checkAll(t, a, map[string]any{
		"a": "3",
	})
	checkSize(t, a, 1, 1)

	if evicts != 2 {
		t.Fatal(evicts)
	}
}

func TestCache_Clear_evicts(t *testing.T) {
	t.Parallel()

	var evicts []int
	evict := func(k int, _ string) {
		evicts = append(evicts, k)
	}
	a := cache.NewCache(cache.CacheOptions[int, string]{Capacity: 2, Evict: evict})

	a.Set(1, "a")
	a.Set(2, "b")

	a.Clear()

	diffFatal(t, []int{1, 2}, evicts, sprintSorter[int]())
}

func TestCache_Clear_random(t *testing.T) {
	t.Parallel()

	o := cache.CacheOptions[int, struct{}]{Capacity: 80}
	a := cache.NewCache(o)

	do := func(seed int) {
		rando := rand.New(rand.NewSource(int64(seed))) //nolint:gosec
		for range 100 {
			k := rando.Intn(100)
			a.Set(k, struct{}{})
		}

		a.Clear()

		for range 100 {
			k := rando.Intn(100)
			a.Set(k, struct{}{})
		}

		a.Clear()
	}

	var wg sync.WaitGroup
	for i := range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			do(i)
		}()
	}
	wg.Wait()

	if a.Size() != 0 {
		t.Fatal(a.Size())
	}

	for i := range 100 {
		if _, ok := a.Get(i); ok {
			t.Fatal("should not have val")
		}
	}
	checkAll(t, a, nil)
}

func TestCache_SetLargerCapacity(t *testing.T) {
	t.Parallel()

	a := cache.NewCache(cache.CacheOptions[string, struct{}]{Capacity: 10})

	a.SetS("a", struct{}{}, 10)
	a.SetS("b", struct{}{}, 5)
	a.SetS("c", struct{}{}, 4)

	checkKeys(t, a, "b", "c")
	checkSize(t, a, 2, 9)

	a.SetLargerCapacity(11, 20)

	a.SetS("d", struct{}{}, 17)

	checkKeys(t, a, "b", "c", "d")
	checkSize(t, a, 3, 26)

	a.SetS("e", struct{}{}, 1)

	checkKeys(t, a, "d", "e")
	checkSize(t, a, 2, 18)

	a.SetLargerCapacity(0, 20)

	checkSize(t, a, 2, 18)
}

func TestCache_SetLargerCapacity_sizeGreaterCap(t *testing.T) {
	t.Parallel()

	a := cache.NewCache(cache.CacheOptions[string, struct{}]{Capacity: 10})

	a.SetS("a", struct{}{}, 12)

	checkKeys(t, a, "a")
	checkSize(t, a, 1, 12)

	a.SetLargerCapacity(2, 20)

	a.SetS("b", struct{}{}, 1)

	// evicts "a" since avail 2 was added to cap of 10 instead of size of 12.
	checkKeys(t, a, "b")
	checkSize(t, a, 1, 1)
}

func TestCache_growPastCapacity(t *testing.T) {
	t.Parallel()

	cases := []struct {
		available  int64
		larger     bool
		wantLength int
		wantSize   int
	}{
		{0, false, 1, 1},
		{1, false, 39, 50},
		{3, false, 39, 50},
		{-1, false, 1, 1},
		{-3, false, 1, 1},
		{1, true, 39, 50},
		{3, true, 39, 50},
		{-1, true, 4, 4},
		{-3, true, 4, 4},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("larger=%v,avail=%v", c.larger, c.available), func(t *testing.T) {
			// ensure we don't grow past capacity in an iterative way.

			rando := rand.New(rand.NewSource(5)) //nolint:gosec
			a := cache.NewCache(cache.CacheOptions[int, struct{}]{Capacity: 4})

			for i := range 2000 {
				r := uint32(rando.Intn(3))
				if c.larger {
					a.SetLargerCapacity(c.available, 50)
				} else {
					a.SetAvailableCapacity(c.available, 50)
				}
				a.SetS(i, struct{}{}, r)
			}

			checkSize(t, a, c.wantLength, int64(c.wantSize))
		})
	}

	t.Run("randomizedAvail", func(t *testing.T) {
		cases := []struct {
			larger                    bool
			iterations                int
			wantMin, wantMed, wantMax int64
		}{
			{false, 10, 1, 5, 10},
			{false, 100, 1, 3, 10},
			{false, 2000, 1, 3, 23},
			{true, 10, 5, 11, 17},
			{true, 100, 5, 49, 51},
			{true, 2000, 5, 50, 51},
		}
		for _, c := range cases {
			t.Run(fmt.Sprintf("larger=%v,iterations=%v", c.larger, c.iterations), func(t *testing.T) {
				rando := rand.New(rand.NewSource(5)) //nolint:gosec
				a := cache.NewCache(cache.CacheOptions[int, struct{}]{Capacity: 4})
				// fill first
				for i := range 5 {
					a.SetS(-i, struct{}{}, 1)
				}
				if a.Size() != 4 { // precondition
					t.Fatal(a.Size())
				}

				var sizes []int64
				for i := range c.iterations {
					r := uint32(rando.Intn(3))
					avail := rando.Int63n(11) - 5 // [-5,5]
					if c.larger {
						a.SetLargerCapacity(avail, 50)
					} else {
						a.SetAvailableCapacity(avail, 50)
					}
					a.SetS(i, struct{}{}, r)

					sizes = append(sizes, a.Size())
				}

				slices.Sort(sizes)
				min := sizes[0]
				med := sizes[len(sizes)/2]
				max := sizes[len(sizes)-1]

				if min != c.wantMin {
					t.Fatal(min)
				}
				if med != c.wantMed {
					t.Fatal(med)
				}
				if max != c.wantMax {
					t.Fatal(max)
				}
			})
		}
	})
}

func TestCache_SetCapacity_nonPositive(t *testing.T) {
	t.Parallel()

	a := cache.NewCache(cache.CacheOptions[string, struct{}]{Capacity: 2})

	if a.Capacity() != 2 { // precondition
		t.Fatal(a.Capacity())
	}

	a.SetCapacity(0)

	if a.Capacity() != 1 {
		t.Fatal(a.Capacity())
	}
}

func TestCache_SwapCapacity(t *testing.T) {
	t.Parallel()

	a := cache.NewCache(cache.CacheOptions[string, struct{}]{Capacity: 2})

	if a.Capacity() != 2 { // precondition
		t.Fatal(a.Capacity())
	}

	if a.SwapCapacity(1, 3) {
		t.Fatal("expected false")
	}
	if a.Capacity() != 2 {
		t.Fatal(a.Capacity())
	}

	if !a.SwapCapacity(2, 3) {
		t.Fatal("expected true")
	}
	if a.Capacity() != 3 {
		t.Fatal(a.Capacity())
	}
}

func TestCache_Expiration(t *testing.T) {
	t.Parallel()

	do := func(t *testing.T, expiration time.Duration) {
		t.Parallel()

		a := cache.NewCache(cache.CacheOptions[int, any]{Capacity: 10, Expiration: expiration})

		setGet := func() {
			for timeout := time.Now().Add(5 * time.Second); ; time.Sleep(time.Millisecond) {
				if time.Now().After(timeout) {
					t.Fatal("timeout")
				}

				a.Set(5, nil)
				checkKeys(t, a, 5)

				if _, ok := a.Get(5); ok { // precondition
					break
				}
			}
		}

		setGet()

		time.Sleep(max(time.Second, expiration))

		if _, ok := a.Get(5); ok {
			t.Fatal("expected not ok")
		}
		checkKeys(t, a)

		// refreshes the expiration
		setGet()
	}

	for _, exp := range []time.Duration{100 * time.Millisecond, time.Second, 2 * time.Second} {
		t.Run(fmt.Sprint(exp), func(t *testing.T) { do(t, exp) })
	}
}

func TestCache_Sizer(t *testing.T) {
	t.Parallel()

	a := cache.NewCache(cache.CacheOptions[string, struct{}]{Capacity: 10})

	a.SetS("a", struct{}{}, 4)

	checkSize(t, a, 1, 4)

	a.SetS("b", struct{}{}, 6)

	checkKeys(t, a, "a", "b")
	checkSize(t, a, 2, 10)

	a.SetS("c", struct{}{}, 1)
	a.SetS("c", struct{}{}, 1)

	checkKeys(t, a, "b", "c")
	checkSize(t, a, 2, 7)
}

func TestCache_Sizer_random(t *testing.T) {
	t.Parallel()

	a := cache.NewCache(cache.CacheOptions[string, struct{}]{Capacity: 1000})

	rando := rand.New(rand.NewSource(5)) //nolint:gosec

	var keys []string
	for i := range 1000 {
		keys = append(keys, strconv.Itoa(i))
	}

	for range 10_000 {
		n1 := rando.Intn(len(keys))
		n2 := rando.Intn(len(keys))
		n3 := uint32(rando.Intn(11))
		a.Get(keys[n1]) // to jiggle order.
		a.SetS(keys[n2], struct{}{}, n3)
	}

	if g := a.Size(); g != 1000 {
		t.Fatal(g)
	}
}

func BenchmarkCache_memory(b *testing.B) {
	rando := rand.New(rand.NewSource(5)) //nolint:gosec

	var keys []string
	buf := make([]byte, 20) // hash-like length
	for range 1_000_000 {
		randRead(b, rando, buf)

		keys = append(keys, string(buf))
	}

	b.ReportAllocs()

	a := cache.NewCache(cache.CacheOptions[string, struct{}]{Capacity: 100_000})

	getSet := func() {
		n1 := rando.Intn(len(keys))
		n2 := rando.Intn(len(keys))
		a.Get(keys[n1]) // to jiggle order.
		a.Set(keys[n2], struct{}{})
	}

	for range 2_000_000 { // fill
		getSet()
	}

	defer profile.Start(profile.MemProfile).Stop()

	for range b.N {
		getSet()
	}
}

func BenchmarkCache_getSet(b *testing.B) {
	rando := rand.New(rand.NewSource(5)) //nolint:gosec

	type key struct {
		a int
		b *string
		c *string
	}
	type kv struct {
		key
		v string
	}

	const (
		items         = 10_000
		capacity      = items / 10
		setSize       = 100
		setIterations = 1000 // roughly the hit ratio
	)

	var kvs []kv
	buf := make([]byte, 20) // hash-like length
	for i := range items {
		randRead(b, rando, buf)

		s := string(buf)
		b := s + "b"
		c := s + "c"

		key := key{i, &b, &c}
		v := s + "val"
		kvs = append(kvs, kv{key, v})
	}

	o := cache.CacheOptions[key, string]{Capacity: int64(capacity)}
	a := cache.NewCache(o)

	var hit, miss atomic.Uint64

	getSet := func(seed int64) {
		rando := rand.New(rand.NewSource(seed)) //nolint:gosec

		var kvSet []kv // to have a desired hit ratio
		for range setSize {
			idx := rando.Intn(len(kvs))
			kvSet = append(kvSet, kvs[idx])
		}

		for range setIterations {
			for _, kv := range kvSet {
				if _, ok := a.Get(kv.key); ok {
					hit.Add(1)
					continue
				}
				miss.Add(1)
				a.Set(kv.key, kv.v)
			}
		}
	}

	benchDo := func() {
		var wg sync.WaitGroup
		for range 20 {
			seed := rando.Int63()
			wg.Add(1)
			go func() {
				defer wg.Done()
				getSet(seed)
			}()
		}
		wg.Wait()
	}

	defer profile.Start(profile.ClockProfile).Stop()

	for range b.N {
		benchDo()
	}

	h, m := hit.Load(), miss.Load()
	b.Log("hit/miss/ratio", h, m, float64(h)/float64(m))
}

func BenchmarkCache_getSet_bucketed(b *testing.B) {
	rando := rand.New(rand.NewSource(5)) //nolint:gosec

	type kv struct {
		k, v int
	}

	const (
		items         = 10_000
		capacity      = items / 10
		setSize       = 100
		setIterations = 1000 // roughly the hit ratio
	)

	var kvs []kv
	for i := range items {
		kvs = append(kvs, kv{i, i * 2})
	}

	o := cache.CacheOptions[int, int]{
		Capacity: int64(capacity),
		MapCreator: func() cmaps.Map[int, *cache.CacheValue[int]] {
			return cmaps.NewBucketed[int, *cache.CacheValue[int]](0)
		},
	}
	a := cache.NewCache(o)

	var hit, miss atomic.Uint64

	getSet := func(seed int64) {
		rando := rand.New(rand.NewSource(seed)) //nolint:gosec

		var kvSet []kv // to have a desired hit ratio
		for range setSize {
			idx := rando.Intn(len(kvs))
			kvSet = append(kvSet, kvs[idx])
		}

		for range setIterations {
			for _, kv := range kvSet {
				if _, ok := a.Get(kv.k); ok {
					hit.Add(1)
					continue
				}
				miss.Add(1)
				a.Set(kv.k, kv.v)
			}
		}
	}

	benchDo := func() {
		var wg sync.WaitGroup
		for range 20 {
			seed := rando.Int63()
			wg.Add(1)
			go func() {
				defer wg.Done()
				getSet(seed)
			}()
		}
		wg.Wait()
	}

	defer profile.Start(profile.ClockProfile).Stop()

	for range b.N {
		benchDo()
	}

	h, m := hit.Load(), miss.Load()
	b.Log("hit/miss/ratio", h, m, float64(h)/float64(m))
}

func checkAll[K comparable, V any](t testing.TB, c *cache.Cache[K, V], want map[K]V) {
	t.Helper()

	got := maps.Collect(c.All())
	diffFatal(t, want, got, cmpopts.EquateEmpty())
}

func checkKeys[K comparable, V any](t testing.TB, c *cache.Cache[K, V], want ...K) {
	t.Helper()

	all := maps.Collect(c.All())
	got := slices.Collect(maps.Keys(all))
	diffFatal(t, want, got, sprintSorter[K](), cmpopts.EquateEmpty())
}

func checkSize[K comparable, V any](t testing.TB, c *cache.Cache[K, V], length int, size int64) {
	t.Helper()
	if v := c.Len(); v != length {
		t.Fatal(v)
	}
	if v := c.Size(); v != size {
		t.Fatal(v)
	}
}

func diffFatal(t testing.TB, want, got any, opts ...cmp.Option) {
	t.Helper()
	if d := cmp.Diff(want, got, opts...); d != "" {
		t.Fatalf("(-want +got):\n%v", d)
	}
}

func randRead(t testing.TB, r *rand.Rand, p []byte) {
	t.Helper()
	if _, err := r.Read(p); err != nil {
		t.Fatal(err)
	}
}

func sprintSorter[T any]() cmp.Option {
	return cmpopts.SortSlices(func(a, b T) bool {
		return fmt.Sprint(a) < fmt.Sprint(b)
	})
}
