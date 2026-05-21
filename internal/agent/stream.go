package agent

import (
	"github.com/johnny1110/evva/internal/agent/event"
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
type chunkAdapter struct {
	agent          *Agent
	emitThinking   bool
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
