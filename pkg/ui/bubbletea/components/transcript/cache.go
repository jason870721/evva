package transcript

// blockCache memoises Block.Render output per block, keyed on every
// input that could change the rendered string. A cache miss runs
// the block's full Render path (including glamour for TextBlock); a
// cache hit returns the stored string with no allocations.
//
// Invalidation is implicit: any change to the key components causes
// the next Get to re-render. The block itself doesn't know its
// entry is cached; the transcript owns the cache.
//
// Concurrency: not safe for concurrent Get/Drop. The bubbletea main
// loop is the only caller, so a mutex would be ceremony for no
// gain.
type blockCache struct {
	entries map[uint64]cacheEntry
}

// cacheKey is the composite key the cache uses to decide hit vs.
// miss. All five fields must match for a hit. Equality is plain Go
// struct compare — no pointer chasing.
type cacheKey struct {
	width    int
	themeRev uint64
	mdRev    uint64
	blockRev uint64
	optsRev  uint64
}

type cacheEntry struct {
	key      cacheKey
	rendered string
}

func newBlockCache() *blockCache {
	return &blockCache{entries: map[uint64]cacheEntry{}}
}

// Get returns the cached render for b under ctx, computing and
// storing it on miss. Always returns a non-nil string (empty when
// the block's Render returns empty).
func (c *blockCache) Get(b Block, ctx RenderContext) string {
	key := cacheKey{
		width:    ctx.Width,
		themeRev: ctx.Theme.Rev,
		mdRev:    mdRev(ctx.Markdown),
		blockRev: b.Rev(),
		optsRev:  optsRev(ctx.Opts),
	}
	if e, ok := c.entries[b.ID()]; ok && e.key == key {
		return e.rendered
	}
	out := b.Render(ctx)
	c.entries[b.ID()] = cacheEntry{key: key, rendered: out}
	return out
}

// Drop removes the cache entry for the given block ID. Called by
// the transcript when a block is removed (e.g. CompactingEnd OK
// removes the inflight compact row).
func (c *blockCache) Drop(id uint64) {
	delete(c.entries, id)
}

// Clear wipes every entry. Called by Transcript.Reset (/clear,
// /model swap) and as a safety net on theme swap if downstream code
// ever forgets to bump Theme.Rev.
func (c *blockCache) Clear() {
	if len(c.entries) == 0 {
		return
	}
	c.entries = map[uint64]cacheEntry{}
}

// Size reports the number of cached entries. Test-only; not part of
// the public API.
func (c *blockCache) Size() int { return len(c.entries) }
