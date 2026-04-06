package routing

import (
	"context"
	"testing"

	"github.com/steveyegge/gastown/internal/config"
)

var testTiers = AgentTiers{
	Cheap:     "claude-haiku",
	Capable:   "claude-sonnet",
	Escalated: "claude-opus",
}

var testCfg = &config.SmartRoutingConfig{
	Enabled:          true,
	Strategy:         "static",
	SuccessThreshold: 0.85,
	MinSamples:       20,
	MaxAttempts:      3,
}

func TestStaticRouter_ChoreP3(t *testing.T) {
	r := &StaticRouter{}
	d := r.Route(context.Background(), RoutingInput{TaskType: "chore", Priority: 3}, testTiers, testCfg)
	if d.Agent != testTiers.Cheap {
		t.Errorf("expected cheap for chore P3, got %q", d.Agent)
	}
	if d.Reason != "static" {
		t.Errorf("expected reason=static, got %q", d.Reason)
	}
}

func TestStaticRouter_FeatureRoutsCapable(t *testing.T) {
	r := &StaticRouter{}
	d := r.Route(context.Background(), RoutingInput{TaskType: "feature", Priority: 1}, testTiers, testCfg)
	if d.Agent != testTiers.Capable {
		t.Errorf("expected capable for feature, got %q", d.Agent)
	}
}

func TestStaticRouter_BugP0RoutesCapable(t *testing.T) {
	r := &StaticRouter{}
	d := r.Route(context.Background(), RoutingInput{TaskType: "bug", Priority: 0}, testTiers, testCfg)
	if d.Agent != testTiers.Capable {
		t.Errorf("expected capable for P0 bug, got %q", d.Agent)
	}
}

func TestEscalationRouter_FirstAttemptDeclines(t *testing.T) {
	r := &EscalationRouter{}
	d := r.Route(context.Background(), RoutingInput{AttemptCount: 0}, testTiers, testCfg)
	if !d.Declined() {
		t.Errorf("expected decline on first attempt, got agent=%q", d.Agent)
	}
}

func TestEscalationRouter_SecondAttemptCapable(t *testing.T) {
	r := &EscalationRouter{}
	d := r.Route(context.Background(), RoutingInput{AttemptCount: 1, LastAgent: "claude-haiku"}, testTiers, testCfg)
	if d.Agent != testTiers.Capable {
		t.Errorf("expected capable on second attempt, got %q", d.Agent)
	}
	if d.FallbackAgent != testTiers.Escalated {
		t.Errorf("expected escalated as fallback, got %q", d.FallbackAgent)
	}
}

func TestEscalationRouter_ThirdAttemptEscalated(t *testing.T) {
	r := &EscalationRouter{}
	d := r.Route(context.Background(), RoutingInput{AttemptCount: 2}, testTiers, testCfg)
	if d.Agent != testTiers.Escalated {
		t.Errorf("expected escalated on third attempt, got %q", d.Agent)
	}
	if d.FallbackAgent != "" {
		t.Errorf("expected empty fallback at top tier, got %q", d.FallbackAgent)
	}
}

func TestChainRouter_EscalationTrumpsStatic(t *testing.T) {
	chain := &ChainRouter{routers: []ModelRouter{
		&EscalationRouter{},
		&StaticRouter{},
	}}
	// Chore P3 would be cheap via static, but attempt_count=1 forces escalation.
	d := chain.Route(context.Background(), RoutingInput{
		TaskType:     "chore",
		Priority:     3,
		AttemptCount: 1,
	}, testTiers, testCfg)
	if d.Agent != testTiers.Capable {
		t.Errorf("expected escalation to override static, got %q", d.Agent)
	}
	if d.Reason != "escalation" {
		t.Errorf("expected reason=escalation, got %q", d.Reason)
	}
}

