# cache

[![Go Reference](https://pkg.go.dev/badge/github.com/graxinc/cache.svg)](https://pkg.go.dev/github.com/graxinc/cache)

## Purpose

Memory caching with the following focus:
- High concurrent throughput
- Size bounding using individual item sizes
- High hit ratio with varying concurrent access patterns
- Reference tracking for shared resource cleanup

## Usage

### Cache

Simple usage is a `Get` and `Set`:
```
a := cache.NewCache(cache.CacheOptions[string, int]{Capacity: 100})

v, ok := a.Get("hello")
if !ok {
    v = makeExpensiveValue()
    a.Set("hello", v)
}

// use v
```

### Counting
`counting.Cache` tracks Release calls until all Get callers are done with their fetched value. Useful for reused buffers or data that needs cleanup:

```
type value struct {
  data []byte
}

func (v value) Release() {
  dataPool.Put(v.data)
}

a := counting.NewCache(counting.CacheOptions[string, int]{})

v, ok := a.Get("key1")
if !ok {
    // load data in reused buffer
    d := dataPool.Get()
    d = append(d[:0], ...key1 data...)

    v = a.Set("key1", value{data: d})
}
defer v.Release()

// use v.Value().data
```

Usages that require another structure outside the cache (perhaps ordered lists) could use `Promote` and `CacheOptions.Evict`.

`SetS` is available when individual value sizes are known.

`SetLargerCapacity` is available for cases when the cache is representing a value outside memory (such as the filesystem).

## Design

### Cache

For both `Get` and `Set` calls, the map / policy split combined with the atomic guarantee on map add allows separation of their operations for less blocking and lower contention.
For `Get` we skip the policy promotion on contention, however on `Set` the policy add is never skipped to avoid trashing the ARC policy's tuning.

### Counting

`counting.Cache` uses atomic counters and booleans with optimistic compare-and-swap loops to track `Release`s of returned `Handle`s.

## Improvements

- Remaining contention within policy add.
  - Any minor optimizations within the ARC policy would reduce time and thus improve contention.
  - Ability to peek a slice of evictions in the policy could allow the cache to use a single lock for the eviction loop.
- Tests and benchmarks are light in a few places.
- counting.Node.Handle() is a significant source of garbage.
