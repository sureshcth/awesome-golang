package util

type Entry[K comparable, V any] struct {
	Key     K
	Value   V
	element *Element[*Entry[K, V]]
}
