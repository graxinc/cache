package policy

import (
	"iter"
	"math"

	"github.com/graxinc/cache/policy/internal"
)

// Based on https://github.com/dgryski/go-arc / https://github.com/hashicorp/golang-lru.
// Algo overview at https://en.wikipedia.org/wiki/Adaptive_replacement_cache.
// Details at https://www2.cs.uh.edu/~paris/7360/PAPERS03/arcfast.pdf.

// Not concurrent safe.
type Policy[T any] interface {
	Clear()
	Promote(T) (exists bool)
	Evict() (_ T, ok bool)
	EvictSkip(skip func(T) bool) (_ T, ok bool)

	// !ok if already exists.
	Add(T) (ok bool)

	// Hottest to coldest.
	// Safe for RLock.
	Values() iter.Seq[T]

	Stats() map[string]any
}

type ARC[T comparable] struct {
	t1 internal.KeyList[T]
	t2 internal.KeyList[T]
	b1 internal.KeyList[T]
	b2 internal.KeyList[T]

	t1TargetFraction float64 // of t1+t2 space, 0-1.
}

func NewARC[T comparable]() *ARC[T] {
	return &ARC[T]{
		t1: internal.NewKeyList[T](),
		t2: internal.NewKeyList[T](),
		b1: internal.NewKeyList[T](),
		b2: internal.NewKeyList[T](),
	}
}

func (c *ARC[T]) Clear() {
	c.t1.Clear()
	c.t2.Clear()
	c.b1.Clear()
	c.b2.Clear()
	c.t1TargetFraction = 0
}

func (c *ARC[T]) Values() iter.Seq[T] {
	// ping pong between recent/frequent.

	return func(yield func(T) bool) {
		t1Next, stop := iter.Pull(c.t1.AllForward())
		defer stop()
		t2Next, stop := iter.Pull(c.t2.AllForward())
		defer stop()

		for {
			t1, ok1 := t1Next()
			if ok1 && !yield(t1) {
				return
			}
			t2, ok2 := t2Next()
			if ok2 && !yield(t2) {
				return
			}
			if !ok1 && !ok2 {
				return
			}
		}
	}
}

func (c *ARC[T]) Promote(key T) bool {
	// t2 first, frequent by definition so more likely.

	if elt := c.t2.Lookup(key); elt != nil {
		c.t2.MoveToFront(elt)
		return true
	}
	if elt := c.t1.Lookup(key); elt != nil {
		c.t1.Remove(elt)
		c.t2.PushFront(elt, key)
		return true
	}
	return false
}

func (c *ARC[T]) EvictSkip(skip func(T) bool) (evicted T, ok bool) {
	tRemove := func(tList, bList internal.KeyList[T]) (T, bool) {
		for elm := range tList.AllReverse() {
			if skip(elm.Value) {
				continue
			}
			tList.Remove(elm)
			bList.PushFront(elm, elm.Value)
			return elm.Value, true
		}
		var zero T
		return zero, false
	}
	if c.t1.Len() > 0 && (c.t1.Len() > c.t1TargetLen() || c.t2.Len() == 0) {
		e, ok := tRemove(c.t1, c.b1)
		if ok {
			return e, true
		}
	}
	return tRemove(c.t2, c.b2)
}

func (c *ARC[T]) Evict() (evicted T, ok bool) {
	skip := func(T) bool { return false }
	return c.EvictSkip(skip)
}

func (c *ARC[T]) Add(key T) (ok bool) {
	// t2/b2 first, frequent by definition so more likely.

	if c.t2.Has(key) || c.t1.Has(key) {
		return false
	}

	// ghost lists check, adapts t1TargetFraction.
	if elt := c.b2.Lookup(key); elt != nil {
		c.b2Hit()
		c.b2.Remove(elt)
		c.t2.PushFront(elt, key)
		return true
	}
	if elt := c.b1.Lookup(key); elt != nil {
		c.b1Hit()
		c.b1.Remove(elt)
		c.t2.PushFront(elt, key)
		return true
	}

	var removed *internal.Element[T]

	// trim b tails, since total b+t increasing.
	// since t slides within t+b, b1 gets smaller as t1 gets bigger.

	t := c.t1TargetLen()
	for c.b1.Len() > c.tLen()-t && c.b1.Len() > 0 {
		removed = c.b1.RemoveTail()
	}
	for c.b2.Len() > t && c.b2.Len() > 0 {
		removed = c.b2.RemoveTail()
	}
	c.t1.PushFront(removed, key)
	return true
}

type ARCParams struct {
	T1Len, T2Len     int
	B1Len, B2Len     int
	T1TargetFraction float64
}

func (c *ARC[T]) ARCParams() ARCParams {
	return ARCParams{
		T1Len:            c.t1.Len(),
		T2Len:            c.t2.Len(),
		B1Len:            c.b1.Len(),
		B2Len:            c.b2.Len(),
		T1TargetFraction: c.t1TargetFraction,
	}
}

func (c *ARC[T]) Stats() map[string]any {
	p := c.ARCParams()
	return map[string]any{
		"t1Len":            p.T1Len,
		"t2Len":            p.T2Len,
		"b1Len":            p.B1Len,
		"b2Len":            p.B2Len,
		"t1TargetFraction": p.T1TargetFraction,
	}
}

// b1 must not be empty.
func (c *ARC[T]) b1Hit() {
	delta := 1.0
	if b1, b2 := c.b1.Len(), c.b2.Len(); b2 > b1 {
		delta = float64(b2) / float64(b1)
	}
	c.setT1TargetFraction(delta)
}

// b2 must not be empty.
func (c *ARC[T]) b2Hit() {
	delta := 1.0
	if b1, b2 := c.b1.Len(), c.b2.Len(); b1 > b2 {
		delta = float64(b1) / float64(b2)
	}
	c.setT1TargetFraction(-delta)
}

func (c *ARC[T]) t1TargetLen() int {
	t := c.t1TargetFraction * float64(c.tLen())
	return int(math.RoundToEven(t))
}

// delta must not be zero.
func (c *ARC[T]) setT1TargetFraction(delta float64) {
	tLen := float64(c.tLen())

	// t1TargetLen = t1TargetFraction * tLen
	// t1TargetFraction = ((t1TargetLen + delta) / tLen), simplifies to:
	v := c.t1TargetFraction + (delta / tLen)

	// bound 0-1.
	v = min(max(v, 0), 1)
	c.t1TargetFraction = v
}

func (c *ARC[T]) tLen() int {
	return c.t1.Len() + c.t2.Len()
}
