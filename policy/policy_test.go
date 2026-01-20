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
		{2, 798_277, 1_622_440},
		{4, 402_442, 803_115},
		{10, 183_685, 306_237},
		{100, 19_002, 23_781},
		{200, 8631, 8398},
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
		iterations = keys * 1000
	)
	p := policy.NewARC[int]()

	var hit, miss, evicts, size int64

	t1TargetFractions := make(map[int]struct{})
	for i := range 10 {
		t1TargetFractions[i] = struct{}{}
	}

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

		t := int(p.ARCParams().T1TargetFraction * 10)
		delete(t1TargetFractions, t)
	}

	for range iterations {
		do()
	}

	if hit != 499_535 || miss != 1_502_201 || evicts != 1_498_196 || size != 1000 {
		t.Fatal(hit, miss, evicts, size)
	}
	diffFatal(t, make(map[int]struct{}), t1TargetFractions)
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

	want := []int{3, 1}
	got := slices.Collect(p.Values())
	diffFatal(t, want, got)
}

func TestARC_evictSkip(t *testing.T) {
	t.Parallel()

	p := policy.NewARC[int]()

	skip := func(k int) bool {
		return k == 2
	}

	checkEvict := func(k int) {
		t.Helper()
		e, ok := p.EvictSkip(skip)
		if !ok || e != k {
			t.Fatal(ok, e)
		}
	}
	checkNoEvict := func() {
		t.Helper()
		e, ok := p.EvictSkip(skip)
		if ok {
			t.Fatal(e)
		}
	}

	p.Add(1)
	p.Add(2)
	checkEvict(1)
	p.Add(3)
	checkEvict(3)
	p.Add(1)
	checkEvict(1)
	p.Add(2)
	checkNoEvict()
	p.Add(1)

	want := []int{2, 1}
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

	want := []int{9, 7, 8, 6, 5, 3, 4, 2}
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

func TestARC_AddExisting(t *testing.T) {
	t.Parallel()

	p := policy.NewARC[int]()

	p.Add(1)
	p.Add(2)
	p.Promote(1)

	check := func() {
		all := slices.Collect(p.Values())
		diffFatal(t, []int{2, 1}, all)

		got := p.ARCParams()
		diffFatal(t, policy.ARCParams{T1Len: 1, T2Len: 1}, got)
	}

	check()

	ok := p.Add(1)
	diffFatal(t, false, ok)
	ok = p.Add(2)
	diffFatal(t, false, ok)

	check()
}

func TestARC_PromoteMissing(t *testing.T) {
	t.Parallel()

	p := policy.NewARC[int]()

	ok := p.Promote(1)
	diffFatal(t, false, ok)
}

func TestARC_adaptWhileEmpty(t *testing.T) {
	t.Parallel()
	p := policy.NewARC[int]()

	p.Add(1)
	p.Add(2)
	p.Add(3)
	// evict them all (to b1)

	got := p.ARCParams()
	diffFatal(t, policy.ARCParams{T1Len: 3}, got)

	p.Evict()
	p.Evict()
	p.Evict()

	got = p.ARCParams()
	diffFatal(t, policy.ARCParams{B1Len: 3}, got)

	// t1+t2 = 0, add 1 back (b1 hit)
	p.Add(1)

	// ensuring we don't get NaN here for T1TargetFraction.
	got = p.ARCParams()
	diffFatal(t, policy.ARCParams{T2Len: 1, B1Len: 2, T1TargetFraction: 1}, got)
}

func TestARC_params(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		setup      func(*testing.T, *policy.ARC[int])
		wantParams policy.ARCParams
		wantItems  []int
	}{
		"add_single_item": {
			setup: func(t *testing.T, p *policy.ARC[int]) {
				p.Add(1)
			},
			wantParams: policy.ARCParams{T1Len: 1},
			wantItems:  []int{1},
		},
		"add_multiple_items_go_to_t1": {
			setup: func(t *testing.T, p *policy.ARC[int]) {
				p.Add(1)
				p.Add(2)
				p.Add(3)
			},
			wantParams: policy.ARCParams{T1Len: 3},
			wantItems:  []int{3, 2, 1},
		},
		"promote_moves_t1_to_t2": {
			setup: func(t *testing.T, p *policy.ARC[int]) {
				p.Add(1)
				p.Add(2)
				p.Add(3)
				p.Promote(2) // moves from t1 to t2
			},
			wantParams: policy.ARCParams{T1Len: 2, T2Len: 1},
			wantItems:  []int{3, 2, 1}, // 2 is now in t2
		},
		"promote_in_t2_moves_to_front": {
			setup: func(t *testing.T, p *policy.ARC[int]) {
				p.Add(1)
				p.Add(2)
				p.Promote(1) // t1 -> t2
				p.Promote(2) // t1 -> t2
				p.Promote(1) // move to front in t2
			},
			wantParams: policy.ARCParams{T2Len: 2},
			wantItems:  []int{1, 2}, // 1 promoted to front
		},
		"evict_from_t1_goes_to_b1": {
			setup: func(t *testing.T, p *policy.ARC[int]) {
				p.Add(1)
				p.Add(2)
				p.Evict()
			},
			wantParams: policy.ARCParams{T1Len: 1, B1Len: 1},
			wantItems:  []int{2},
		},
		"evict_from_t2_goes_to_b2": {
			setup: func(t *testing.T, p *policy.ARC[int]) {
				p.Add(1)
				p.Add(2)
				p.Promote(1) // to t2
				p.Promote(2) // to t2
				p.Evict()    // evicts from t2
			},
			wantParams: policy.ARCParams{T2Len: 1, B2Len: 1},
			wantItems:  []int{2},
		},
		"b1_hit_adds_to_t2": {
			setup: func(t *testing.T, p *policy.ARC[int]) {
				p.Add(1)
				p.Add(2)
				p.Evict() // to b1
				p.Add(1)  // b1 hit: 1 goes to t2
			},
			wantParams: policy.ARCParams{T1Len: 1, T2Len: 1, T1TargetFraction: 1},
			wantItems:  []int{2, 1},
		},
		"b2_hit_adds_to_t2": {
			setup: func(t *testing.T, p *policy.ARC[int]) {
				p.Add(1)
				p.Promote(1) // to t2
				p.Evict()    // to b2
				p.Add(1)     // b2 hit: 1 goes to t2
			},
			wantParams: policy.ARCParams{T2Len: 1},
			wantItems:  []int{1},
		},
		"mixed_with_recency_and_frequency": {
			setup: func(t *testing.T, p *policy.ARC[int]) {
				p.Add(1)
				p.Add(2)
				p.Promote(1) // t1 [2], t2 [1]
				p.Promote(1)
				p.Add(3)     // t1 [3,2]
				p.Promote(2) // t1 [3], t2 [2,1]
			},
			wantParams: policy.ARCParams{T1Len: 1, T2Len: 2},
			wantItems:  []int{3, 2, 1},
		},
	}

	for n, c := range cases {
		t.Run(n, func(t *testing.T) {
			t.Parallel()
			p := policy.NewARC[int]()

			c.setup(t, p)

			gotParams := p.ARCParams()
			diffFatal(t, c.wantParams, gotParams)

			got := slices.Collect(p.Values())
			diffFatal(t, c.wantItems, got)
		})
	}
}

func diffFatal(t testing.TB, want, got any, opts ...cmp.Option) {
	t.Helper()
	if d := cmp.Diff(want, got, opts...); d != "" {
		t.Fatalf("(-want +got):\n%v", d)
	}
}
