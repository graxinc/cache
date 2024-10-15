package policy_test

import (
	"fmt"
	"math/rand"
	"slices"
	"testing"

	"github.com/graxinc/cache/policy"

	"github.com/google/go-cmp/cmp"
	lru "github.com/hashicorp/golang-lru/arc/v2"
)

func TestARC_compareImpl(t *testing.T) {
	t.Parallel()

	const (
		cap        = 1000
		keys       = cap * 2
		iterations = keys * 2
	)

	t.Log("cap", cap, "keys", keys, "iterations", iterations)

	type testCase struct {
		itersPerScan         int
		wantTheirs, wantOurs int
	}

	do := func(t *testing.T, c testCase) {
		t.Parallel()

		theirs, err := lru.NewARC[int, struct{}](cap)
		if err != nil {
			t.Fatal(err)
		}

		ours := policy.NewARC[int]()
		var ourSize int64

		ourAdd := func(k int) {
			if ourSize >= cap {
				if _, ok := ours.Evict(); ok {
					ourSize--
				}
			}
			if ours.Add(k) {
				ourSize++
			}
		}

		rando := rand.New(rand.NewSource(5)) //nolint:gosec

		var theirHits, ourHits int

		get := func(k int) {
			if _, ok := theirs.Get(k); ok {
				theirHits++
			} else {
				theirs.Add(k, struct{}{})
			}
			if ours.Promote(k) {
				ourHits++
			} else {
				ourAdd(k)
			}
		}

		for i := range iterations {
			if i%c.itersPerScan == 0 {
				for j := range keys {
					get(j)
				}
			}

			k := rando.Intn(keys)
			get(k)
		}

		if theirHits != c.wantTheirs || ourHits != c.wantOurs {
			t.Fatal(theirHits, ourHits)
		}
	}

	cases := []testCase{
		{2, 798_277, 1_629_236},
		{4, 402_442, 807_548},
		{10, 183_685, 315_757},
		{100, 19_002, 19146},
		{200, 8631, 9048},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("itersPerScan=%v", c.itersPerScan), func(t *testing.T) { do(t, c) })
	}
}

func TestARC_random(t *testing.T) {
	t.Parallel()

	rando := rand.New(rand.NewSource(5)) //nolint:gosec

	const (
		cap        = 1000
		keys       = cap * 4
		iterations = keys * 2
	)
	p := policy.NewARC[int]()

	var hit, miss, evicts, size int64

	do := func() {
		if rando.Intn(2) == 0 {
			k := rando.Intn(keys)

			if size >= cap {
				if _, ok := p.Evict(); ok {
					size--
					evicts++
				}
			}

			if p.Add(k) {
				size++
			}
		} else {
			if p.Promote(rando.Intn(keys)) {
				hit++
			} else {
				miss++
			}
		}
	}

	for range iterations {
		do()
	}

	if hit != 857 || miss != 3175 || evicts != 2102 || size != 1000 {
		t.Fatal(hit, miss, evicts, size)
	}
}

func TestARC_evict(t *testing.T) {
	t.Parallel()

	p := policy.NewARC[int]()

	checkEvict := func(k int) {
		t.Helper()
		e, ok := p.Evict()
		if !ok || e != k {
			t.Fatal(ok, e)
		}
	}

	p.Add(1)
	p.Add(2)
	checkEvict(1)
	p.Add(3)
	checkEvict(2)
	p.Add(1)
	checkEvict(1)
	p.Add(2)
	checkEvict(2)
	p.Add(1)

	want := []int{1, 3}
	got := slices.Collect(p.Values())
	diffFatal(t, want, got)
}

func TestARC_values_order(t *testing.T) {
	t.Parallel()

	p := policy.NewARC[int]()

	for i := range 10 {
		p.Add(i)
	}

	p.Promote(3)
	p.Promote(7)
	p.Promote(6)
	p.Promote(7)
	p.Evict()
	p.Evict()

	want := []int{7, 6, 3, 9, 8, 5, 4, 2}
	got := slices.Collect(p.Values())
	diffFatal(t, want, got)
}

func TestARC_values_stop(t *testing.T) {
	t.Parallel()

	p := policy.NewARC[int]()

	p.Add(1)
	p.Add(2)
	p.Add(3)

	var i int
	var got []int

	p.Values()(func(v int) bool {
		got = append(got, v)
		i++
		return i <= 1
	})

	diffFatal(t, []int{3, 2}, got)
}

func TestARC_clear(t *testing.T) {
	t.Parallel()

	p := policy.NewARC[int]()

	p.Add(1)
	p.Add(2)

	// precondition
	all := slices.Collect(p.Values())
	if len(all) == 0 {
		t.Fatal("expected some")
	}
	if !p.Promote(1) {
		t.Fatal("expected true")
	}
	if !p.Promote(2) {
		t.Fatal("expected true")
	}

	p.Clear()

	all = slices.Collect(p.Values())
	if len(all) != 0 {
		t.Fatal(all)
	}
	if p.Promote(1) {
		t.Fatal("expected false")
	}
	if p.Promote(2) {
		t.Fatal("expected false")
	}
}

func diffFatal(t testing.TB, want, got any, opts ...cmp.Option) {
	t.Helper()
	if d := cmp.Diff(want, got, opts...); d != "" {
		t.Fatalf("(-want +got):\n%v", d)
	}
}
