# cache

[![Go Reference](https://pkg.go.dev/badge/github.com/graxinc/cache.svg)](https://pkg.go.dev/github.com/graxinc/cache)

## Purpose

Memory caching with the following focus:
- High concurrent throughput
- Size bounding using individual item sizes
- High hit ratio with varying concurrent access patterns

## Usage

Simple usage is a `Set` and `Get`:
```
a := cacheutil.NewCache(cacheutil.CacheOptions[string, int]{Capacity: 100})

a.Set("hello", 5)

v, ok := a.Get("hello")
if ok {
    // use v
}
```

Usages that require another structure outside the cache (perhaps ordered lists) could use `Promote` and `CacheOptions.Evict`.
`SetS` is available when individual value sizes are known. 

## Design

For both `Get` and `Set` calls, the map / policy split combined with the atomic guarantee on map add allows separation of their operations for less blocking and lower contention.
For `Get` we skip the policy promotion on contention, however on `Set` the policy add is never skipped to avoid trashing the ARC policy's tuning.

## Improvements

- Remaining contention within policy add.
  - Any minor optimizations within the ARC policy would reduce time and thus improve contention.
  - Ability to peek a slice of evictions in the policy could allow the cache to use a single lock for the eviction loop.
- Tests and benchmarks are light in a few places.
