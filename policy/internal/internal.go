package internal

import (
	"iter"
)

// originally from github.com/szyhf/go-container/list

// Element is an element of a linked list.
type Element[T any] struct {
	// Next and previous pointers in the doubly-linked list of elements.
	// To simplify the implementation, internally a list l is implemented
	// as a ring, such that &l.root is both the next element of the last
	// list element (l.Back()) and the previous element of the first list
	// element (l.Front()).
	next, prev *Element[T]

	// The value stored with this element.
	Value T
}

// List represents a doubly linked list.
// The zero value for List is an empty list ready to use.
type List[T any] struct {
	root Element[T] // sentinel list element, only &root, root.prev, and root.next are used
	len  int        // current list length excluding (this) sentinel element
}

// Init initializes or clears list l.
func (l *List[T]) Init() *List[T] {
	l.root.next = &l.root
	l.root.prev = &l.root
	l.len = 0
	return l
}

func NewList[T any]() *List[T] {
	return new(List[T]).Init()
}

// Len returns the number of elements of list l.
// The complexity is O(1).
func (l *List[T]) Len() int { return l.len }

// Front returns the first element of list l or nil if the list is empty.
func (l *List[T]) Front() *Element[T] {
	if l.len == 0 {
		return nil
	}
	return l.root.next
}

// Back returns the last element of list l or nil if the list is empty.
func (l *List[T]) Back() *Element[T] {
	if l.len == 0 {
		return nil
	}
	return l.root.prev
}

// Next returns the next list element after e or nil.
func (l *List[T]) Next(e *Element[T]) *Element[T] {
	if p := e.next; p != &l.root {
		return p
	}
	return nil
}

// Prev returns the previous list element before e or nil.
func (l *List[T]) Prev(e *Element[T]) *Element[T] {
	if p := e.prev; p != &l.root {
		return p
	}
	return nil
}

// remove removes e from list, decrements l.len
func (l *List[T]) Remove(e *Element[T]) {
	e.prev.next = e.next
	e.next.prev = e.prev
	e.next = nil // avoid memory leaks
	e.prev = nil // avoid memory leaks
	l.len--
}

// PushFront inserts a new element or reset buf e with value v at the front of list l and returns e.
func (l *List[T]) PushFront(buf *Element[T], v T) *Element[T] {
	l.lazyInit()
	return l.insertValue(buf, v, &l.root)
}

// MoveToFront moves element e to the front of list l.
// If e is not an element of l, the list is not modified.
// The element must not be nil.
func (l *List[T]) MoveToFront(e *Element[T]) {
	if l.root.next == e {
		return
	}
	// see comment in List.Remove about initialization of l
	l.move(e, &l.root)
}

// lazyInit lazily initializes a zero List value.
func (l *List[T]) lazyInit() {
	if l.root.next == nil {
		l.Init()
	}
}

// insertValue is a convenience wrapper for insert(&Element{Value: v}, at).
func (l *List[T]) insertValue(buf *Element[T], v T, at *Element[T]) *Element[T] {
	if buf == nil {
		buf = &Element[T]{Value: v}
	} else {
		buf.next = nil
		buf.prev = nil
		buf.Value = v
	}
	return l.insert(buf, at)
}

// insert inserts e after at, increments l.len, and returns e.
func (l *List[T]) insert(e, at *Element[T]) *Element[T] {
	e.prev = at
	e.next = at.next
	e.prev.next = e
	e.next.prev = e
	l.len++
	return e
}

// move moves e to next to at.
func (l *List[T]) move(e, at *Element[T]) {
	if e == at {
		return
	}
	e.prev.next = e.next
	e.next.prev = e.prev

	e.prev = at
	e.next = at.next
	e.prev.next = e
	e.next.prev = e
}

type KeyList[T comparable] struct {
	l    *List[T]
	keys map[T]*Element[T]
}

func NewKeyList[T comparable]() KeyList[T] {
	return KeyList[T]{
		l:    NewList[T](),
		keys: make(map[T]*Element[T]),
	}
}

func (c *KeyList[T]) Has(key T) bool {
	_, ok := c.keys[key]
	return ok
}

// do not mod result. might return nil.
func (c *KeyList[T]) Lookup(key T) *Element[T] {
	return c.keys[key]
}

func (c *KeyList[T]) MoveToFront(elt *Element[T]) {
	c.l.MoveToFront(elt)
}

func (c *KeyList[T]) PushFront(buf *Element[T], key T) {
	c.keys[key] = c.l.PushFront(buf, key)
}

func (c *KeyList[T]) Remove(elt *Element[T]) {
	delete(c.keys, elt.Value)
	c.l.Remove(elt)
}

// list must not be empty.
func (c *KeyList[T]) RemoveTail() *Element[T] {
	elt := c.l.Back()
	c.Remove(elt)
	return elt
}

func (c *KeyList[T]) Len() int {
	return c.l.Len()
}

func (c *KeyList[T]) Clear() {
	c.l.Init()
	clear(c.keys)
}

func (c *KeyList[T]) AllReverse() iter.Seq[*Element[T]] {
	return func(yield func(*Element[T]) bool) {
		for e := c.l.Back(); e != nil; e = c.l.Prev(e) {
			if !yield(e) {
				return
			}
		}
	}
}

func (c *KeyList[T]) AllForward() iter.Seq[T] {
	return func(yield func(T) bool) {
		for e := c.l.Front(); e != nil; e = c.l.Next(e) {
			if !yield(e.Value) {
				return
			}
		}
	}
}
