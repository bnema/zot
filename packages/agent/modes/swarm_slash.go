package modes

import (
	"context"
	"fmt"
	"strings"

	"github.com/patriceckhart/zot/packages/agent/swarm"
	"github.com/patriceckhart/zot/packages/provider"
	"github.com/patriceckhart/zot/packages/tui"
)

// runSwarm dispatches /swarm subcommands. Layout:
//
//	/swarm                       -> open the dashboard
//	/swarm list                  -> open the dashboard
//	/swarm new [--agent A] [--model M] [--provider P] [--reasoning L] <task...>
//	                             -> spawn an agent with optional profile/model overrides
//	/swarm kill <id>             -> stop a running agent
//	/swarm remove <id>           -> tear down a terminated agent
//	/swarm logs <id>             -> open the scrollable transcript view
//	/swarm send <id> <text...>   -> send a follow-up user turn to <id>
//	/swarm resume [id]           -> resume an agent (omit id to pick from a list)
//	/swarm attach <id>           -> (planned) drop into the agent's TUI
//
// When cfg.Swarm is nil the command tells the user the feature is
// disabled instead of pretending to work.
func (i *Interactive) runSwarm(ctx context.Context, args []string) {
	if i.cfg.Swarm == nil {
		i.mu.Lock()
		i.statusErr = "swarm is disabled in this build"
		i.statusOK = ""
		i.mu.Unlock()
		return
	}

	sub := ""
	rest := ""
	if len(args) > 0 {
		sub = strings.ToLower(args[0])
		// Guard the args[1:] reslice: when only the subcommand was
		// typed (e.g. bare "/swarm new"), args has length 1 and the
		// naive args[1:] is fine, but when args is empty (bare
		// "/swarm") the reslice is [1:0] and panics. The len>0 branch
		// here keeps both cases safe.
		if len(args) > 1 {
			rest = strings.TrimSpace(strings.Join(args[1:], " "))
		}
	}

	// spawnAdapter, sendAdapter, resumeAdapter wrap the Swarm methods
	// in the signatures the dialog expects. Defined once here so the
	// three Open()-shaped entry points (list, logs/view-jump, resume)
	// feed the dialog identical callbacks.
	spawnAdapter := func(task, model, provider string) error {
		_, err := i.cfg.Swarm.SpawnReq(ctx, swarm.SpawnRequest{
			Task: task, Model: model, Provider: provider, Reasoning: i.cfg.Reasoning,
		})
		return err
	}
	sendAdapter := func(id, text string) error {
		return i.cfg.Swarm.SendUserTurn(id, text)
	}
	resumeAdapter := func(id string) error {
		_, err := i.cfg.Swarm.ResumeSession(ctx, id)
		return err
	}

	// Pin every fresh spawn to whatever the host's /model selection
	// is right now. This is captured at /swarm time — if the user
	// wants a different model for the next swarm agent, they pick it
	// via /model first (globally), or, while inside the spawn
	// editor, by typing /model on its own line to pop the picker.
	i.swarmDialog.SetCompactMode(i.compactModeEnabled())
	i.swarmDialog.SetLineInput(tui.NormalizeInputStyle(i.cfg.TUIInputStyle) == tui.InputStyleLines)
	i.swarmDialog.SetCurrentModel(i.cfg.Model, i.cfg.Provider)
	if i.cfg.LoggedInProviders != nil {
		i.swarmDialog.SetLoggedInProviders(i.cfg.LoggedInProviders())
	}

	switch sub {
	case "", "list", "ls", "ps":
		i.swarmDialog.Open(
			i.cfg.Swarm.SnapshotAll,
			i.cfg.Swarm.Stop,
			i.cfg.Swarm.Remove,
			spawnAdapter,
			sendAdapter,
			resumeAdapter,
			i.cfg.CWD,
		)
	case "new", "spawn":
		if rest == "" {
			i.swarmStatus("", "/swarm new <task>: missing task")
			return
		}
		// Permit profile/model/provider/reasoning flags before the task
		// so scripts can pin a child without going through the dialog.
		// Anything that isn't a recognised flag terminates parsing and
		// the rest becomes the task; only leading flags are consumed.
		model, provider, reasoning, subagent, task := parseSpawnFlags(rest)
		var profileTools []string
		var fastModeOverride *bool
		if task == "" {
			i.swarmStatus("", "/swarm new: missing task (after any spawn flags)")
			return
		}
		if subagent != "" {
			if i.cfg.ResolveSubagent == nil {
				i.swarmStatus("", "spawn: named subagent profiles are unavailable")
				return
			}
			profile, err := i.cfg.ResolveSubagent(subagent)
			if err != nil {
				i.swarmStatus("", "spawn: "+err.Error())
				return
			}
			if profile == nil {
				i.swarmStatus("", "spawn: unknown subagent profile "+subagent)
				return
			}
			subagent = profile.Name
			profileTools = append([]string(nil), profile.Tools...)
			fastModeOverride = profile.FastMode
			profileProvider, profileModel := profile.ModelSelection()
			if model == "" {
				model = profileModel
			}
			if provider == "" {
				provider = profileProvider
			}
			if reasoning == "" {
				reasoning = profile.Thinking
			}
		}
		if model == "" {
			model = i.cfg.Model
		}
		if provider == "" {
			provider = i.cfg.Provider
		}
		if reasoning == "" {
			reasoning = i.cfg.Reasoning
		}
		if reasoning != "" {
			normalizedReasoning, err := normalizeSpawnReasoning(reasoning)
			if err != nil {
				i.swarmStatus("", "spawn: "+err.Error())
				return
			}
			reasoning = normalizedReasoning
		}
		a, err := i.cfg.Swarm.SpawnReq(ctx, swarm.SpawnRequest{
			Task: task, Model: model, Provider: provider, Reasoning: reasoning,
			FastMode: fastModeOverride, Subagent: subagent,
			Tools: profileTools,
		})
		if err != nil {
			i.swarmStatus("", "spawn: "+err.Error())
			return
		}
		status := "spawned " + a.ID
		if subagent != "" {
			status += " (agent " + subagent + ")"
		} else if model != "" {
			status += " (model " + model + ")"
		}
		i.swarmStatus(status, "")
	case "kill", "stop":
		if rest == "" {
			i.swarmStatus("", "/subagents kill <id>: missing id")
			return
		}
		if err := i.cfg.Swarm.Stop(rest); err != nil {
			i.swarmStatus("", "kill: "+err.Error())
			return
		}
		i.swarmStatus("stopped "+rest, "")
	case "cancel":
		if rest == "" {
			i.swarmStatus("", "/subagents cancel <id>: missing id")
			return
		}
		if err := i.cfg.Swarm.CancelTurn(rest); err != nil {
			i.swarmStatus("", "cancel: "+err.Error())
			return
		}
		i.swarmStatus("canceling turn "+rest, "")
	case "remove", "rm":
		if rest == "" {
			i.swarmStatus("", "/swarm remove <id>: missing id")
			return
		}
		if err := i.cfg.Swarm.Remove(rest); err != nil {
			i.swarmStatus("", "remove: "+err.Error())
			return
		}
		i.swarmStatus("removed "+rest, "")
	case "logs", "log", "view":
		if rest == "" {
			i.swarmStatus("", "/swarm logs <id>: missing id")
			return
		}
		ok := i.swarmDialog.OpenViewing(
			rest,
			i.cfg.Swarm.SnapshotAll,
			i.cfg.Swarm.Stop,
			i.cfg.Swarm.Remove,
			spawnAdapter,
			sendAdapter,
			resumeAdapter,
			i.cfg.CWD,
		)
		if !ok {
			i.swarmStatus("", "/swarm logs: no agent matching "+rest)
		}
	case "result", "inspect":
		if rest == "" {
			i.swarmStatus("", "/subagents result <id>: missing id")
			return
		}
		result, err := i.cfg.Swarm.ReadResult(rest)
		if err != nil {
			i.swarmStatus("", "result: "+err.Error())
			return
		}
		status := fmt.Sprintf("%s result %s", result.Status, i.cfg.Swarm.ResultReference(rest))
		if result.Summary != "" {
			status += ": " + truncateStatus(result.Summary, 160)
		}
		i.swarmStatus(status, "")
	case "wait":
		if rest == "" {
			i.swarmStatus("", "/subagents wait <id>: missing id")
			return
		}
		a := i.cfg.Swarm.Get(rest)
		if a == nil {
			i.swarmStatus("", "wait: no such agent "+rest)
			return
		}
		go func() {
			a.Wait()
			i.swarmStatus("completed "+a.ID, "")
		}()
		i.swarmStatus("waiting for "+a.ID, "")
	case "resume-session", "resume", "reattach", "reopen":
		if rest == "" {
			count := i.swarmDialog.OpenForResume(
				i.cfg.Swarm.SnapshotAll,
				i.cfg.Swarm.Stop,
				i.cfg.Swarm.Remove,
				spawnAdapter,
				sendAdapter,
				resumeAdapter,
				i.cfg.CWD,
			)
			switch count {
			case 0:
				i.swarmStatus("", "/subagents resume-session: no resumable agents")
			case 1:
				i.swarmStatus("1 resumable agent, press R to resume", "")
			default:
				i.swarmStatus(fmt.Sprintf("%d resumable agents, ↑/↓ to pick, R to resume", count), "")
			}
			return
		}
		a, err := i.cfg.Swarm.ResumeSession(ctx, rest)
		if err != nil {
			i.swarmStatus("", "resume-session: "+err.Error())
			return
		}
		i.swarmStatus("resumed "+a.ID, "")
	case "restart-task", "restart":
		if rest == "" {
			i.swarmStatus("", "/subagents restart-task <id>: missing id")
			return
		}
		a, err := i.cfg.Swarm.RestartTask(ctx, rest)
		if err != nil {
			i.swarmStatus("", "restart-task: "+err.Error())
			return
		}
		i.swarmStatus("restarted task "+a.ID, "")
	case "send", "prompt", "msg":
		// /swarm send <id> <text...> is the non-interactive
		// counterpart of pressing 'p' in the dashboard. We split the
		// joined `rest` ourselves rather than reusing the dispatcher's
		// already-fielded args[] because the text may contain spaces
		// the user expects to be preserved verbatim.
		id, text := splitIDAndRest(rest)
		if id == "" {
			i.swarmStatus("", "/swarm send <id> <text>: missing id")
			return
		}
		if text == "" {
			i.swarmStatus("", "/swarm send <id> <text>: missing text")
			return
		}
		if err := i.cfg.Swarm.SendUserTurn(id, text); err != nil {
			i.swarmStatus("", friendlySendErr(id, err))
			return
		}
		i.swarmStatus("sent to "+id, "")
	case "attach":
		// PTY-reparenting is a significant chunk of work I haven't
		// landed yet (see the design sketch). Recognise the name so
		// /swarm attach doesn't fall through to the generic "unknown
		// subcommand" path — that error message is misleading because
		// it makes attach sound like a typo instead of a planned
		// feature. Point the user at /swarm logs in the meantime.
		i.swarmStatus("", "/swarm attach: not implemented yet (needs PTY reparenting). Use /swarm logs "+firstWord(rest)+" to watch its transcript.")
	default:
		i.swarmStatus("", "/subagents: unknown subcommand "+sub+" (try list / new / kill / remove / logs / send / result / resume-session / restart-task)")
	}
}

