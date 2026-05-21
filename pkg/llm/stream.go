package llm

// ChunkKind discriminates what kind of text a streaming delta carries.
// Providers translate their wire-level signal (text_delta, reasoning_content,
// thinking_delta, message.content, ...) into one of these values so the agent
// and UI never need to import a provider package.
type ChunkKind int

const (
	// ChunkText is a delta of the assistant's user-facing reply.
	ChunkText ChunkKind = iota
	// ChunkThinking is a delta of the model's reasoning trace (DeepSeek
	// reasoning_content, Anthropic thinking_delta, Ollama message.thinking).
	ChunkThinking
)

// Chunk is one delta emitted during a streaming completion. Delta is the
// incremental text the provider just produced — never the cumulative buffer.
// Consumers append it to their own in-flight block.
type Chunk struct {
	Kind  ChunkKind
	Delta string
}

// ChunkSink consumes deltas as they arrive. Providers call OnChunk from the
// goroutine that owns the streaming read; calls are serialized by the
// provider, never invoked concurrently for the same Stream call.
//
// Implementations should be fast and non-blocking. The agent's adapter (see
// internal/agent/stream.go) forwards each chunk to the event sink with the
// usual emit mutex, which is sufficient.
type ChunkSink interface {
	OnChunk(Chunk)
}

// ChunkFunc adapts a plain function into a ChunkSink. Convenient for tests
// and small inline consumers.
type ChunkFunc func(Chunk)

func (f ChunkFunc) OnChunk(c Chunk) { f(c) }

// DiscardChunks is a ChunkSink that drops every chunk. Useful when a caller
// wants to invoke Stream but doesn't care about progressive output.
var DiscardChunks ChunkSink = ChunkFunc(func(Chunk) {})
