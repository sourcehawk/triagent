package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// syncMode controls how aggressively Store.Sync walks the channel.
//
// `syncPeek` is the cheap first-look (default cap 200 parents). Used by
// `slack_channel_overview` when the agent is sizing up the channel.
//
// `syncFull` is the deep walk (default cap 2000 parents). Used by
// `analyze_channel` and `slack_search_messages`.
type syncMode int

const (
	syncPeek syncMode = iota
	syncFull
)

const (
	defaultPeekCap        = 200
	defaultAnalyzeCap     = 2000
	historyPageSize       = 999 // Slack trims to its per-token cap (15 for non-Marketplace); cooperative tokens get larger pages.
	repliesPageSize       = 1000
	metaFilename          = "meta.json"
	usersFilename         = "users.json"
	messagesFilename      = "messages.md"
	parentsFilename       = "parents.json" // structured backing for messages.md, used to rehydrate the cache on boot
	threadsDirname        = "threads"
)

// Store materialises the Slack channel content into a flat directory the
// sub-agent can read with `Read`/`Glob`/`Grep` only. The directory is
// rebuilt incrementally by repeated Sync calls within the lifetime of one
// launcher process. The store is per-channel; the Server holds a lazy
// map of stores keyed by channel id and creates them on demand.
type Store struct {
	dir string

	mu          sync.Mutex
	client      *Client
	channelID   string
	peekCap     int
	analyzeCap  int

	// in-memory state mirroring the on-disk artefacts. Populated lazily
	// by Sync; persisted to disk after each Sync call.
	parents     []Message
	threadCount map[string]int             // ts → reply_count last materialised
	threadMsgs  map[string][]Message       // ts → parent + replies
	users       map[string]UserSummary     // resolved user id → summary
}

// StoreOptions are the inputs NewStore needs. The Server fills in the
// remaining fields (client, channelID, …) directly after construction
// because they cross package-private boundaries cleanly.
type StoreOptions struct {
	// Root is the parent directory under which a per-channel folder is
	// created. Required.
	Root string
	// ChannelID becomes the leaf directory name under Root. Required.
	ChannelID string
}

// NewStore opens (or creates) the per-channel cache directory at mode
// 0700 and rehydrates any existing on-disk state. Wipe-on-boot was
// dropped in spec 2026-05-08-persistent-slack-cache-design.md — the
// cache now persists across launcher reboots so re-investigating a
// channel doesn't pay the cold-start sync cost. Operators clear the
// cache explicitly via `triagent clean`.
func NewStore(opts StoreOptions) (*Store, error) {
	if opts.Root == "" {
		return nil, fmt.Errorf("slack store: root dir is required")
	}
	if opts.ChannelID == "" {
		return nil, fmt.Errorf("slack store: channel id is required")
	}
	dir := filepath.Join(opts.Root, opts.ChannelID)
	if err := os.MkdirAll(filepath.Join(dir, threadsDirname), 0o700); err != nil {
		return nil, fmt.Errorf("slack store: mkdir %s: %w", dir, err)
	}
	parents, threadMsgs, threadCount, users := loadCache(dir, defaultAnalyzeCap)
	return &Store{
		dir:         dir,
		channelID:   opts.ChannelID,
		peekCap:     defaultPeekCap,
		analyzeCap:  defaultAnalyzeCap,
		parents:     parents,
		threadMsgs:  threadMsgs,
		threadCount: threadCount,
		users:       users,
	}, nil
}

// Dir returns the cache directory the sub-agent reads from. Used by
// runSubAgentReal to set the WorkingDir for `claude`.
func (s *Store) Dir() string { return s.dir }

