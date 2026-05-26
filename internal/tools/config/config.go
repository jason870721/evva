package configtool

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/tools"
)

// Tool implements the `config` tool: one input shape {setting, value?},
// dispatched to a get (value omitted) or a set (value supplied).
type Tool struct {
	cfg *config.Config
}

// New builds a config tool bound to cfg. cfg may be nil — Execute surfaces
// a clear error in that case.
func New(cfg *config.Config) *Tool { return &Tool{cfg: cfg} }

func (t *Tool) Name() string { return string(tools.CONFIG) }

// Description returns the dynamically-generated prompt body. Built from
// SUPPORTED_SETTINGS so the model's view of tunable settings always matches
// the registry (see prompt.go).
func (t *Tool) Description() string { return generatePrompt() }

func (t *Tool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"required":["setting"],
		"properties":{
			"setting":{"type":"string","description":"The setting key (e.g., \"display_thinking\", \"max_iterations\", \"openai.api_key\")"},
			"value":{"description":"The new value. Omit to read the current value. May be a string, boolean, or number depending on the setting."}
		}
	}`)
}

type input struct {
	Setting string `json:"setting"`
	// RawMessage so we can distinguish absent (GET) from present-but-falsy
	// (SET to false / 0 / ""). A *any would conflate {value:null} with
	// absence; RawMessage with len==0 is unambiguous.
	Value json.RawMessage `json:"value"`
}

func (t *Tool) Execute(_ context.Context, logger *slog.Logger, raw json.RawMessage) (tools.Result, error) {
	var in input
	if err := json.Unmarshal(raw, &in); err != nil {
		return errResult("config: bad input: %v", err), nil
	}
	if t.cfg == nil {
		return errResult("config: no config installed"), nil
	}

	sc, ok := Get(in.Setting)
	if !ok {
		return errResult("Unknown setting: %q", in.Setting), nil
	}

	// GET — value absent.
	if len(in.Value) == 0 {
		v := sc.Get(t.cfg)
		if sc.FormatOnRead != nil {
			v = sc.FormatOnRead(v)
		}
		return tools.Result{Content: fmt.Sprintf("%s = %v", in.Setting, v)}, nil
	}

	// SET — decode the value into the most permissive container, then let
	// the registry entry coerce to its native type.
	var value any
	if err := json.Unmarshal(in.Value, &value); err != nil {
		return errResult("config: bad value: %v", err), nil
	}

	// Options validation (string-typed settings only).
	if len(sc.Options) > 0 {
		s, _ := value.(string)
		if !slices.Contains(sc.Options, s) {
			return errResult("Invalid value %q. Options: %s",
				toString(value), strings.Join(sc.Options, ", ")), nil
		}
	}

	if err := sc.Set(t.cfg, value); err != nil {
		return errResult("%s: %s", in.Setting, err.Error()), nil
	}

	logger.Debug("config.set", "key", in.Setting, "value", value)
	return tools.Result{Content: fmt.Sprintf("Set %s to %v", in.Setting, value)}, nil
}

func errResult(format string, args ...any) tools.Result {
	return tools.Result{IsError: true, Content: fmt.Sprintf(format, args...)}
}
