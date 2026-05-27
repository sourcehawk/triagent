package watches

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// ItemRetention is the rolling window for items.jsonl. Pruning runs inline
// after every successful poll cycle (see Poller). Launcher-wide constant
// in phase 1; per-watch override is §12 out-of-scope.
const ItemRetention = 7 * 24 * time.Hour

// fileMu serializes writes per-path to keep append + atomic-rewrite from
// stomping each other. One mutex per launcher invocation is fine — log
// writes are coarse-grained and infrequent.
var fileMu = struct {
	sync.Mutex
	m map[string]*sync.Mutex
}{m: map[string]*sync.Mutex{}}

func lockFor(path string) *sync.Mutex {
	fileMu.Lock()
	defer fileMu.Unlock()
	if mu, ok := fileMu.m[path]; ok {
		return mu
	}
	mu := &sync.Mutex{}
	fileMu.m[path] = mu
	return mu
}

// ReadOpts controls ReadItems / ReadSignals pagination + filter visibility.
type ReadOpts struct {
	Limit           int       // 0 = no cap
	Before          time.Time // exclusive; zero = no cursor
	IncludeFiltered bool      // items only — see ReadItems
}

// AppendItem writes one Item as a JSONL row. Creates the file (and any
// parent dirs) when missing. Concurrency-safe per path.
func AppendItem(path string, it Item) error {
	mu := lockFor(path)
	mu.Lock()
	defer mu.Unlock()
	return appendOne(path, it)
}

// ReadItems returns items newest-first. Filtered items are excluded
// unless opts.IncludeFiltered=true.
func ReadItems(path string, opts ReadOpts) ([]Item, error) {
	mu := lockFor(path)
	mu.Lock()
	defer mu.Unlock()
	rows, err := readAll(path, func() any { return &Item{} })
	if err != nil {
		return nil, err
	}
	items := make([]Item, 0, len(rows))
	for _, r := range rows {
		it := *(r.(*Item))
		if !opts.IncludeFiltered && it.Filtered != nil {
			continue
		}
		if !opts.Before.IsZero() && !it.CapturedAt.Before(opts.Before) {
			continue
		}
		items = append(items, it)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CapturedAt.After(items[j].CapturedAt) })
	if opts.Limit > 0 && len(items) > opts.Limit {
		items = items[:opts.Limit]
	}
	return items, nil
}

// PruneItems rewrites items.jsonl keeping only entries with
// CapturedAt >= now - retention. Atomic rename. Called inline by the
// poller at the end of each successful poll cycle.
func PruneItems(path string, now time.Time, retention time.Duration) error {
	mu := lockFor(path)
	mu.Lock()
	defer mu.Unlock()
	rows, err := readAll(path, func() any { return &Item{} })
	if err != nil {
		return err
	}
	cutoff := now.Add(-retention)
	out := make([]Item, 0, len(rows))
	for _, r := range rows {
		it := *(r.(*Item))
		if !it.CapturedAt.Before(cutoff) {
			out = append(out, it)
		}
	}
	return rewriteJSONL(path, out)
}

// AppendSignal writes one Signal as a JSONL row. Mutations re-use the
// signal id; ReadSignals collapses to latest-row-per-id.
func AppendSignal(path string, s Signal) error {
	mu := lockFor(path)
	mu.Lock()
	defer mu.Unlock()
	return appendOne(path, s)
}

// ReadSignals returns signals newest-first, projecting latest-row-per-id.
func ReadSignals(path string, opts ReadOpts) ([]Signal, error) {
	mu := lockFor(path)
	mu.Lock()
	defer mu.Unlock()
	rows, err := readAll(path, func() any { return &Signal{} })
	if err != nil {
		return nil, err
	}
	latest := map[string]Signal{}
	for _, r := range rows {
		s := *(r.(*Signal))
		prev, ok := latest[s.ID]
		if !ok || s.CreatedAt.After(prev.CreatedAt) {
			latest[s.ID] = s
		}
	}
	out := make([]Signal, 0, len(latest))
	for _, s := range latest {
		if !opts.Before.IsZero() && !s.CreatedAt.Before(opts.Before) {
			continue
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if opts.Limit > 0 && len(out) > opts.Limit {
		out = out[:opts.Limit]
	}
	return out, nil
}

// ---- internal helpers shared with signal log ----

func appendOne(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		return err
	}
	return nil
}

func readAll(path string, factory func() any) ([]any, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 8*1024*1024)
	out := []any{}
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		v := factory()
		if err := json.Unmarshal(line, v); err != nil {
			return nil, fmt.Errorf("parse line: %w", err)
		}
		out = append(out, v)
	}
	if err := sc.Err(); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return out, nil
}

func rewriteJSONL(path string, items any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	switch v := items.(type) {
	case []Item:
		for _, x := range v {
			if err := enc.Encode(x); err != nil {
				_ = f.Close()
				return err
			}
		}
	case []Signal:
		for _, x := range v {
			if err := enc.Encode(x); err != nil {
				_ = f.Close()
				return err
			}
		}
	default:
		_ = f.Close()
		return fmt.Errorf("rewriteJSONL: unknown slice type %T", items)
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