// loadCache rehydrates the in-memory store maps from the on-disk JSON
// sidecars. Per-file failures (missing, malformed JSON) are non-fatal:
// the affected piece starts empty, the rest loads. This means a
// partially-corrupt cache degrades to a smaller cache, never to a hard
// error that prevents the launcher from booting.
//
// parentsCap caps the loaded parents slice — if on-disk state is larger
// than the configured analyzeCap (e.g. cap was lowered between boots),
// keep only the newest cap-many entries.
func loadCache(dir string, parentsCap int) (parents []Message, threadMsgs map[string][]Message, threadCount map[string]int, users map[string]UserSummary) {
	threadMsgs = map[string][]Message{}
	threadCount = map[string]int{}
	users = map[string]UserSummary{}

	if body, err := os.ReadFile(filepath.Join(dir, parentsFilename)); err == nil {
		var loaded []Message
		if jerr := json.Unmarshal(body, &loaded); jerr == nil {
			if parentsCap > 0 && len(loaded) > parentsCap {
				loaded = loaded[len(loaded)-parentsCap:]
			}
			parents = loaded
		}
	}

	threadEntries, _ := os.ReadDir(filepath.Join(dir, threadsDirname))
	for _, e := range threadEntries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		ts := strings.TrimSuffix(name, ".json")
		body, err := os.ReadFile(filepath.Join(dir, threadsDirname, name))
		if err != nil {
			continue
		}
		var replies []Message
		if jerr := json.Unmarshal(body, &replies); jerr != nil {
			continue
		}
		threadMsgs[ts] = replies
		threadCount[ts] = len(replies)
	}

	if body, err := os.ReadFile(filepath.Join(dir, usersFilename)); err == nil {
		var loaded map[string]UserSummary
		if jerr := json.Unmarshal(body, &loaded); jerr == nil && loaded != nil {
			users = loaded
		}
	}

	return parents, threadMsgs, threadCount, users
}

// SyncResult summarises one Sync call. Surfaced on tool outputs so the
// agent knows whether the cache covered everything, was rate-limited,
// or hit the configured cap.
type SyncResult struct {
	ParentsAdded     int
	ThreadsRefreshed int
	RateLimited      bool
	Truncated        bool
	NewestTS         string
	OldestTS         string
	ParentCount      int
}

// Sync brings the cache up to date with the upstream channel. Concurrent
// callers serialise on the Store's mutex — the second caller waits for
// the first, then sees a fresh cache.
//
// sinceUnix is an optional lower bound on message timestamps the caller
// wants materialised. Zero means "no lower bound". The agent passes this
// through the per-call `since_unix` arg on the surface tools.
func (s *Store) Sync(ctx context.Context, mode syncMode, sinceUnix int64) (SyncResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.client == nil {
		return SyncResult{}, fmt.Errorf("slack store: client not configured")
	}

	cap := s.analyzeCap
	if mode == syncPeek {
		cap = s.peekCap
	}

	// Compute the `oldest` floor for conversations.history: max of the
	// caller's sinceUnix and the integer-seconds part of the newest
	// already-cached parent. This makes successive Sync calls
	// incremental — Slack returns only messages strictly newer than
	// the floor, so we don't waste pages re-walking the whole channel
	// each call.
	oldest := sinceUnix
	if len(s.parents) > 0 {
		newestCached := s.parents[len(s.parents)-1].TS
		if dot := strings.Index(newestCached, "."); dot >= 0 {
			if n, err := strconv.ParseInt(newestCached[:dot], 10, 64); err == nil && n > oldest {
				oldest = n
			}
		}
	}

	// Page history newest-first. Slack returns messages newest-first
	// within each page; we accumulate, then sort ascending below.
	var fetched []Message
	cursor := ""
	rateLimited := false
	for {
		page, next, rl, err := s.client.ConversationsHistory(ctx, s.channelID, cursor, historyPageSize, oldest)
		if err != nil {
			return SyncResult{}, err
		}
		if rl {
			rateLimited = true
			break
		}
		fetched = append(fetched, page...)
		if next == "" {
			break
		}
		if len(fetched) >= cap {
			break
		}
		cursor = next
	}

	// Merge fetched parents with the cached set, deduping by ts. Cached
	// parents stay; new ones are appended. The merged slice is then
	// sorted and (if oversized) truncated at the cap, keeping the
	// newest cap-many entries.
	seenTS := map[string]bool{}
	for _, p := range s.parents {
		seenTS[p.TS] = true
	}
	added := 0
	merged := append([]Message{}, s.parents...)
	for _, p := range fetched {
		if seenTS[p.TS] {
			continue
		}
		seenTS[p.TS] = true
		merged = append(merged, p)
		added++
	}

	// Sort ascending by ts so the cache is chronological.
	sort.SliceStable(merged, func(i, j int) bool {
		return tsLess(merged[i].TS, merged[j].TS)
	})

	truncated := false
	if len(merged) > cap {
		truncated = true
		// Keep the newest `cap` parents — they're at the tail of the
		// sorted slice.
		merged = merged[len(merged)-cap:]
		// `added` counts pre-truncation; clamp to the post-truncation
		// size so ParentsAdded reflects the actual cache delta the
		// caller observes.
		if added > len(merged) {
			added = len(merged)
		}
	}

	// `collected` is the post-merge, post-truncation working set used
	// by the rest of the function (thread refresh, user resolution,
	// persistence).
	collected := merged

	// Refresh threads whose parent has reply_count > 0 OR whose cached
	// reply count differs from upstream.
	threadsRefreshed := 0
	for _, p := range collected {
		want := p.ReplyCount
		if want <= 0 {
			continue
		}
		if cur, ok := s.threadCount[p.TS]; ok && cur == want {
			continue
		}
		replies, err := s.fetchAllReplies(ctx, p.TS)
		if err != nil {
			// Don't fail the whole sync on one thread; the agent can
			// retry summarize_thread for that one parent.
			continue
		}
		s.threadMsgs[p.TS] = replies
		s.threadCount[p.TS] = want
		threadsRefreshed++
	}

	// Resolve any new user IDs.
	missing := s.collectUnknownUsers(collected)
	if len(missing) > 0 {
		resolved, err := s.client.ResolveUsers(ctx, missing)
		if err == nil {
			for _, u := range resolved {
				s.users[u.ID] = u
			}
		}
	}

	s.parents = collected

	res := SyncResult{
		ParentsAdded:     added,
		ThreadsRefreshed: threadsRefreshed,
		RateLimited:      rateLimited,
		Truncated:        truncated,
		ParentCount:      len(collected),
	}
	if len(collected) > 0 {
		res.OldestTS = collected[0].TS
		res.NewestTS = collected[len(collected)-1].TS
	}

	if err := s.persist(); err != nil {
		return res, fmt.Errorf("slack store: persist: %w", err)
	}
	return res, nil
}

