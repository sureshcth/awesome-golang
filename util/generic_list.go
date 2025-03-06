package util

// Element is an element of a linked list.
type Element[V any] struct {
	next, prev *Element[V]
	list       *List[V]
	Value      V
}

// Next returns the next list element or nil.
func (e *Element[V]) Next() *Element[V] {
	if p := e.next; e.list != nil && p != &e.list.root {
		return p
	}
	return nil
}

// Prev returns the previous list element or nil.
func (e *Element[V]) Prev() *Element[V] {
	if p := e.prev; e.list != nil && p != &e.list.root {
		return p
	}
	return nil
}

// List represents a doubly linked list.
// The zero value for List is an empty list ready to use.
type List[V any] struct {
	root Element[V]
	len  int
}

// Init initializes or clears list l.
func (l *List[V]) Init() *List[V] {
	l.root.next = &l.root
	l.root.prev = &l.root
	l.len = 0
	return l
}

// NewList returns an initialized list.
func NewList[V any]() *List[V] {
	return new(List[V]).Init()
}

// Len returns the number of elements of list l.
func (l *List[V]) Len() int {
	return l.len
}

// Front returns the first element of list l or nil if the list is empty.
func (l *List[V]) Front() *Element[V] {
	if l.len == 0 {
		return nil
	}
	return l.root.next
}

// Back returns the last element of list l or nil if the list is empty.
func (l *List[V]) Back() *Element[V] {
	if l.len == 0 {
		return nil
	}
	return l.root.prev
}

// insert inserts e after at, increments l.len, and returns e.
func (l *List[V]) insert(e, at *Element[V]) *Element[V] {
	e.prev = at.prev
	e.next = at
	e.prev.next = e
	e.next.prev = e
	e.list = l
	l.len++
	return e
}

// remove removes e from its list, decrements l.len, and returns e.
func (l *List[V]) remove(e *Element[V]) {
	e.prev.next = e.next
	e.next.prev = e.prev
	e.next = nil
	e.prev = nil
	e.list = nil
	l.len--
}

// move moves e to next to at and returns e.
func (l *List[V]) move(e, at *Element[V]) {
	if e == at {
		return
	}
	e.prev.next = e.next
	e.next.prev = e.prev

	e.prev = at.prev
	e.next = at
	e.prev.next = e
	e.next.prev = e
}

// PushBack inserts a new element e with value v at the back of list l and returns e.
func (l *List[V]) InsertValue(v V, at *Element[V]) *Element[V] {
	return l.insert(&Element[V]{Value: v}, at)
}

// Remove removes e from l if e is an element of list l.
func (l *List[V]) Remove(e *Element[V]) {
	if e.list == l {
		l.remove(e)
	}
}

// lazyInit lazily initializes a zero List value.
func (l *List[V]) lazyInit() {
	if l.root.next == nil {
		l.Init()
	}
}

// PushFront inserts a new element e with value v at the front of list l and returns e.
func (l *List[V]) PushFront(v V) *Element[V] {
	l.lazyInit()
	return l.insert(&Element[V]{Value: v}, &l.root)
}

// PushBack inserts a new element e with value v at the back of list l and returns e.
func (l *List[V]) PushBack(v V) *Element[V] {
	l.lazyInit()
	return l.insert(&Element[V]{Value: v}, l.root.prev)
}

// InsertBefore inserts a new element e with value v immediately before mark and returns e.
// If mark is not an element of l, the list is not modified.
func (l *List[V]) InsertBefore(v V, mark *Element[V]) *Element[V] {
	if mark.list != l {
		return nil
	}
	return l.InsertValue(v, mark.prev)
}

// InsertAfter inserts a new element e with value v immediately after mark and returns e.
// If mark is not an element of l, the list is not modified.
func (l *List[V]) InsertAfter(v V, mark *Element[V]) *Element[V] {
	if mark.list != l {
		return nil
	}
	return l.InsertValue(v, mark)
}

// MoveToFront moves element e to the front of list l.
// If e is not an element of l, the list is not modified.
func (l *List[V]) MoveToFront(e *Element[V]) {
	if e.list != l || l.root.next == e {
		return
	}
	l.move(e, &l.root)
}

// MoveToBack moves element e to the back of list l.
// If e is not an element of l, the list is not modified.
func (l *List[V]) MoveToBack(e *Element[V]) {
	if e.list != l || l.root.prev == e {
		return
	}
	l.move(e, l.root.prev)
}

// MoveBefore moves element e to its new position before mark.
// If e or mark is not an element of l, or e == mark, the list is not modified.
func (l *List[V]) MoveBefore(e, mark *Element[V]) {
	if e.list != l || e == mark || mark.list != l {
		return
	}
	l.move(e, mark.prev)
}

// MoveAfter moves element e to its new position after mark.
// If e or mark is not an element of l, or e == mark, the list is not modified.
func (l *List[V]) MoveAfter(e, mark *Element[V]) {
	if e.list != l || e == mark || mark.list != l {
		return
	}
	l.move(e, mark)
}

// PushBackList inserts a copy of an other list at the back of list l.
func (l *List[V]) PushBackList(other *List[V]) {
	l.lazyInit()
	for i, e := other.Len(), other.Front(); i > 0; i, e = i-1, e.Next() {
		l.insert(&Element[V]{Value: e.Value}, l.root.prev)
	}
}

// PushFrontList inserts a copy of an other list at the front of list l.
func (l *List[V]) PushFrontList(other *List[V]) {
	l.lazyInit()
	for i, e := other.Len(), other.Back(); i > 0; i, e = i-1, e.Prev() {
		l.insert(&Element[V]{Value: e.Value}, &l.root)
	}
}
