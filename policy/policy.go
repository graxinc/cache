package policy

import (
	"iter"
	"math"

	"github.com/szyhf/go-container/list"
)

// Based on https://github.com/dgryski/go-arc / https://github.com/hashicorp/golang-lru.
// Algo overview at https://en.wikipedia.org/wiki/Adaptive_replacement_cache.
// Details at https://www2.cs.uh.edu/~paris/7360/PAPERS03/arcfast.pdf.

// Not concurrent safe.
type Policy[T any] interface {
	Clear()
	Promote(T) (exists bool)
	Evict() (_ T, ok bool)

	// !ok if already exists.
	Add(T) (ok bool)

	// Hottest to coldest.
	// Safe for RLock.
	Values() iter.Seq[T]
}

type clist[T comparable] struct {
	l    *list.List[T]
	keys map[T]*list.Element[T]
}

func makeClist[T comparable]() clist[T] {
	return clist[T]{
		l:    list.New[T](),
		keys: make(map[T]*list.Element[T]),
	}
}

func (c *clist[T]) Has(key T) bool {
	_, ok := c.keys[key]
	return ok
}

// do not mod result. might return nil.
func (c *clist[T]) Lookup(key T) *list.Element[T] {
	return c.keys[key]
}

func (c *clist[T]) MoveToFront(elt *list.Element[T]) {
	c.l.MoveToFront(elt)
}

func (c *clist[T]) PushFront(key T) {
	c.keys[key] = c.l.PushFront(key)
}

func (c *clist[T]) Remove(elt *list.Element[T]) {
	delete(c.keys, elt.Value)
	c.l.Remove(elt)
}

// list must not be empty.
func (c *clist[T]) RemoveTail() T {
	elt := c.l.Back()
	c.Remove(elt)
	return elt.Value
}

func (c *clist[T]) Len() int {
	return c.l.Len()
}

func (c *clist[T]) Clear() {
	c.l.Init()
	clear(c.keys)
}

func (c *clist[T]) All() iter.Seq[T] {
	return func(yield func(T) bool) {
		for e := c.l.Front(); e != nil; e = e.Next() {
			if !yield(e.Value) {
				return
			}
		}
	}
}

type ARC[T comparable] struct {
	// immutable
	t1 clist[T]
	t2 clist[T]
	b1 clist[T]
	b2 clist[T]

	f float64
}

func NewARC[T comparable]() *ARC[T] {
	return &ARC[T]{
		t1: makeClist[T](),
		t2: makeClist[T](),
		b1: makeClist[T](),
		b2: makeClist[T](),
	}
}

func (c *ARC[T]) Clear() {
	c.t1.Clear()
	c.t2.Clear()
	c.b1.Clear()
	c.b2.Clear()
	c.f = 0
}

func (c *ARC[T]) Values() iter.Seq[T] {
	return func(yield func(T) bool) {
		for v := range c.t2.All() {
			if !yield(v) {
				return
			}
		}
		for v := range c.t1.All() {
			if !yield(v) {
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
		c.t2.PushFront(key)
		return true
	}
	return false
}

func (c *ARC[T]) Evict() (evicted T, ok bool) {
	if c.t1.Len() > 0 && (c.t1.Len() > c.t1TargetLen() || c.t2.Len() == 0) {
		e := c.t1.RemoveTail()
		c.b1.PushFront(e)
		return e, true
	}
	if c.t2.Len() > 0 {
		e := c.t2.RemoveTail()
		c.b2.PushFront(e)
		return e, true
	}
	var zero T
	return zero, false
}

func (c *ARC[T]) Add(key T) (ok bool) {
	// t2/b2 first, frequent by definition so more likely.

	if c.t2.Has(key) || c.t1.Has(key) {
		return false
	}

	if elt := c.b2.Lookup(key); elt != nil {
		c.b2Hit()
		c.b2.Remove(elt)
		c.t2.PushFront(key)
		return true
	}
	if elt := c.b1.Lookup(key); elt != nil {
		c.b1Hit()
		c.b1.Remove(elt)
		c.t2.PushFront(key)
		return true
	}

	// trim b tails, since total b+t increasing.
	t := c.t1TargetLen()
	for c.b1.Len() > c.tLen()-t && c.b1.Len() > 0 {
		c.b1.RemoveTail()
	}
	for c.b2.Len() > t && c.b2.Len() > 0 {
		c.b2.RemoveTail()
	}
	c.t1.PushFront(key)
	return true
}

// b1 must not be empty.
func (c *ARC[T]) b1Hit() {
	delta := 1
	if b1, b2 := c.b1.Len(), c.b2.Len(); b2 > b1 {
		delta = b2 / b1
	}
	c.setF(delta)
}

// b2 must not be empty.
func (c *ARC[T]) b2Hit() {
	delta := 1
	if b1, b2 := c.b1.Len(), c.b2.Len(); b1 > b2 {
		delta = b1 / b2
	}
	c.setF(-delta)
}

func (c *ARC[T]) t1TargetLen() int {
	f := math.RoundToEven(c.f * float64(c.tLen()))
	return int(f)
}

func (c *ARC[T]) setF(delta int) {
	t := c.t1TargetLen()
	t += delta
	t = min(max(t, 0), c.tLen()) // bound
	c.f = float64(t) / float64(c.tLen())
}

func (c *ARC[T]) tLen() int {
	return c.t1.Len() + c.t2.Len()
}