// SyncThread re-fetches just one thread (used by summarize_thread, which
// wants up-to-the-second freshness without paying the cost of a full
// channel sync).
func (s *Store) SyncThread(ctx context.Context, threadTS string) ([]Message, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client == nil {
		return nil, false, fmt.Errorf("slack store: client not configured")
	}
	replies, rl, err := s.fetchAllRepliesUnlocked(ctx, threadTS)
	if err != nil {
		return nil, rl, err
	}
	s.threadMsgs[threadTS] = replies
	if len(replies) > 0 {
		s.threadCount[threadTS] = len(replies) - 1 // exclude parent itself
	}
	missing := s.collectUnknownUsers(replies)
	if len(missing) > 0 {
		resolved, err := s.client.ResolveUsers(ctx, missing)
		if err == nil {
			for _, u := range resolved {
				s.users[u.ID] = u
			}
		}
	}
	if err := s.persist(); err != nil {
		return replies, rl, fmt.Errorf("slack store: persist thread: %w", err)
	}
	return replies, rl, nil
}

// Snapshot returns shallow copies of the in-memory cache for read-only
// consumers (search, overview). Callers must NOT mutate the returned
// slices.
type Snapshot struct {
	Parents []Message
	Threads map[string][]Message
	Users   map[string]UserSummary
}

func (s *Store) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	parents := make([]Message, len(s.parents))
	copy(parents, s.parents)
	threads := make(map[string][]Message, len(s.threadMsgs))
	for k, v := range s.threadMsgs {
		cp := make([]Message, len(v))
		copy(cp, v)
		threads[k] = cp
	}
	users := make(map[string]UserSummary, len(s.users))
	for k, v := range s.users {
		users[k] = v
	}
	return Snapshot{Parents: parents, Threads: threads, Users: users}
}

func (s *Store) fetchAllReplies(ctx context.Context, threadTS string) ([]Message, error) {
	replies, _, err := s.fetchAllRepliesUnlocked(ctx, threadTS)
	return replies, err
}

func (s *Store) fetchAllRepliesUnlocked(ctx context.Context, threadTS string) ([]Message, bool, error) {
	cursor := ""
	var out []Message
	rateLimited := false
	for {
		page, next, rl, err := s.client.ConversationsReplies(ctx, s.channelID, threadTS, cursor, repliesPageSize)
		if err != nil {
			return out, rateLimited, err
		}
		if rl {
			rateLimited = true
			break
		}
		out = append(out, page...)
		if next == "" {
			break
		}
		cursor = next
	}
	sort.SliceStable(out, func(i, j int) bool { return tsLess(out[i].TS, out[j].TS) })
	return out, rateLimited, nil
}

func (s *Store) collectUnknownUsers(msgs []Message) []string {
	seen := map[string]bool{}
	var missing []string
	for _, m := range msgs {
		if m.User == "" || seen[m.User] {
			continue
		}
		if _, ok := s.users[m.User]; ok {
			continue
		}
		seen[m.User] = true
		missing = append(missing, m.User)
	}
	return missing
}

