package agent

import (
	"strings"

	"github.com/johnny1110/evva/pkg/event"
	"github.com/johnny1110/evva/pkg/llm"
)

// chunkAdapter bridges an llm.Client.Stream call back into the agent's event
// sink. Each provider delta becomes a KindTextChunk or KindThinkingChunk on
// the agent's stream.
//
// Subagents emit no chunks — the chunkAdapter is only created on root agents
// (and only when Profile.Stream is true). Thinking chunks honor the global
// DisplayThinking config switch so users who suppress thinking blocks today
// see the same behavior in streaming mode.
//
// It also accumulates the text it has seen (STE-2). When an interject cuts
// a streaming call short the provider returns a zero Response and the
// partial answer would otherwise be lost — even though the user watched it
// arrive. Capturing here rather than inside each provider is deliberate:
// this is the one place every provider's stream already passes through, so
// six client implementations (claude, deepseek, glm, ollama, openai, qwen)
// get partial capture without any of them changing, and a seventh gets it
// for free.
//
// Only text is kept. Thinking is not replayed into history — providers that
// support extended thinking require an opaque signature alongside it, and a
// truncated block has none, so re-submitting it would be rejected. Partial
// tool_use blocks are unrecoverable by construction: the adapter never sees
// them, and half a JSON argument object is not a tool call.
type chunkAdapter struct {
	agent        *Agent
	emitThinking bool

	text strings.Builder
}

func (a *Agent) newChunkAdapter() *chunkAdapter {
	return &chunkAdapter{
		agent:        a,
		emitThinking: a.cfg.GetDisplayThinking(),
	}
}

func (c *chunkAdapter) OnChunk(ck llm.Chunk) {
	if ck.Delta == "" {
		return
	}
	switch ck.Kind {
	case llm.ChunkText:
		c.text.WriteString(ck.Delta)
		c.agent.emit(event.KindTextChunk, func(e *event.Event) {
			e.Text = &event.TextPayload{Text: ck.Delta}
		})
	case llm.ChunkThinking:
		if !c.emitThinking {
			return
		}
		c.agent.emit(event.KindThinkingChunk, func(e *event.Event) {
			e.Thinking = &event.TextPayload{Text: ck.Delta}
		})
	}
}

// Partial returns the assistant text streamed so far. Called only after the
// Stream call has returned, so there is no concurrent writer.
func (c *chunkAdapter) Partial() string {
	if c == nil {
		return ""
	}
	return c.text.String()
}
