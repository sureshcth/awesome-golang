package heap

import (
	"container/heap"
)

type IntHeap []int

// newHeap function returns a new MaxHeap
func newHeap() *IntHeap {
	h := &IntHeap{}
	heap.Init(h)
	return h
}

// Len function returns the length of MaxHeap
func (h IntHeap) Len() int {
	return len(h)
}

// Less function compares two elements of MaxHeap given their indices
func (h IntHeap) Less(i, j int) bool {
	return h[i] > h[j]
}

// Swap function swaps two elements of MaxHeap given their indices
func (h IntHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
}

// Push function pushes an element to MaxHeap
func (h *IntHeap) Push(x interface{}) {
	*h = append(*h, x.(int))
}

// Pop function pops an element from MaxHeap
func (h *IntHeap) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[0 : n-1]
	return x
}

// Top function returns the top element of MaxHeap
func (h *IntHeap) Top() interface{} {
	return (*h)[0]
}

// Empty function returns true if MaxHeap is empty
func (h *IntHeap) Empty() bool {
	return len(*h) == 0
}
