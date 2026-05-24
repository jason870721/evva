package hooks

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// Warning is a non-fatal load error. The caller surfaces these on stderr
// like permission warnings; the agent still starts so a malformed
// settings.json doesn't brick the session.
type Warning struct {
	Path string
	Err  error
}

func (w Warning) Error() string {
	if w.Path == "" {
		return w.Err.Error()
	}
	return fmt.Sprintf("%s: %v", w.Path, w.Err)
}

// fileShape is the JSON layout on disk. Mirrors Claude Code's
// settings.json `hooks` block verbatim so files written for either tool
// load in the other unchanged.
type fileShape struct {
	Hooks map[string][]matcherShape `json:"hooks"`
}

type matcherShape struct {
	Matcher string         `json:"matcher,omitempty"`
	Hooks   []commandShape `json:"hooks"`
}

type commandShape struct {
	Type    string            `json:"type"`
	Command string            `json:"command,omitempty"`
	URL     string            `json:"url,omitempty"`
	Method  string            `json:"method,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Timeout int               `json:"timeout,omitempty"`
	Async   *bool             `json:"async,omitempty"`
}

// Load reads hooks from <workdir>/.evva/settings.json and
// <evvaHome>/settings.json. Missing files are not errors. Malformed
// entries become Warnings; the rest of the file still loads.
//
// Merge order: project hooks first, then user hooks → project hooks fire
// first in the dispatcher's sequential walk and may short-circuit user
// hooks via continue:false.
func Load(workdir, evvaHome string) (*Registry, []Warning) {
	reg := NewRegistry()
	byEvent := map[Event][]Config{}
	var warns []Warning

	if workdir != "" {
		path := filepath.Join(workdir, ".evva", "settings.json")
		got, w := loadFile(path)
		warns = append(warns, w...)
		mergeInto(byEvent, got)
	}
	if evvaHome != "" {
		path := filepath.Join(evvaHome, "settings.json")
		got, w := loadFile(path)
		warns = append(warns, w...)
		mergeInto(byEvent, got)
	}

	reg.ReplaceAll(byEvent)
	return reg, warns
}

func mergeInto(dst map[Event][]Config, src map[Event][]Config) {
	for ev, cfgs := range src {
		dst[ev] = append(dst[ev], cfgs...)
	}
}

func loadFile(path string) (map[Event][]Config, []Warning) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, []Warning{{Path: path, Err: err}}
	}

	var shape fileShape
	if err := json.Unmarshal(raw, &shape); err != nil {
		return nil, []Warning{{Path: path, Err: fmt.Errorf("invalid json: %w", err)}}
	}

	out := map[Event][]Config{}
	var warns []Warning

	for evName, matchers := range shape.Hooks {
		ev := Event(evName)
		if !ev.Valid() {
			warns = append(warns, Warning{Path: path, Err: fmt.Errorf("unknown event %q", evName)})
			continue
		}
		for i, ms := range matchers {
			cfg, mw := normalizeMatcher(path, evName, i, ms)
			warns = append(warns, mw...)
			if len(cfg.Hooks) == 0 {
				continue
			}
			out[ev] = append(out[ev], cfg)
		}
	}
	return out, warns
}

// normalizeMatcher validates one matcher entry and drops bad hooks within
// it. A bad matcher glob produces a warning and the whole matcher is
// skipped; a bad hook (wrong type, missing field) produces a warning and
// only that hook is skipped — the matcher's other hooks still run.
func normalizeMatcher(path, evName string, idx int, ms matcherShape) (Config, []Warning) {
	var warns []Warning
	cfg := Config{Matcher: ms.Matcher}

	if ms.Matcher != "" {
		if _, err := doublestar.Match(ms.Matcher, "x"); err != nil {
			warns = append(warns, Warning{
				Path: path,
				Err:  fmt.Errorf("%s[%d]: invalid matcher %q: %w", evName, idx, ms.Matcher, err),
			})
			return Config{}, warns
		}
	}

	for j, hs := range ms.Hooks {
		cmd, herr := normalizeCommand(evName, hs)
		if herr != nil {
			warns = append(warns, Warning{
				Path: path,
				Err:  fmt.Errorf("%s[%d].hooks[%d]: %w", evName, idx, j, herr),
			})
			continue
		}
		cfg.Hooks = append(cfg.Hooks, cmd)
	}
	return cfg, warns
}

func normalizeCommand(evName string, hs commandShape) (Command, error) {
	t := CommandType(strings.ToLower(hs.Type))
	cmd := Command{
		Type:    t,
		Timeout: hs.Timeout,
		Method:  hs.Method,
		Headers: hs.Headers,
	}
	if hs.Async != nil {
		cmd.Async = *hs.Async
	} else if t == TypeHTTP {
		cmd.Async = true // HTTP webhooks default to fire-and-forget
	}

	if hs.Timeout != 0 && (hs.Timeout < 1 || hs.Timeout > 600) {
		return Command{}, fmt.Errorf("timeout %d out of range [1,600]", hs.Timeout)
	}

	switch t {
	case TypeCommand:
		if strings.TrimSpace(hs.Command) == "" {
			return Command{}, errors.New("type=command requires non-empty command")
		}
		cmd.Command = hs.Command
	case TypeHTTP:
		if hs.URL == "" {
			return Command{}, errors.New("type=http requires url")
		}
		u, err := url.Parse(hs.URL)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return Command{}, fmt.Errorf("invalid url %q", hs.URL)
		}
		cmd.URL = hs.URL
	default:
		return Command{}, fmt.Errorf("unsupported type %q (want command|http)", hs.Type)
	}
	return cmd, nil
}
