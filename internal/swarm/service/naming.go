package service

import (
	"math/rand"

	"github.com/johnny1110/evva/pkg/common"
)

// Docker-style handle generation: an adjective-noun pair (e.g. "swift-otter")
// assigned when a swarm has no `name:` in its manifest and no --name was given.
// The lists are small on purpose — a few hundred combinations is plenty for a
// localhost workstation, and genNameLocked retries on the rare collision.
var (
	nameAdjectives = []string{
		"swift", "calm", "bold", "brave", "bright", "clever", "eager", "gentle",
		"keen", "lively", "merry", "noble", "quiet", "rapid", "sturdy", "witty",
	}
	nameNouns = []string{
		"otter", "falcon", "maple", "harbor", "comet", "willow", "badger", "lark",
		"cedar", "ember", "quartz", "raven", "delta", "fjord", "pike", "wren",
	}
)

// nameTakenLocked reports whether any known space (running or stopped) already
// uses name. Caller holds s.mu.
func (s *Service) nameTakenLocked(name string) bool {
	for _, e := range s.spaces {
		if e.name == name {
			return true
		}
	}
	return false
}

// genNameLocked returns a fresh, unique adjective-noun handle. Caller holds s.mu.
// On the (improbable) event every combination it tries is taken, it falls back
// to a short uuid-suffixed handle that is effectively always unique.
func (s *Service) genNameLocked() string {
	for i := 0; i < 100; i++ {
		n := nameAdjectives[rand.Intn(len(nameAdjectives))] + "-" + nameNouns[rand.Intn(len(nameNouns))]
		if !s.nameTakenLocked(n) {
			return n
		}
	}
	return "swarm-" + common.GenUUID()[:8]
}
