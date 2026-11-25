package routing

import (
	"github.com/sipeed/picoclaw/pkg/providers"
)

// defaultThreshold is used when the config threshold is zero or negative.
// At 0.35 a message needs at least one strong signal (code block, long text,
// or an attachment) before the heavy model is chosen.
const defaultThreshold = 0.35

// Default light-to-primary escalation guards. They keep a cheap model from
// spinning through a tool-heavy turn or a growing context forever.
const (
	defaultEscalationPromptTokens   = 32768
	defaultEscalationToolIterations = 3
)

// RouterConfig holds the validated model routing settings.
// It mirrors config.RoutingConfig but lives in pkg/routing to keep the
// dependency graph simple: pkg/agent resolves config → routing, not the reverse.
type RouterConfig struct {
	// LightModel is the model_name (from model_list) used for simple tasks.
	LightModel string

	// Threshold is the complexity score cutoff in [0, 1].
	// score >= Threshold → primary (heavy) model.
	// score <  Threshold → light model.
	Threshold float64

	// EscalationPromptTokens is the prompt-token count above which the rest of
	// the turn is sent to the primary model. Zero uses a safe default.
	EscalationPromptTokens int

	// EscalationToolIterations is the number of consecutive tool-calling
	// responses after which the rest of the turn is sent to the primary model.
	// Zero uses a safe default.
	EscalationToolIterations int
}

// Router selects the appropriate model tier for each incoming message.
// It is safe for concurrent use from multiple goroutines.
type Router struct {
	cfg        RouterConfig
	classifier Classifier
}

// New creates a Router with the given config and the default RuleClassifier.
// If cfg.Threshold is zero or negative, defaultThreshold (0.35) is used.
func New(cfg RouterConfig) *Router {
	if cfg.Threshold <= 0 {
		cfg.Threshold = defaultThreshold
	}
	if cfg.EscalationPromptTokens <= 0 {
		cfg.EscalationPromptTokens = defaultEscalationPromptTokens
	}
	if cfg.EscalationToolIterations <= 0 {
		cfg.EscalationToolIterations = defaultEscalationToolIterations
	}
	return &Router{
		cfg:        cfg,
		classifier: &RuleClassifier{},
	}
}

// newWithClassifier creates a Router with a custom Classifier.
// Intended for unit tests that need to inject a deterministic scorer.
func newWithClassifier(cfg RouterConfig, c Classifier) *Router {
	if cfg.Threshold <= 0 {
		cfg.Threshold = defaultThreshold
	}
	return &Router{cfg: cfg, classifier: c}
}

// SelectModel returns the model to use for this conversation turn along with
// the computed complexity score (for logging and debugging).
//
//   - If score < cfg.Threshold: returns (cfg.LightModel, true, score)
//   - Otherwise:               returns (primaryModel, false, score)
//
// The caller is responsible for resolving the returned model name into
// provider candidates (see AgentInstance.LightCandidates).
func (r *Router) SelectModel(
	msg string,
	history []providers.Message,
	primaryModel string,
) (model string, usedLight bool, score float64) {
	return r.SelectModelWithMedia(msg, history, primaryModel, false)
}

// SelectModelWithMedia is SelectModel plus an explicit signal that the current
// turn carries real media (providers.Message.Media / turnState.media).
//
// The text-only detection in ExtractFeatures cannot see media delivered as a
// structured field, so it would score an image-only turn as trivial and select
// the light model up front. hasMedia makes the tier decision itself correct: it
// is treated as a hard gate, so the primary model is selected and the reported
// score reflects it, matching the documented "attachments force the heavy
// model" behavior.
//
// This is not the only guard: before the LLM call the agent pipeline
// (routeMediaTurn) can still switch an already-selected light model to the
// primary or image model. That switch is a later safety net that also covers
// configured image models; it does not replace selecting the right tier here.
func (r *Router) SelectModelWithMedia(
	msg string,
	history []providers.Message,
	primaryModel string,
	hasMedia bool,
) (model string, usedLight bool, score float64) {
	features := ExtractFeatures(msg, history)
	features.HasAttachments = features.HasAttachments || hasMedia
	return r.selectFeatures(primaryModel, features)
}

// selectFeatures applies the classifier and threshold to choose a tier.
func (r *Router) selectFeatures(
	primaryModel string,
	features Features,
) (model string, usedLight bool, score float64) {
	score = r.classifier.Score(features)
	if score < r.cfg.Threshold {
		return r.cfg.LightModel, true, score
	}
	return primaryModel, false, score
}

// LightModel returns the configured light model name.
func (r *Router) LightModel() string {
	return r.cfg.LightModel
}

// Threshold returns the complexity threshold in use.
func (r *Router) Threshold() float64 {
	return r.cfg.Threshold
}

// EscalationPromptTokens returns the prompt-token guard used for mid-turn
// escalation from the light tier to the primary tier.
func (r *Router) EscalationPromptTokens() int {
	return r.cfg.EscalationPromptTokens
}

// EscalationToolIterations returns the consecutive-tool-response guard used
// for mid-turn escalation from the light tier to the primary tier.
func (r *Router) EscalationToolIterations() int {
	return r.cfg.EscalationToolIterations
}
