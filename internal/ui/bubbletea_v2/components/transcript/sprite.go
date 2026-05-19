package transcript

// ============================================================================
// ThinkingSpriteBlock — animated sprite at the end of the transcript while
// the agent is thinking. Rendered with the timeline gutter so it reads as
// part of the scrollback. Frame advances on every spinner tick.
// ============================================================================

var thinkingSpriteFrames = []string{
	`  .-.    
 (o.o)  
  m m    `,
	`    .-.    
   (o.o)  
   _m_m_   `,
	`      .-.    
     (o.o)  
      w w    `,
	`        .-.    
       (o.o)  
      _w_w_   `,
	`          .-.    
         (O.O)  
          m m    `,
	`            .-.    
           (O.O)  
          _m_m_   `,
	`              .-.    
             (o.o)  
              w w    `,
	`                .-.    
               (o.o)   
              _w_w_   `,
	`                .-.    
               (·.·)  
                | |    `,
	`                .-.    
               (·.·)  
                | |    `,
	`              .-.    
             (o.o)  
             m m    `,
	`            .-.    
           (o.o)  
          _m_m_   `,
	`          .-.    
         (o.o)  
         w w    `,
	`        .-.    
       (o.o)  
      _w_w_   `,
	`      .-.    
     (O.O)  
     m m    `,
	`    .-.    
   (O.O)  
  _m_m_   `,
	`  .-.    
 (o.o)  
 m m    `,
	`  .-.    
 (o.o)  
_w_w_   `,
	`  .-.    
 (·.·)  
  | |    `,
	`  .-.    
 (·.·) 
  | |    `,
}

// ThinkingSpriteBlock is the "walking" sprite shown when the agent state is
// StateThinking. Managed by Transcript.ShowThinkingSprite / HideThinkingSprite.
type ThinkingSpriteBlock struct {
	id    uint64
	rev   uint64
	frame int
}

func newThinkingSpriteBlock() *ThinkingSpriteBlock {
	return &ThinkingSpriteBlock{id: allocID(), rev: 1}
}

func (b *ThinkingSpriteBlock) ID() uint64        { return b.id }
func (b *ThinkingSpriteBlock) Rev() uint64       { return b.rev }
func (b *ThinkingSpriteBlock) Kind() Kind        { return KindSystem }
func (b *ThinkingSpriteBlock) PlainText() string { return "" }

// SetFrame bumps the animation frame. No-op when frame hasn't changed
// (avoids cache churn on consecutive ticks with the same index).
func (b *ThinkingSpriteBlock) SetFrame(frame int) {
	if frame == b.frame {
		return
	}
	b.frame = frame
	b.rev++
}

func (b *ThinkingSpriteBlock) Render(ctx RenderContext) string {
	f := b.frame % len(thinkingSpriteFrames)
	styled := ctx.Theme.UserPrompt.Render(thinkingSpriteFrames[f])
	return applyLineGutter(styled, ctx.Width, ctx.Theme, ctx.Opts.Focused, len(ctx.Opts.Highlights) > 0)
}
