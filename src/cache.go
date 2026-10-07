package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Org and repository lists rarely change but are slow to fetch, so they are
// cached on disk for a day. --refresh bypasses (and rewrites) the cache.
const cacheTTL = 24 * time.Hour

// refreshCache is set by --refresh to ignore any cached value.
var refreshCache bool

type cacheFile[T any] struct {
	FetchedAt time.Time
	Value     T
}

// cached returns the value stored under key if it is younger than cacheTTL,
// otherwise calls fetch and stores the result. Cache problems are never
// fatal: an unreadable or unwritable cache just means fetching every time.
func cached[T any](key string, fetch func() (T, error)) (T, error) {
	path := cachePath(key)
	if path != "" && !refreshCache {
		if raw, err := os.ReadFile(path); err == nil {
			var cf cacheFile[T]
			if json.Unmarshal(raw, &cf) == nil && time.Since(cf.FetchedAt) < cacheTTL {
				return cf.Value, nil
			}
		}
	}
	value, err := fetch()
	if err != nil {
		return value, err
	}
	if path != "" {
		if raw, err := json.Marshal(cacheFile[T]{time.Now(), value}); err == nil {
			if os.MkdirAll(filepath.Dir(path), 0o755) == nil {
				_ = os.WriteFile(path, raw, 0o644)
			}
		}
	}
	return value, nil
}

func cachePath(key string) string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	safe := strings.NewReplacer("/", "_", "\\", "_", ":", "_").Replace(strings.ToLower(key))
	return filepath.Join(dir, "fetch-pr-commentary", safe+".json")
}
