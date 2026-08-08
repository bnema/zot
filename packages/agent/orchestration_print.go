package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/bnema/zut/packages/agent/modes"
	"github.com/bnema/zut/packages/agent/subagents"
	"github.com/bnema/zut/packages/core"
	"github.com/bnema/zut/packages/provider"
)

// runOrchestratedPrintMode is deliberately separate from ordinary print mode:
// ordinary print continues to stream its existing one-turn implementation,
// while this path keeps every parent turn in memory until terminal synthesis.
func runOrchestratedPrintMode(parentCtx context.Context, args Args, version string) (runErr error) {
	ctx, stopSignal := signal.NotifyContext(parentCtx, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stopSignal()

	if args.NoYolo {
		fmt.Fprintln(os.Stderr, "warning: --no-yolo has no effect in print mode (no interactive prompt available); tools will run without confirmation")
	}
	r, err := Resolve(args, true)
	if err != nil {
		return err
	}
	initialCfg, err := LoadConfig()
	if err != nil {
		return fmt.Errorf("load config for orchestrated print: %w", err)
	}
	prompt := args.Prompt
	if prompt == "" {
		piped, readErr := readAllStdin()
		if readErr != nil {
			return fmt.Errorf("read print prompt from stdin: %w", readErr)
		}
		prompt = strings.TrimSpace(piped)
	}
	if prompt == "" {
		return fmt.Errorf("print mode requires a prompt (arg or stdin)")
	}
	extMgr, stopExt := setupNonInteractiveExtensions(ctx, args, &r, version)
	defer stopExt()

	tracker := subagents.NewCompletionTracker()
	onSpawned := func(a *subagents.Agent, task string) {
		tracker.TrackTurn(a, task, false)
	}
	onResumed := func(a *subagents.Agent, prompt string) {
		// BeforeResumed owns the strict future-turn registration. Keep this
		// post-delivery callback for the same lifecycle semantics as the
		// interactive flow; SubagentResumeTool suppresses it when the pre-hook
		// is present so it cannot register a duplicate completion.
		tracker.TrackTurn(a, prompt, true)
	}
	onBeforeResumed := func(a *subagents.Agent, prompt string) func() {
		return tracker.TrackFutureTurn(a, prompt, true)
	}
	onStopRequested := func(a *subagents.Agent) {
		tracker.TrackExit(a, "stopped")
	}
	runtime := newSubagentRuntime(subagentRuntimeConfig{
		Context:         ctx,
		Args:            args,
		Root:            filepath.Join(ZutHome(), "subagents"),
		RepoRoot:        r.CWD,
		Provider:        r.Provider,
		Model:           r.Model,
		Reasoning:       r.Reasoning,
		BaseURL:         r.BaseURL,
		InsecureTLS:     r.InsecureTLS,
		FastMode:        r.FastMode,
		APIKey:          args.APIKey,
		Policy:          subagentPolicyFromConfig(initialCfg.Subagents),
		WebSearchPolicy: webSearchPolicyForRegistry(r.WebSearchPolicy, r.ToolRegistry),
		OnSpawned:       onSpawned,
		OnResumed:       onResumed,
		BeforeResumed:   onBeforeResumed,
		OnStopRequested: onStopRequested,
	})
	defer func() {
		if closeErr := closeSubagentRuntimeFresh(runtime); closeErr != nil {
			runErr = errors.Join(runErr, closeErr)
		}
	}()

	prepareRegistry := func(reg core.Registry) core.Registry {
		return runtime.PrepareRegistry(reg)
	}
	runtime.SetProvider(r.Provider)
	runtime.SetProviderSettings(r.BaseURL, r.InsecureTLS)
	runtime.SetFastMode(r.FastMode)
	runtime.PrepareResolvedRegistry(r.ToolRegistry, r.WebSearchPolicy)
	ag := r.NewAgent()
	initialAg := ag
	defer func() {
		closeAgentLSP(ag)
		if ag != initialAg {
			closeAgentLSP(initialAg)
		}
	}()
	wireNonInteractiveAgentExtHooks(ctx, ag, extMgr)

	sess, err := openOrCreateSession(ctx, args, r, ag, version)
	if err != nil {
		return err
	}
	if sess != nil {
		defer func() { runErr = joinSessionCloseError(runErr, sess) }()
		var providerName, model string
		sess, ag, providerName, model, err = applyInitialSessionResumeWithRuntime(ctx, args, r, extMgr, sess, ag, runtime)
		if err != nil {
			return err
		}
		r.Provider, r.Model = providerName, model
		ag.OnToolResult = func(_ string, result core.ToolResult) { persistExtensionToolResult(extMgr, sess, result) }
		runtime.SetActiveSession(sess.ID)
	}
	announceSession(extMgr, sess)

	start := len(ag.Messages())
	if err := runZutfileStartupPre(ctx, args.StartupPre, r.CWD, r.Sandbox, ag, nil, os.Stderr); err != nil {
		return err
	}
	if strings.TrimSpace(args.StartupPre) != "" {
		refreshedPolicy, refreshErr := reloadResourcesAfterStartupPreWithRegistry(ctx, args, extMgr, r.Sandbox, ag, prepareRegistry)
		if refreshErr != nil {
			return refreshErr
		}
		// entry.pre can change the resolved PermissionSet. Apply that effective
		// policy before any child launch, not only to the parent's registry.
		runtime.SetWebSearchPolicy(refreshedPolicy)
	}
	var usagePersistErr error
	if sess != nil {
		ag.OnUsage = func(cumulative provider.Usage) {
			if usagePersistErr == nil {
				usagePersistErr = sess.AppendUsage(ag.LastTurnUsage(), cumulative)
			}
		}
	}
	var persistCompaction func([]provider.Message) error
	if sess != nil {
		persistCompaction = func(messages []provider.Message) error {
			return sess.AppendCompactionWithUsage(messages, ag.Cost())
		}
	}

	var totalUsage provider.Usage
	transcriptStart := start
	runParent := func(turnCtx context.Context, text string) (string, error) {
		var captured bytes.Buffer
		usage, recovery, turnErr := modes.RunPrintWithContextRecovery(turnCtx, ag, text, nil, &captured, persistCompaction)
		totalUsage = totalUsage.Add(usage)
		if recovery.Compacted {
			transcriptStart = recovery.OutputStart
		}
		if usagePersistErr != nil {
			return captured.String(), usagePersistErr
		}
		if turnErr != nil {
			return captured.String(), turnErr
		}
		return strings.TrimSuffix(captured.String(), "\n"), nil
	}

	finalText, err := runParent(ctx, prompt)
	if err == nil {
		finalText, err = runHeadlessContinuation(ctx, tracker, finalText, runParent)
	}
	if persistErr := WriteNewTranscript(ag, sess, transcriptStart); persistErr != nil {
		err = errors.Join(err, persistErr)
	}
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(os.Stdout, finalText); err != nil {
		return err
	}
	return nil
}
