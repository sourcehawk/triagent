package slack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewStore_PreservesExistingCacheDir(t *testing.T) {
	root := t.TempDir()
	channelDir := filepath.Join(root, "C1")
	require.NoError(t, os.MkdirAll(channelDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(channelDir, "stale.md"), []byte("old"), 0o600))

	st, err := NewStore(StoreOptions{Root: root, ChannelID: "C1"})
	require.NoError(t, err)
	assert.Equal(t, channelDir, st.Dir())

	// The pre-existing file MUST survive — the launcher's contract is now
	// "preserve cache across boots; clear via `triagent clean`".
	body, statErr := os.ReadFile(filepath.Join(channelDir, "stale.md"))
	require.NoError(t, statErr, "stale file must NOT be wiped on construct")
	assert.Equal(t, "old", string(body))

	// Threads dir is created idempotently so the rest of the store works.
	info, err := os.Stat(filepath.Join(channelDir, threadsDirname))
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func newHistoryStub(t *testing.T, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/conversations.history":
			_, _ = w.Write([]byte(body))
		case "/conversations.replies":
			_, _ = w.Write([]byte(`{"ok":true,"messages":[],"response_metadata":{"next_cursor":""}}`))
		case "/users.info":
			id := r.URL.Query().Get("user")
			_, _ = w.Write([]byte(`{"ok":true,"user":{"id":"` + id + `","name":"` + id + `","real_name":"R"}}`))
		default:
			assert.Failf(t, "unexpected path", "%s", r.URL.Path)
		}
	}))
}

func TestStoreSync_ColdFullWritesMessagesAndMeta(t *testing.T) {
	stub := newHistoryStub(t, `{"ok":true,"messages":[
		{"ts":"100.000100","user":"U1","text":"first"},
		{"ts":"200.000200","user":"U2","text":"second"}
	],"response_metadata":{"next_cursor":""}}`)
	defer stub.Close()

	st, err := NewStore(StoreOptions{Root: t.TempDir(), ChannelID: "C1"})
	require.NoError(t, err)
	st.client = NewClient(stub.URL, "xoxp-x")
	st.channelID = "C1"
	st.analyzeCap = 100

	res, err := st.Sync(context.Background(), syncFull, 0)
	require.NoError(t, err)
	assert.Equal(t, 2, res.ParentsAdded, "counts: %+v", res)
	assert.Equal(t, 2, res.ParentCount, "counts: %+v", res)
	assert.Equal(t, "200.000200", res.NewestTS, "ts bounds: %+v", res)
	assert.Equal(t, "100.000100", res.OldestTS, "ts bounds: %+v", res)
	body, err := os.ReadFile(st.Dir() + "/messages.md")
	require.NoError(t, err)
	got := string(body)
	assert.Contains(t, got, "(100.000100)", "messages.md missing ts marker")
	assert.Contains(t, got, "(200.000200)", "messages.md missing ts marker")
	assert.Contains(t, got, "first", "messages.md missing text")
	assert.Contains(t, got, "second", "messages.md missing text")
	_, statErr := os.Stat(st.Dir() + "/meta.json")
	assert.NoError(t, statErr, "meta.json missing")
}

func TestStoreSync_FetchesThreadsForParentsWithReplies(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/conversations.history":
			_, _ = w.Write([]byte(`{"ok":true,"messages":[
				{"ts":"100.000100","user":"U1","text":"parent","reply_count":2,"thread_ts":"100.000100"}
			],"response_metadata":{"next_cursor":""}}`))
		case "/conversations.replies":
			assert.Equal(t, "100.000100", r.URL.Query().Get("ts"))
			_, _ = w.Write([]byte(`{"ok":true,"messages":[
				{"ts":"100.000100","user":"U1","text":"parent"},
				{"ts":"110.000100","user":"U2","text":"reply-one"},
				{"ts":"120.000100","user":"U3","text":"reply-two"}
			],"response_metadata":{"next_cursor":""}}`))
		default:
			_, _ = w.Write([]byte(`{"ok":true}`))
		}
	}))
	defer stub.Close()

	st, _ := NewStore(StoreOptions{Root: t.TempDir(), ChannelID: "C1"})
	st.client = NewClient(stub.URL, "xoxp-x")
	st.channelID = "C1"

	res, err := st.Sync(context.Background(), syncFull, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, res.ThreadsRefreshed)
	body, err := os.ReadFile(st.Dir() + "/threads/100.000100.md")
	require.NoError(t, err)
	got := string(body)
	assert.Contains(t, got, "reply-one", "thread file missing reply")
	assert.Contains(t, got, "reply-two", "thread file missing reply")
}