// normalizeSpawnReasoning validates and canonicalizes the reasoning levels
// accepted by ParseArgs before a child process is launched.
func normalizeSpawnReasoning(value string) (string, error) {
	raw := strings.ToLower(strings.TrimSpace(value))
	switch raw {
	case "", "off":
		return raw, nil
	case "minimal", "minimum", "low", "medium", "high", "xhigh", "maximum", "max":
		return provider.NormalizeReasoning(raw), nil
	default:
		return "", fmt.Errorf("reasoning must be off|minimum|low|medium|high|xhigh|max")
	}
}

// parseSpawnFlags consumes leading profile/model/provider/reasoning flags
// using both separate-value and equals forms. Flags after the task remain
// part of the task text.
func parseSpawnFlags(s string) (model, provider, reasoning, subagent, task string) {
	fields := strings.Fields(s)
	i := 0
	for i < len(fields) {
		f := fields[i]
		switch {
		case f == "--agent":
			if i+1 < len(fields) && !strings.HasPrefix(fields[i+1], "--") {
				subagent = fields[i+1]
				i += 2
			} else {
				i++
			}
			continue
		case strings.HasPrefix(f, "--agent="):
			subagent = strings.TrimPrefix(f, "--agent=")
			i++
			continue
		case f == "--model":
			// Consume the flag even when no value follows so a
			// dangling "--model" doesn't leak into the task. The
			// caller surfaces "missing task" instead.
			if i+1 < len(fields) && !strings.HasPrefix(fields[i+1], "--") {
				model = fields[i+1]
				i += 2
			} else {
				i++
			}
			continue
		case strings.HasPrefix(f, "--model="):
			model = strings.TrimPrefix(f, "--model=")
			i++
			continue
		case f == "--provider":
			if i+1 < len(fields) && !strings.HasPrefix(fields[i+1], "--") {
				provider = fields[i+1]
				i += 2
			} else {
				i++
			}
			continue
		case strings.HasPrefix(f, "--provider="):
			provider = strings.TrimPrefix(f, "--provider=")
			i++
			continue
		case f == "--reasoning" || f == "--thinking":
			if i+1 < len(fields) && !strings.HasPrefix(fields[i+1], "--") {
				reasoning = fields[i+1]
				i += 2
			} else {
				i++
			}
			continue
		case strings.HasPrefix(f, "--reasoning="):
			reasoning = strings.TrimPrefix(f, "--reasoning=")
			i++
			continue
		case strings.HasPrefix(f, "--thinking="):
			reasoning = strings.TrimPrefix(f, "--thinking=")
			i++
			continue
		}
		break
	}
	task = strings.TrimSpace(strings.Join(fields[i:], " "))
	return
}

