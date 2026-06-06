package main

import (
	"context"

	"github.com/johnny1110/evva/pkg/agent"
)

// This file is the separate-module compile proof for the public inbox-drainer
// seam (SPRD-1-12): because full-host is its own Go module that may import only
// evva's pkg/* surface, the fact that it references agent.Drainer +
// agent.WithInboxDrainer here is proof a third party can fold out-of-band
// messages into a busy agent's run without touching internal/*.
//
// The flagship example installs no drainer (single-agent TUI), so this is
// compile-only — it is never wired into the live agent in main().

// silentDrainer is a no-op agent.Drainer.
type silentDrainer struct{}

func (silentDrainer) Drain(context.Context) (string, bool) { return "", false }

// inboxDrainerOption shows WithInboxDrainer composing like any other option.
func inboxDrainerOption() agent.Option { return agent.WithInboxDrainer(silentDrainer{}) }
