package util

import (
	"container/list"
)

type CacheNode struct {
	Data int
	Key  *list.Element
}

type LRUCache struct {
	Capacity int
	Queue    *list.List
	Map      map[int]*CacheNode
}

// NewLRUCache creates a new LRUCache
func NewLRUCache(capacity int) *LRUCache {
	return &LRUCache{
		Capacity: capacity,
		Queue:    list.New(),
		Map:      make(map[int]*CacheNode),
	}
}

// Get returns the value of the key if the key exists, otherwise returns nil
func (this *LRUCache) Get(key int) *CacheNode {
	if node, ok := this.Map[key]; ok {
		this.Queue.MoveToFront(node.Key)
		return node
	}
	return nil
}

// Put inserts the key-value pair into the cache
func (this *LRUCache) Put(key int, value int) {
	if node, ok := this.Map[key]; ok {
		node.Data = value
		this.Queue.MoveToFront(node.Key)
	} else {
		if this.Queue.Len() == this.Capacity {
			delete(this.Map, this.Queue.Back().Value.(int))
			this.Queue.Remove(this.Queue.Back())
		}
		node := &CacheNode{Data: value, Key: nil}
		node.Key = this.Queue.PushFront(key)
		this.Map[key] = node
	}
}
