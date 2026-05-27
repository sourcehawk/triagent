package watches

import "context"

// Source is the per-kind poll adapter. All adapters live in
// investigate/internal/watches/sources/. They're registered in
// NewSourceRegistry at construction; the poller looks them up by
// Watch.Source.Kind.
type Source interface {
	Kind() SourceKind
	// Poll returns items newer than the watermark, with their dedupe
	// identity populated. Returns the advanced watermark; the manager
	// persists it after a successful poll.
	Poll(ctx context.Context, w Watch, wm WatermarkState) ([]Item, WatermarkState, error)
}

type SourceRegistry struct {
	m map[SourceKind]Source
}

func NewSourceRegistry() *SourceRegistry { return &SourceRegistry{m: map[SourceKind]Source{}} }

func (r *SourceRegistry) Register(s Source) { r.m[s.Kind()] = s }

func (r *SourceRegistry) Get(k SourceKind) (Source, bool) { s, ok := r.m[k]; return s, ok }