// splitIDAndRest splits "<id> <text...>" into (id, text). The text
// half preserves all whitespace after the first token so the agent
// receives the user's prompt verbatim (modulo a single trim of the
// boundary space). Returns ("", "") when s is empty so the caller
// can surface a missing-id error.
func splitIDAndRest(s string) (id, text string) {
	s = strings.TrimLeft(s, " \t")
	if s == "" {
		return "", ""
	}
	cut := strings.IndexAny(s, " \t")
	if cut < 0 {
		return s, ""
	}
	return s[:cut], strings.TrimLeft(s[cut+1:], " \t")
}

// firstWord returns the first whitespace-separated token of s, or
// "<id>" when s is empty. Used to keep the "/swarm attach" hint
// readable even when the user typed no argument.
func firstWord(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "<id>"
	}
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		return s[:i]
	}
	return s
}

func truncateStatus(value string, max int) string {
	value = strings.ReplaceAll(strings.TrimSpace(value), "\n", " ")
	if len([]rune(value)) <= max {
		return value
	}
	if max <= 3 {
		return string([]rune(value)[:max])
	}
	return string([]rune(value)[:max-3]) + "..."
}

func (i *Interactive) swarmStatus(ok, errMsg string) {
	i.mu.Lock()
	i.statusOK = ok
	i.statusErr = errMsg
	i.mu.Unlock()
	i.invalidate()
}
