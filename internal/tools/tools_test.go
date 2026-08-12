package tools

import (
	"testing"

	"wproxyman/internal/models"
)

func newTestFlow(url, method string) *models.Flow {
	f := models.NewFlow()
	f.FullURL = url
	f.Method = method
	f.Host = "example.com"
	f.Path = "/api"
	return f
}

func TestMapLocalThroughEngine(t *testing.T) {
	eng := NewEngine()
	eng.SetConfig(&EngineConfig{
		MapLocal: MapLocalConfig{
			Enabled: true,
			Rules: []MapLocalRule{{
				Rule: Rule{
					ID: "r1", Name: "map", Enabled: true,
					Match: URLMatch{Pattern: "example.com/api", IsRegex: false},
				},
				Type: "inline", Body: `{"mapped":true}`,
				Headers: []models.Header{{Name: "X-Mapped", Value: "1"}},
			}},
		},
	})
	f := newTestFlow("http://example.com/api/users", "GET")
	decision, err := eng.OnRequest(f)
	if err != nil {
		t.Fatal(err)
	}
	if decision == nil || !decision.ShortCircuit {
		t.Fatalf("expected short circuit, got %+v", decision)
	}
	if string(f.ResponseBody) != `{"mapped":true}` {
		t.Fatalf("unexpected mapped body: %s", f.ResponseBody)
	}
	if models.HeaderValue(f.ResponseHeaders, "X-Mapped") != "1" {
		t.Fatal("mapped header missing")
	}
}

func TestBlockListThroughEngine(t *testing.T) {
	eng := NewEngine()
	eng.SetConfig(&EngineConfig{
		BlockList: BlockListConfig{
			Enabled: true,
			Rules: []BlockListRule{{
				Rule: Rule{
					ID: "b1", Name: "block-ads", Enabled: true,
					Match: URLMatch{Pattern: `.*\.doubleclick\.net/.*`, IsRegex: true},
				},
			}},
		},
	})
	f := newTestFlow("https://ad.doubleclick.net/pixel", "GET")
	decision, _ := eng.OnRequest(f)
	if decision == nil || !decision.ShortCircuit {
		t.Fatalf("expected block, got %+v", decision)
	}
	if f.ResponseStatus != 404 {
		t.Fatalf("expected 404, got %d", f.ResponseStatus)
	}
	// Non-matching flows pass through
	f2 := newTestFlow("https://example.com/ok", "GET")
	decision2, _ := eng.OnRequest(f2)
	if decision2 != nil {
		t.Fatalf("expected no decision, got %+v", decision2)
	}
}

func TestBreakpointPauseResume(t *testing.T) {
	eng := NewEngine()
	eng.SetConfig(&EngineConfig{
		Breakpoints: BreakpointConfig{
			Enabled: true,
			Rules: []BreakpointRule{{
				Rule:   Rule{ID: "bp1", Name: "pause", Enabled: true, Match: URLMatch{Pattern: "example.com"}},
				Phases: []string{"request"},
			}},
		},
	})
	f := newTestFlow("http://example.com/edit", "POST")
	decision, err := eng.OnRequest(f)
	if err != nil {
		t.Fatal(err)
	}
	if decision == nil || !decision.Wait {
		t.Fatalf("expected wait decision, got %+v", decision)
	}
	if !f.WaitingForDecision {
		t.Fatal("flow should be marked waiting")
	}

	go func() {
		modified := *f
		modified.Method = "PUT"
		modified.RequestBody = []byte("edited")
		_ = eng.ResolveBreakpoint(f.ID, &modified)
	}()

	decision2, err := eng.WaitForDecision(f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if decision2 != nil {
		t.Fatalf("expected nil decision after resume, got %+v", decision2)
	}
	if f.Method != "PUT" {
		t.Fatalf("flow was not modified by breakpoint resolution: %s", f.Method)
	}
	if string(f.RequestBody) != "edited" {
		t.Fatalf("body not updated: %s", f.RequestBody)
	}
}

func TestScriptingModifiesRequest(t *testing.T) {
	eng := NewEngine()
	eng.SetConfig(&EngineConfig{
		Scripts: ScriptingConfig{
			Enabled: true,
			Scripts: []ScriptEntry{{
				ID: "s1", Name: "add-header", Enabled: true,
				Match: URLMatch{Pattern: "example.com"},
				Code: `
function onRequest(context) {
	context.request.setHeader("X-Injected", "yes");
	context.request.body = "patched-body";
}
`,
			}},
		},
	})
	f := newTestFlow("http://example.com/x", "POST")
	f.RequestBody = []byte("orig")
	decision, err := eng.OnRequest(f)
	if err != nil {
		t.Fatal(err)
	}
	if decision != nil {
		t.Fatalf("expected no decision, got %+v", decision)
	}
	if models.HeaderValue(f.RequestHeaders, "X-Injected") != "yes" {
		t.Fatal("script did not inject header")
	}
	if string(f.RequestBody) != "patched-body" {
		t.Fatalf("script did not patch body: %q", f.RequestBody)
	}
}

func TestAllowListSkipsCapture(t *testing.T) {
	eng := NewEngine()
	eng.SetConfig(&EngineConfig{
		AllowList: AllowListConfig{
			Enabled: true,
			Rules: []AllowListRule{{
				Rule: Rule{ID: "a1", Name: "allow-api", Enabled: true,
					Match: URLMatch{Pattern: "example.com/api"}},
			}},
		},
	})
	// Non-matching → SkipCapture
	f := newTestFlow("https://tracker.example.com/pixel", "GET")
	decision, _ := eng.OnRequest(f)
	if decision == nil || !decision.SkipCapture {
		t.Fatalf("expected skip capture, got %+v", decision)
	}
	// Matching → normal pass
	f2 := newTestFlow("https://example.com/api/v1", "GET")
	decision2, _ := eng.OnRequest(f2)
	if decision2 != nil {
		t.Fatalf("expected no decision for allowed flow, got %+v", decision2)
	}
}
