package permission

import "testing"

func httpCall(input string) ToolCall {
	return ToolCall{Name: "http_request", Input: []byte(input)}
}

func TestHTTPRequestMethodGating(t *testing.T) {
	cases := []struct {
		name, input string
		want        Behavior
	}{
		{"GET allows", `{"method":"GET","url":"http://x"}`, BehaviorAllow},
		{"HEAD allows", `{"method":"HEAD","url":"http://x"}`, BehaviorAllow},
		{"unset method defaults to GET → allows", `{"url":"http://x"}`, BehaviorAllow},
		{"lowercase get allows", `{"method":"get","url":"http://x"}`, BehaviorAllow},
		{"POST asks", `{"method":"POST","url":"http://x","body":{}}`, BehaviorAsk},
		{"PUT asks", `{"method":"PUT","url":"http://x"}`, BehaviorAsk},
		{"DELETE asks", `{"method":"DELETE","url":"http://x"}`, BehaviorAsk},
		{"unparseable input asks (safe default)", `not json`, BehaviorAsk},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := Decide(httpCall(c.input), ModeDefault, nil, Hint{}, "", "")
			if d.Behavior != c.want {
				t.Errorf("got %v, want %v (%s)", d.Behavior, c.want, d.Reason)
			}
		})
	}
}

func TestHTTPRequestPlanMode(t *testing.T) {
	// A read-only GET is allowed even in plan mode (morally identical to web_fetch);
	// a mutating POST is denied outright like any other write in plan mode.
	if d := Decide(httpCall(`{"method":"GET","url":"http://x"}`), ModePlan, nil, Hint{}, "", ""); d.Behavior != BehaviorAllow {
		t.Errorf("plan-mode GET: got %v, want allow", d.Behavior)
	}
	if d := Decide(httpCall(`{"method":"POST","url":"http://x"}`), ModePlan, nil, Hint{}, "", ""); d.Behavior != BehaviorDeny {
		t.Errorf("plan-mode POST: got %v, want deny", d.Behavior)
	}
}

func TestHTTPRequestDenyRuleStillWins(t *testing.T) {
	// A deny rule must override the read-only auto-allow (deny rules win in step 2).
	store := NewStore()
	store.AddSessionRule(Rule{ToolName: "http_request", Behavior: BehaviorDeny, Source: SourceSession})
	if d := Decide(httpCall(`{"method":"GET","url":"http://x"}`), ModeDefault, store, Hint{}, "", ""); d.Behavior != BehaviorDeny {
		t.Errorf("deny rule should win over read auto-allow: got %v", d.Behavior)
	}
}

func TestHTTPRequestRuleMatching(t *testing.T) {
	call := func(method, url string) ToolCall {
		return ToolCall{Name: "http_request", Input: []byte(`{"method":"` + method + `","url":"` + url + `"}`)}
	}
	cases := []struct {
		name, content, method, url string
		want                       bool
	}{
		{"method + exact url", "POST http://h/halt", "POST", "http://h/halt", true},
		{"method mismatch", "POST http://h/halt", "GET", "http://h/halt", false},
		{"url mismatch", "POST http://h/halt", "POST", "http://h/strategy", false},
		{"prefix wildcard", "GET http://h/*", "GET", "http://h/status", true},
		{"method-agnostic url", "http://h/halt", "DELETE", "http://h/halt", true},
		{"tool-wide empty content matches all", "", "POST", "http://h/anything", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := Rule{ToolName: "http_request", Content: c.content}
			if got := matchToolCall(r, call(c.method, c.url)); got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

// TestHTTPRequestScopedLever is the RP-B scoped-lever scenario end-to-end through
// Decide: a member granted a narrow halt lever can POST /halt, but POST /strategy
// still asks, and reads stay auto-allowed. (Per-member scoping is the existing
// per-agent session-rule mechanism; this rule lives only in that member's store.)
func TestHTTPRequestScopedLever(t *testing.T) {
	store := NewStore()
	store.AddSessionRule(Rule{ToolName: "http_request", Content: "POST http://127.0.0.1:7777/halt", Behavior: BehaviorAllow})

	if d := Decide(httpCall(`{"method":"POST","url":"http://127.0.0.1:7777/halt","body":{}}`), ModeDefault, store, Hint{}, "", ""); d.Behavior != BehaviorAllow {
		t.Errorf("scoped halt lever: got %v, want allow", d.Behavior)
	}
	if d := Decide(httpCall(`{"method":"POST","url":"http://127.0.0.1:7777/strategy","body":{}}`), ModeDefault, store, Hint{}, "", ""); d.Behavior != BehaviorAsk {
		t.Errorf("unscoped strategy lever: got %v, want ask", d.Behavior)
	}
	if d := Decide(httpCall(`{"method":"GET","url":"http://127.0.0.1:7777/status"}`), ModeDefault, store, Hint{}, "", ""); d.Behavior != BehaviorAllow {
		t.Errorf("read: got %v, want allow", d.Behavior)
	}
}
