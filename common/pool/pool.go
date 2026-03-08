package pool

import (
	"sync"
)

const (
	Small  = 1 << 10  // 1KB
	Medium = 8 << 10  // 8KB
	Large  = 32 << 10 // 32KB
	Huge   = 64 << 10 // 64KB
)

var (
	smallPool = sync.Pool{
		New: func() any { b := make([]byte, Small); return &b },
	}
	mediumPool = sync.Pool{
		New: func() any { b := make([]byte, Medium); return &b },
	}
	largePool = sync.Pool{
		New: func() any { b := make([]byte, Large); return &b },
	}
	hugePool = sync.Pool{
		New: func() any { b := make([]byte, Huge); return &b },
	}
)

// Get returns a buffer pointer of at least the given size from the pool.
func Get(size int) *[]byte {
	switch {
	case size <= Small:
		return smallPool.Get().(*[]byte)
	case size <= Medium:
		return mediumPool.Get().(*[]byte)
	case size <= Large:
		return largePool.Get().(*[]byte)
	case size <= Huge:
		return hugePool.Get().(*[]byte)
	default:
		b := make([]byte, size)
		return &b
	}
}

// Put returns a buffer to the appropriate pool.
func Put(b *[]byte) {
	if b == nil {
		return
	}
	size := cap(*b)
	switch {
	case size == Small:
		smallPool.Put(b)
	case size == Medium:
		mediumPool.Put(b)
	case size == Large:
		largePool.Put(b)
	case size == Huge:
		hugePool.Put(b)
	// Non-standard sizes are not returned to pool
	}
}

// GetSlice is a convenience wrapper that returns a byte slice of the requested size.
func GetSlice(size int) (*[]byte, []byte) {
	bp := Get(size)
	return bp, (*bp)[:size]
}
