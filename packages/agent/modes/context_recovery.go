package modes

import (
	"context"
	"fmt"

	"github.com/bnema/zut/packages/core"
	"github.com/bnema/zut/packages/provider"
)

// ContextRecoveryOptions configures the host-owned, one-shot recovery for a
// provider context-overflow error. It deliberately does not add a terminal
// continuation policy to core.Agent.
type ContextRecoveryOptions struct {
	// PersistCompaction records the compacted transcript before the existing
	// prompt is continued. A persistence failure restores the pre-compaction
	// transcript and prevents the continuation request.
	PersistCompaction func([]provider.Message) error

	// SuppressInitialOverflowEvent hides the recoverable first-attempt
	// EvTurnEnd overflow from sinks whose protocol cannot retract a terminal
	// error frame, such as JSONL. A retry overflow is never suppressed.
	SuppressInitialOverflowEvent bool
}

// ContextRecoveryResult describes a completed host-level context recovery.
type ContextRecoveryResult struct {
	// OutputStart is the first message to append after a persisted compaction.
	// When Compacted is false, it is the message count before Prompt.
	OutputStart int
	Compacted   bool
}

// PromptWithContextRecovery appends prompt and runs it once. If that provider
// request exceeds its context window, it compacts the existing transcript and
// continues the already-appended prompt exactly once. Cancellation, a
// one-message transcript, a compaction failure, and a second overflow remain
// terminal outcomes.
func PromptWithContextRecovery(ctx context.Context, ag *core.Agent, prompt string, images []provider.ImageBlock, sink func(core.AgentEvent), options ContextRecoveryOptions) (result ContextRecoveryResult, err error) {
	result.OutputStart = len(ag.Messages())
	if sink == nil {
		sink = func(core.AgentEvent) {}
	}

	firstSink := sink
	if options.SuppressInitialOverflowEvent {
		firstSink = func(event core.AgentEvent) {
			if turnEnd, ok := event.(core.EvTurnEnd); ok && provider.IsContextOverflowError(turnEnd.Err) {
				return
			}
			sink(event)
		}
	}

	if err = ag.Prompt(ctx, prompt, images, firstSink); err == nil || ctx.Err() != nil || !provider.IsContextOverflowError(err) {
		return result, err
	}

	messages := ag.Messages()
	if len(messages) <= 1 {
		// The failed prompt is the only message. Summarizing it would replace
		// the user's task with an LLM-generated paraphrase.
		return result, err
	}
	keepTail := min(4, len(messages)-1)
	if _, compactErr := ag.Compact(ctx, keepTail, nil); compactErr != nil {
		return result, fmt.Errorf("compact transcript after initial overflow: %w", compactErr)
	}

	compacted := ag.Messages()
	if options.PersistCompaction != nil {
		if persistErr := options.PersistCompaction(compacted); persistErr != nil {
			// The session did not receive the compaction checkpoint, so restore
			// the original history plus the already-appended prompt rather than
			// let a later transcript flush make the session inconsistent.
			ag.SetMessages(messages)
			return result, fmt.Errorf("persist compacted transcript: %w", persistErr)
		}
	}

	result.OutputStart = len(compacted)
	result.Compacted = true
	return result, ag.Continue(ctx, sink)
}
