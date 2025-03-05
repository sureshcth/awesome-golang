package util

import (
	"container/list"
	"strconv"
)

type Deque struct {
	items *list.List
}

// NewDeque creates a new deque
func NewQueue() *Deque {
	return &Deque{items: list.New()}
}

// PushFront pushes an element to the front of the deque
func (d *Deque) PushFront(item interface{}) {
	d.items.PushFront(item)
}

// PushBack pushes an element to the back of the deque
func (d *Deque) PushBack(item interface{}) {
	d.items.PushBack(item)
}

// PopFront pops an element from the front of the deque
func (d *Deque) PopFront() interface{} {
	if d.items.Len() == 0 {
		return nil
	}
	return d.items.Remove(d.items.Front())
}

// PopBack pops an element from the back of the deque
func (d *Deque) PopBack() interface{} {
	if d.items.Len() == 0 {
		return nil
	}
	return d.items.Remove(d.items.Back())
}

// Front returns the front element of the deque
func (d *Deque) Front() interface{} {
	if d.items.Len() == 0 {
		return nil
	}
	return d.items.Front().Value
}

// Back returns the back element of the deque
func (d *Deque) Back() interface{} {
	if d.items.Len() == 0 {
		return nil
	}
	return d.items.Back().Value
}

// Empty returns true if the deque is empty
func (d *Deque) Empty() bool {
	return d.items.Len() == 0
}

// Size returns the size of the deque
func (d *Deque) Size() int {
	return d.items.Len()
}

// Clear clears the deque
func (d *Deque) Clear() {
	d.items.Init()
}

// Print prints the deque
func (d *Deque) Print() {
	tmp := d.items.Front()
	s := "["
	for tmp != nil {
		temp2 := tmp.Value.(int)
		s += strconv.Itoa(temp2) + " "
		tmp = tmp.Next()
		if tmp != nil {
			s += "->"
		}
	}
	for e := d.items.Front(); e != nil; e = e.Next() {
		//Print(e.Value)
	}
}