func TestChainRouter_FallsThrough(t *testing.T) {
	chain := &ChainRouter{routers: []ModelRouter{
		&EscalationRouter{}, // declines on first attempt
		&HistoryRouter{},    // declines (no VictoriaMetrics)
		&StaticRouter{},     // decides
	}}
	d := chain.Route(context.Background(), RoutingInput{
		TaskType: "chore",
		Priority: 4,
	}, testTiers, testCfg)
	if d.Agent != testTiers.Cheap {
		t.Errorf("expected cheap from static fallback, got %q", d.Agent)
	}
	if d.Reason != "static" {
		t.Errorf("expected reason=static, got %q", d.Reason)
	}
}

func TestBuildRouter_Static(t *testing.T) {
	r := BuildRouter("static")
	if r == nil {
		t.Fatal("BuildRouter returned nil for 'static'")
	}
	d := r.Route(context.Background(), RoutingInput{TaskType: "chore", Priority: 3}, testTiers, testCfg)
	if d.Agent != testTiers.Cheap {
		t.Errorf("expected cheap, got %q", d.Agent)
	}
}

func TestBuildRouter_Chain(t *testing.T) {
	r := BuildRouter("history,static")
	if r == nil {
		t.Fatal("BuildRouter returned nil for 'history,static'")
	}
	// History declines (no VM), falls through to static.
	d := r.Route(context.Background(), RoutingInput{TaskType: "feature", Priority: 1}, testTiers, testCfg)
	if d.Agent != testTiers.Capable {
		t.Errorf("expected capable, got %q", d.Agent)
	}
}

func TestBuildRouter_Unknown(t *testing.T) {
	r := BuildRouter("nonexistent")
	if r != nil {
		t.Error("expected nil for unknown strategy")
	}
}

func TestBuildRouter_Empty(t *testing.T) {
	r := BuildRouter("")
	if r == nil {
		t.Fatal("BuildRouter returned nil for empty string")
	}
	// Should default to escalation+static
	d := r.Route(context.Background(), RoutingInput{TaskType: "task", Priority: 2}, testTiers, testCfg)
	if d.Declined() {
		t.Error("expected a decision, got decline")
	}
}

func TestClassifyRouter_Declines(t *testing.T) {
	r := &ClassifyRouter{}
	// No classify_model configured → should decline.
	d := r.Route(context.Background(), RoutingInput{}, testTiers, testCfg)
	if !d.Declined() {
		t.Errorf("expected decline without classify_model, got agent=%q", d.Agent)
	}
}

func TestHistoryRouter_Declines(t *testing.T) {
	r := &HistoryRouter{}
	// No VictoriaMetrics → should decline.
	d := r.Route(context.Background(), RoutingInput{TaskType: "bug"}, testTiers, testCfg)
	if !d.Declined() {
		t.Errorf("expected decline without VM, got agent=%q", d.Agent)
	}
}

func TestRegisterRouter_Custom(t *testing.T) {
	RegisterRouter("always-cheap", func() ModelRouter {
		return &alwaysCheapRouter{}
	})
	r := BuildRouter("always-cheap")
	if r == nil {
		t.Fatal("BuildRouter returned nil for custom router")
	}
	d := r.Route(context.Background(), RoutingInput{TaskType: "feature", Priority: 0}, testTiers, testCfg)
	if d.Agent != testTiers.Cheap {
		t.Errorf("expected cheap from custom router, got %q", d.Agent)
	}
	// Cleanup
	registryMu.Lock()
	delete(registry, "always-cheap")
	registryMu.Unlock()
}

// Test helper: a custom router that always picks the cheap tier.
type alwaysCheapRouter struct{}

func (r *alwaysCheapRouter) Name() string { return "always-cheap" }
func (r *alwaysCheapRouter) Route(_ context.Context, _ RoutingInput, tiers AgentTiers, _ *config.SmartRoutingConfig) RoutingDecision {
	return RoutingDecision{Agent: tiers.Cheap, Reason: "always-cheap", Confidence: 1.0}
}