// persist flushes the in-memory cache to disk. Atomic via tmp+rename.
func (s *Store) persist() error {
	if err := s.writeUsers(); err != nil {
		return err
	}
	for ts, replies := range s.threadMsgs {
		if err := s.writeThreadFile(ts, replies); err != nil {
			return err
		}
	}
	if err := s.writeMessagesMD(); err != nil {
		return err
	}
	if err := s.writeParentsJSON(); err != nil {
		return err
	}
	return s.writeMeta()
}

func (s *Store) writeUsers() error {
	body, err := json.MarshalIndent(s.users, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(s.dir, usersFilename), body)
}

func (s *Store) writeMeta() error {
	type meta struct {
		ParentCount  int    `json:"parent_count"`
		NewestTS     string `json:"newest_ts,omitempty"`
		OldestTS     string `json:"oldest_ts_in_cache,omitempty"`
		LastSyncUnix int64  `json:"last_sync_unix"`
	}
	m := meta{
		ParentCount:  len(s.parents),
		LastSyncUnix: time.Now().Unix(),
	}
	if len(s.parents) > 0 {
		m.OldestTS = s.parents[0].TS
		m.NewestTS = s.parents[len(s.parents)-1].TS
	}
	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(s.dir, metaFilename), body)
}

func (s *Store) writeThreadFile(ts string, msgs []Message) error {
	var b strings.Builder
	b.WriteString("# Thread ")
	b.WriteString(ts)
	b.WriteString("\n\n")
	for _, m := range msgs {
		s.appendMessageHeader(&b, m)
		if m.Text != "" {
			b.WriteString(m.Text)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	if err := atomicWrite(filepath.Join(s.dir, threadsDirname, ts+".md"), []byte(b.String())); err != nil {
		return err
	}
	body, err := json.MarshalIndent(msgs, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(s.dir, threadsDirname, ts+".json"), body)
}

func (s *Store) writeMessagesMD() error {
	var b strings.Builder
	b.WriteString("# Channel transcript\n\n")
	for _, m := range s.parents {
		s.appendMessageHeader(&b, m)
		if m.Text != "" {
			b.WriteString(m.Text)
			b.WriteString("\n")
		}
		if m.ReplyCount > 0 {
			fmt.Fprintf(&b, "🧵 %d replies → %s/%s.md\n", m.ReplyCount, threadsDirname, m.TS)
		}
		b.WriteString("\n")
	}
	return atomicWrite(filepath.Join(s.dir, messagesFilename), []byte(b.String()))
}

func (s *Store) writeParentsJSON() error {
	body, err := json.MarshalIndent(s.parents, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(s.dir, parentsFilename), body)
}

func (s *Store) appendMessageHeader(b *strings.Builder, m Message) {
	display := s.userDisplay(m.User)
	when := tsToISO(m.TS)
	fmt.Fprintf(b, "## %s @%s (%s)\n", when, display, m.TS)
}

func (s *Store) userDisplay(id string) string {
	if id == "" {
		return "system"
	}
	if u, ok := s.users[id]; ok {
		if u.IsBot {
			return "bot:" + u.Name
		}
		if u.Name != "" {
			return u.Name
		}
	}
	return id
}

// atomicWrite writes body to dst via a sibling tmp file + rename.
func atomicWrite(dst string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// tsToISO renders a Slack ts (dotted decimal seconds) as RFC3339 UTC.
// Slack-ts strings always have a `.` separator. Falls back to the raw
// value on parse failure.
func tsToISO(ts string) string {
	dot := strings.Index(ts, ".")
	if dot < 0 {
		return ts
	}
	secs, err := strconv.ParseInt(ts[:dot], 10, 64)
	if err != nil {
		return ts
	}
	return time.Unix(secs, 0).UTC().Format(time.RFC3339)
}

// tsLess compares two Slack ts strings as numeric values. Both are dotted
// decimal seconds; the integer-part comparison is sufficient because
// Slack ts strings within one channel keep ordering correct under string
// compare when they're the same length, but we still split on "." for
// safety with mixed-precision values.
func tsLess(a, b string) bool {
	da := strings.Index(a, ".")
	db := strings.Index(b, ".")
	if da < 0 || db < 0 {
		return a < b
	}
	ai, _ := strconv.ParseInt(a[:da], 10, 64)
	bi, _ := strconv.ParseInt(b[:db], 10, 64)
	if ai != bi {
		return ai < bi
	}
	// equal seconds → tiebreak on fractional part lexicographically
	return a[da+1:] < b[db+1:]
}
