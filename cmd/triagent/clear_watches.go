// Package cmd hosts launcher CLI subcommands (split out so tests can exercise
// flag parsing without booting the full server).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/sourcehawk/triagent/internal/watches"
)

// ClearWatches runs `triagent clear watches ...` against the on-disk
// JSONL files, no manager required. Returns the exit code.
func ClearWatches(args []string) int {
	fs := flag.NewFlagSet("clear watches", flag.ContinueOnError)
	watch := fs.String("watch", "", "scope to one watch id")
	items := fs.Bool("items", false, "clear items only")
	signals := fs.Bool("signals", false, "clear signals only")
	older := fs.String("older-than", "", "duration (e.g. 30d, 7d, 24h)")
	yes := fs.Bool("yes", false, "skip confirmation prompt")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if !*items && !*signals {
		*items, *signals = true, true
	}
	var dur time.Duration
	if *older != "" {
		d, err := clearParseDurationWithDays(*older)
		if err != nil {
			fmt.Fprintln(os.Stderr, "invalid --older-than:", err)
			return 2
		}
		dur = d
	}
	if !*yes && *watch == "" && *older == "" {
		fmt.Print("This wipes ALL watches' logs. Continue? [y/N] ")
		var resp string
		_, _ = fmt.Scanln(&resp)
		if resp != "y" && resp != "Y" {
			return 1
		}
	}
	path, err := watches.DefaultUserWatchesPath()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	root := filepath.Dir(path)
	ws, err := watches.LoadWatches(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	now := time.Now().UTC()
	totalI, totalS := 0, 0
	for _, w := range ws {
		if *watch != "" && w.ID != *watch {
			continue
		}
		dir := filepath.Join(root, "watches", w.ID)
		if *items {
			n, err := truncOlder(filepath.Join(dir, "items.jsonl"), dur, now, true)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			totalI += n
		}
		if *signals {
			n, err := truncOlder(filepath.Join(dir, "signals.jsonl"), dur, now, false)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			totalS += n
		}
	}
	fmt.Printf("cleared %d items, %d signals\n", totalI, totalS)
	return 0
}

// clearParseDurationWithDays is a local copy of parseDurationWithDays kept
// decoupled from the server import graph.
func clearParseDurationWithDays(s string) (time.Duration, error) {
	if len(s) > 1 && s[len(s)-1] == 'd' {
		var days int
		_, err := fmt.Sscanf(s, "%dd", &days)
		if err != nil {
			return 0, err
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

func truncOlder(path string, dur time.Duration, now time.Time, isItems bool) (int, error) {
	if dur == 0 {
		// Wipe the file entirely.
		if isItems {
			before, _ := watches.ReadItems(path, watches.ReadOpts{Limit: 1 << 30, IncludeFiltered: true})
			return len(before), os.Remove(path)
		}
		before, _ := watches.ReadSignals(path, watches.ReadOpts{Limit: 1 << 30})
		return len(before), os.Remove(path)
	}
	// Older-than: re-write keeping recent.
	if isItems {
		before, _ := watches.ReadItems(path, watches.ReadOpts{Limit: 1 << 30, IncludeFiltered: true})
		cutoff := now.Add(-dur)
		kept := before[:0]
		for _, it := range before {
			if !it.CapturedAt.Before(cutoff) {
				kept = append(kept, it)
			}
		}
		if err := writeItemsAtomic(path, kept); err != nil {
			return 0, err
		}
		return len(before) - len(kept), nil
	}
	before, _ := watches.ReadSignals(path, watches.ReadOpts{Limit: 1 << 30})
	cutoff := now.Add(-dur)
	kept := before[:0]
	for _, s := range before {
		if !s.CreatedAt.Before(cutoff) {
			kept = append(kept, s)
		}
	}
	if err := writeSignalsAtomic(path, kept); err != nil {
		return 0, err
	}
	return len(before) - len(kept), nil
}

func writeItemsAtomic(path string, items []watches.Item) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	for _, it := range items {
		if err := enc.Encode(it); err != nil {
			_ = f.Close()
			return err
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func writeSignalsAtomic(path string, signals []watches.Signal) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	for _, s := range signals {
		if err := enc.Encode(s); err != nil {
			_ = f.Close()
			return err
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
