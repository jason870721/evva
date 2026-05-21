package llm

import "testing"

// Phase 1 analysis — stream surface:
//   - ChunkFunc adapts a plain func into a ChunkSink — OnChunk forwards
//     the chunk to the underlying function verbatim
//   - DiscardChunks is a no-op sink (useful when a caller wants to invoke
//     Stream but doesn't care about deltas)
//   - ChunkKind discriminates ChunkText vs ChunkThinking; tests pin the
//     enum values so a reorder isn't silently wire-compatible

func TestChunkFunc_ForwardsCallVerbatim(t *testing.T) {
	var got []Chunk
	sink := ChunkFunc(func(c Chunk) { got = append(got, c) })

	sink.OnChunk(Chunk{Kind: ChunkText, Delta: "hello"})
	sink.OnChunk(Chunk{Kind: ChunkThinking, Delta: "thinking..."})

	if len(got) != 2 {
		t.Fatalf("len: got %d, want 2", len(got))
	}
	if got[0].Kind != ChunkText || got[0].Delta != "hello" {
		t.Errorf("chunk 0: got %+v", got[0])
	}
	if got[1].Kind != ChunkThinking || got[1].Delta != "thinking..." {
		t.Errorf("chunk 1: got %+v", got[1])
	}
}

func TestDiscardChunks_NeverPanics(t *testing.T) {
	// Smoke: feed several chunks into DiscardChunks; the contract is
	// purely "no-op, never blocks or panics".
	DiscardChunks.OnChunk(Chunk{Kind: ChunkText, Delta: "ignored"})
	DiscardChunks.OnChunk(Chunk{Kind: ChunkThinking, Delta: "also ignored"})
	DiscardChunks.OnChunk(Chunk{}) // zero-value
}

func TestChunkKind_StableValues(t *testing.T) {
	// Lock down the enum values so external snapshots (logs, persisted
	// transcripts) that record them as ints stay readable across refactors.
	if ChunkText != 0 {
		t.Errorf("ChunkText: got %d, want 0", ChunkText)
	}
	if ChunkThinking != 1 {
		t.Errorf("ChunkThinking: got %d, want 1", ChunkThinking)
	}
}