func TestStoreSync_WritesUsersJSONForResolvedHandles(t *testing.T) {
	stub := newHistoryStub(t, `{"ok":true,"messages":[
		{"ts":"100.000100","user":"U1","text":"hi"},
		{"ts":"200.000100","user":"U2","text":"yo"}
	],"response_metadata":{"next_cursor":""}}`)
	defer stub.Close()

	st, _ := NewStore(StoreOptions{Root: t.TempDir(), ChannelID: "C1"})
	st.client = NewClient(stub.URL, "xoxp-x")
	st.channelID = "C1"

	_, err := st.Sync(context.Background(), syncFull, 0)
	require.NoError(t, err)
	body, err := os.ReadFile(st.Dir() + "/users.json")
	require.NoError(t, err)
	got := string(body)
	assert.Contains(t, got, `"U1"`, "users.json missing entry")
	assert.Contains(t, got, `"U2"`, "users.json missing entry")
}

func TestStoreSync_IncrementalSkipsCachedParents(t *testing.T) {
	calls := 0
	mu := &sync.Mutex{}
	pages := [][]byte{
		[]byte(`{"ok":true,"messages":[
			{"ts":"200.000100","user":"U2","text":"second"},
			{"ts":"100.000100","user":"U1","text":"first"}
		],"response_metadata":{"next_cursor":""}}`),
		[]byte(`{"ok":true,"messages":[
			{"ts":"300.000100","user":"U3","text":"third"},
			{"ts":"200.000100","user":"U2","text":"second"}
		],"response_metadata":{"next_cursor":""}}`),
	}
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/conversations.history" {
			mu.Lock()
			defer mu.Unlock()
			page := pages[calls]
			calls++
			_, _ = w.Write(page)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"messages":[]}`))
	}))
	defer stub.Close()

	st, _ := NewStore(StoreOptions{Root: t.TempDir(), ChannelID: "C1"})
	st.client = NewClient(stub.URL, "xoxp-x")
	st.channelID = "C1"

	_, err := st.Sync(context.Background(), syncFull, 0)
	require.NoError(t, err)
	res, err := st.Sync(context.Background(), syncFull, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, res.ParentsAdded, "second sync should add 1 (only 300.x)")
	assert.Equal(t, "300.000100", res.NewestTS)
}

func TestStoreSync_TruncatedAtCap(t *testing.T) {
	stub := newHistoryStub(t, `{"ok":true,"messages":[
		{"ts":"300.000100","user":"U1","text":"c"},
		{"ts":"200.000100","user":"U1","text":"b"},
		{"ts":"100.000100","user":"U1","text":"a"}
	],"response_metadata":{"next_cursor":""}}`)
	defer stub.Close()

	st, _ := NewStore(StoreOptions{Root: t.TempDir(), ChannelID: "C1"})
	st.client = NewClient(stub.URL, "xoxp-x")
	st.channelID = "C1"
	st.analyzeCap = 2

	res, err := st.Sync(context.Background(), syncFull, 0)
	require.NoError(t, err)
	assert.True(t, res.Truncated, "want Truncated=true, got %+v", res)
	assert.Equal(t, 2, res.ParentsAdded)
}

func TestStoreSync_RateLimited(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer stub.Close()

	st, _ := NewStore(StoreOptions{Root: t.TempDir(), ChannelID: "C1"})
	st.client = NewClient(stub.URL, "xoxp-x")
	st.channelID = "C1"

	res, err := st.Sync(context.Background(), syncFull, 0)
	require.NoError(t, err)
	assert.True(t, res.RateLimited, "want RateLimited=true, got %+v", res)
}

func TestStoreSync_PeekModeUsesPeekCap(t *testing.T) {
	stub := newHistoryStub(t, `{"ok":true,"messages":[
		{"ts":"300.000100","user":"U1","text":"c"},
		{"ts":"200.000100","user":"U1","text":"b"},
		{"ts":"100.000100","user":"U1","text":"a"}
	],"response_metadata":{"next_cursor":""}}`)
	defer stub.Close()

	st, _ := NewStore(StoreOptions{Root: t.TempDir(), ChannelID: "C1"})
	st.client = NewClient(stub.URL, "xoxp-x")
	st.channelID = "C1"
	st.peekCap = 1
	st.analyzeCap = 100

	res, err := st.Sync(context.Background(), syncPeek, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, res.ParentsAdded, "peek-cap not honoured: %+v", res)
	assert.True(t, res.Truncated, "peek-cap not honoured: %+v", res)
}

func TestStoreSync_WritesParentsJSONSidecar(t *testing.T) {
	stub := newHistoryStub(t, `{"ok":true,"messages":[
		{"ts":"500.000100","user":"U1","text":"first","reply_count":0},
		{"ts":"600.000100","user":"U2","text":"second","reply_count":0}
	],"response_metadata":{"next_cursor":""}}`)
	defer stub.Close()
	root := t.TempDir()
	st, err := NewStore(StoreOptions{Root: root, ChannelID: "C1"})
	require.NoError(t, err)
	st.client = NewClient(stub.URL, "xoxp-x")
	st.channelID = "C1"

	_, err = st.Sync(context.Background(), syncFull, 0)
	require.NoError(t, err)

	body, err := os.ReadFile(filepath.Join(st.Dir(), "parents.json"))
	require.NoError(t, err, "parents.json must be written alongside messages.md")
	var got []Message
	require.NoError(t, json.Unmarshal(body, &got))
	require.Len(t, got, 2)
	assert.Equal(t, "500.000100", got[0].TS)
	assert.Equal(t, "first", got[0].Text)
	assert.Equal(t, "U1", got[0].User)
	assert.Equal(t, "600.000100", got[1].TS)
}

func TestStoreSync_WritesThreadJSONSidecars(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/conversations.history":
			_, _ = w.Write([]byte(`{"ok":true,"messages":[
				{"ts":"500.000100","user":"U1","text":"parent","reply_count":1}
			],"response_metadata":{"next_cursor":""}}`))
		case "/conversations.replies":
			_, _ = w.Write([]byte(`{"ok":true,"messages":[
				{"ts":"500.000100","user":"U1","text":"parent"},
				{"ts":"550.000100","user":"U2","text":"reply"}
			],"response_metadata":{"next_cursor":""}}`))
		case "/users.info":
			id := r.URL.Query().Get("user")
			_, _ = w.Write([]byte(`{"ok":true,"user":{"id":"` + id + `","name":"` + id + `","real_name":"R"}}`))
		default:
			assert.Failf(t, "unexpected path", "%s", r.URL.Path)
		}
	}))
	defer stub.Close()

	st, err := NewStore(StoreOptions{Root: t.TempDir(), ChannelID: "C1"})
	require.NoError(t, err)
	st.client = NewClient(stub.URL, "xoxp-x")
	st.channelID = "C1"

	_, err = st.Sync(context.Background(), syncFull, 0)
	require.NoError(t, err)

	body, err := os.ReadFile(filepath.Join(st.Dir(), "threads", "500.000100.json"))
	require.NoError(t, err, "threads/<ts>.json sidecar must be written alongside threads/<ts>.md")
	var got []Message
	require.NoError(t, json.Unmarshal(body, &got))
	require.Len(t, got, 2)
	assert.Equal(t, "500.000100", got[0].TS)
	assert.Equal(t, "550.000100", got[1].TS)
}

func TestNewStore_LoadsPopulatedCacheIntoMemory(t *testing.T) {
	root := t.TempDir()
	channelDir := filepath.Join(root, "C1")
	require.NoError(t, os.MkdirAll(filepath.Join(channelDir, "threads"), 0o700))

	parents := []Message{
		{TS: "500.000100", User: "U1", Text: "first"},
		{TS: "600.000100", User: "U2", Text: "second", ReplyCount: 1},
	}
	pBody, _ := json.MarshalIndent(parents, "", "  ")
	require.NoError(t, os.WriteFile(filepath.Join(channelDir, "parents.json"), pBody, 0o600))

	thread := []Message{
		{TS: "600.000100", User: "U2", Text: "parent"},
		{TS: "650.000100", User: "U3", Text: "reply"},
	}
	tBody, _ := json.MarshalIndent(thread, "", "  ")
	require.NoError(t, os.WriteFile(filepath.Join(channelDir, "threads", "600.000100.json"), tBody, 0o600))

	usersBody, _ := json.MarshalIndent(map[string]UserSummary{
		"U1": {ID: "U1", Name: "alice"},
		"U2": {ID: "U2", Name: "bob"},
	}, "", "  ")
	require.NoError(t, os.WriteFile(filepath.Join(channelDir, "users.json"), usersBody, 0o600))

	st, err := NewStore(StoreOptions{Root: root, ChannelID: "C1"})
	require.NoError(t, err)

	snap := st.Snapshot()
	require.Len(t, snap.Parents, 2, "loaded parents.json must populate the in-memory parents slice")
	assert.Equal(t, "first", snap.Parents[0].Text)
	require.Contains(t, snap.Threads, "600.000100", "loaded threads/<ts>.json must populate threadMsgs")
	require.Len(t, snap.Threads["600.000100"], 2)
	require.Contains(t, snap.Users, "U1")
	assert.Equal(t, "alice", snap.Users["U1"].Name)
}

func TestNewStore_EmptyCacheDirStartsFresh(t *testing.T) {
	st, err := NewStore(StoreOptions{Root: t.TempDir(), ChannelID: "C1"})
	require.NoError(t, err)
	snap := st.Snapshot()
	assert.Empty(t, snap.Parents)
	assert.Empty(t, snap.Threads)
	assert.Empty(t, snap.Users)
}

func TestNewStore_CorruptCacheFilesAreSkipped(t *testing.T) {
	root := t.TempDir()
	channelDir := filepath.Join(root, "C1")
	require.NoError(t, os.MkdirAll(filepath.Join(channelDir, "threads"), 0o700))

	// Corrupt parents.json — must be silently dropped.
	require.NoError(t, os.WriteFile(filepath.Join(channelDir, "parents.json"), []byte("{not json"), 0o600))

	// One valid thread + one corrupt thread — only the valid one survives.
	good := []Message{{TS: "500.000100", User: "U1", Text: "parent"}}
	gBody, _ := json.MarshalIndent(good, "", "  ")
	require.NoError(t, os.WriteFile(filepath.Join(channelDir, "threads", "500.000100.json"), gBody, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(channelDir, "threads", "999.000000.json"), []byte("garbage"), 0o600))

	// Corrupt users.json — must be silently dropped.
	require.NoError(t, os.WriteFile(filepath.Join(channelDir, "users.json"), []byte("not json"), 0o600))

	st, err := NewStore(StoreOptions{Root: root, ChannelID: "C1"})
	require.NoError(t, err, "construction must not fail on corrupt files")
	snap := st.Snapshot()
	assert.Empty(t, snap.Parents, "corrupt parents.json drops to empty")
	assert.Empty(t, snap.Users, "corrupt users.json drops to empty")
	require.Contains(t, snap.Threads, "500.000100", "valid thread survives alongside corrupt sibling")
	assert.NotContains(t, snap.Threads, "999.000000", "corrupt thread is silently dropped")
}

func TestStoreSync_IncrementalAfterLoadFromDisk(t *testing.T) {
	// Stub asserts the request's `oldest` parameter is the loaded TS — i.e.
	// Sync resumes from the persisted state instead of refetching from zero.
	var seenOldest []string
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/conversations.history":
			seenOldest = append(seenOldest, r.URL.Query().Get("oldest"))
			_, _ = w.Write([]byte(`{"ok":true,"messages":[
				{"ts":"800.000100","user":"U3","text":"new"}
			],"response_metadata":{"next_cursor":""}}`))
		case "/conversations.replies":
			_, _ = w.Write([]byte(`{"ok":true,"messages":[],"response_metadata":{"next_cursor":""}}`))
		case "/users.info":
			id := r.URL.Query().Get("user")
			_, _ = w.Write([]byte(`{"ok":true,"user":{"id":"` + id + `","name":"` + id + `","real_name":"R"}}`))
		default:
			assert.Failf(t, "unexpected path", "%s", r.URL.Path)
		}
	}))
	defer stub.Close()

	root := t.TempDir()
	channelDir := filepath.Join(root, "C1")
	require.NoError(t, os.MkdirAll(filepath.Join(channelDir, "threads"), 0o700))
	parents := []Message{{TS: "500.000100", User: "U1", Text: "old"}}
	pBody, _ := json.MarshalIndent(parents, "", "  ")
	require.NoError(t, os.WriteFile(filepath.Join(channelDir, "parents.json"), pBody, 0o600))

	st, err := NewStore(StoreOptions{Root: root, ChannelID: "C1"})
	require.NoError(t, err)
	st.client = NewClient(stub.URL, "xoxp-x")
	st.channelID = "C1"

	_, err = st.Sync(context.Background(), syncFull, 0)
	require.NoError(t, err)

	require.Len(t, seenOldest, 1, "want exactly one history call")
	assert.Equal(t, "500", seenOldest[0],
		"oldest parameter must be the integer-seconds part of the loaded newest TS — proves incremental resume")

	snap := st.Snapshot()
	require.Len(t, snap.Parents, 2, "fresh message merged with loaded one")
	assert.Equal(t, "500.000100", snap.Parents[0].TS)
	assert.Equal(t, "800.000100", snap.Parents[1].TS)
}
