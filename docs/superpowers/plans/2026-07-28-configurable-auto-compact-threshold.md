# Configurable Auto-Compact Threshold Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add persisted auto-compaction threshold presets to the interactive TUI settings.

**Architecture:** Keep automatic triggering in `packages/agent/modes`. Thread an optional integer percentage from persisted `agent.Config` into `modes.InteractiveConfig`, normalize missing or invalid values to 85%, and let zero disable only percentage-based triggers. Reuse the settings dialog's existing fixed-option picker and settings-store persistence path.

**Tech Stack:** Go, standard library, Zot's existing TUI settings dialog, JSON configuration.

## Global Constraints

- Supported values are `0` (off), `70`, `80`, `85`, and `90`.
- Missing or invalid persisted values resolve to `85`.
- Disabling the threshold does not disable manual compaction or HTTP 413 recovery.
- Do not add dependencies or change non-interactive compaction behavior.
- Preserve unrelated working-tree changes and stage only this feature.

---

### Task 1: Configuration and threshold decision

**Files:**
- Modify: `packages/agent/config.go`
- Modify: `packages/agent/modes/interactive.go`
- Create: `packages/agent/modes/auto_compact_test.go`
- Modify: `packages/agent/cli.go`

**Interfaces:**
- Produces: `Config.AutoCompactThreshold *int`
- Produces: `InteractiveConfig.AutoCompactThreshold *int`
- Produces: `normalizeAutoCompactThreshold(*int) int`
- Produces: `shouldAutoCompact(inputTokens, contextWindow, thresholdPercent int) bool`

- [ ] **Step 1: Write failing normalization and decision tests**

Add table-driven tests proving nil and invalid values resolve to 85, supported presets survive unchanged, zero disables percentage checks, and 70/80/85/90 trigger exactly at their boundaries.

```go
func TestNormalizeAutoCompactThreshold(t *testing.T) {
	tests := []struct {
		name string
		value *int
		want int
	}{
		{name: "missing", value: nil, want: 85},
		{name: "off", value: intPtr(0), want: 0},
		{name: "seventy", value: intPtr(70), want: 70},
		{name: "invalid", value: intPtr(42), want: 85},
	}
	// Assert normalizeAutoCompactThreshold(test.value) == test.want.
}

func TestShouldAutoCompactUsesConfiguredThreshold(t *testing.T) {
	if shouldAutoCompact(84, 100, 85) {
		t.Fatal("84% must remain below an 85% threshold")
	}
	if !shouldAutoCompact(85, 100, 85) {
		t.Fatal("85% must trigger an 85% threshold")
	}
	if shouldAutoCompact(100, 100, 0) {
		t.Fatal("off must disable percentage-triggered compaction")
	}
}
```

- [ ] **Step 2: Run tests and verify RED**

Run:

```sh
go test ./packages/agent/modes -run 'TestNormalizeAutoCompactThreshold|TestShouldAutoCompactUsesConfiguredThreshold' -count=1
```

Expected: compilation failure because the normalization and decision helpers do not exist.

- [ ] **Step 3: Add optional configuration and minimal helpers**

Add `AutoCompactThreshold *int` with JSON name `auto_compact_threshold` to both configuration structs. Implement normalization for exactly `0`, `70`, `80`, `85`, and `90`, defaulting all other values to `85`. Replace the hard-coded fraction check with the normalized percentage helper and pass the config field through `cli.go`.

- [ ] **Step 4: Run focused tests and verify GREEN**

Run:

```sh
go test ./packages/agent/modes -run 'TestNormalizeAutoCompactThreshold|TestShouldAutoCompactUsesConfiguredThreshold' -count=1
```

Expected: PASS.

### Task 2: Persistence and TUI preset picker

**Files:**
- Modify: `packages/agent/settings_store.go`
- Modify: `packages/agent/settings_store_test.go`
- Modify: `packages/agent/modes/interactive.go`
- Modify: `packages/agent/modes/auto_compact_test.go`

**Interfaces:**
- Extends: `SettingsStore.SetAutoCompactThreshold(percent int) error`
- Consumes: `normalizeAutoCompactThreshold(*int) int`

- [ ] **Step 1: Write failing persistence and settings-item tests**

Add a settings-store test that writes `70` into an isolated `$ZOT_HOME` and verifies `auto_compact_threshold` plus an unrelated config field. Add a modes test that opens settings with missing configuration and verifies the option values are `0`, `70`, `80`, `85`, `90` with `85` selected.

```go
func TestConfigSettingsStorePersistsAutoCompactThreshold(t *testing.T) {
	t.Setenv("ZOT_HOME", t.TempDir())
	if err := SaveConfig(Config{Theme: "dark"}); err != nil {
		t.Fatal(err)
	}
	if err := (configSettingsStore{}).SetAutoCompactThreshold(70); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AutoCompactThreshold == nil || *cfg.AutoCompactThreshold != 70 {
		t.Fatalf("auto_compact_threshold = %v, want 70", cfg.AutoCompactThreshold)
	}
	if cfg.Theme != "dark" {
		t.Fatalf("unrelated config changed: theme = %q, want dark", cfg.Theme)
	}
}
```

- [ ] **Step 2: Run tests and verify RED**

Run:

```sh
go test ./packages/agent ./packages/agent/modes -run 'AutoCompactThreshold|SettingsDialog.*AutoCompact' -count=1
```

Expected: compilation or assertion failure because persistence and the picker row do not exist.

- [ ] **Step 3: Implement persistence and live setting application**

Add the settings-store method, fixed picker options, choice resolution, action routing, integer parsing, live `InteractiveConfig` update, and status message. Persist only values exposed by the picker; normalize the live value defensively before use.

- [ ] **Step 4: Run focused tests and verify GREEN**

Run:

```sh
go test ./packages/agent ./packages/agent/modes -run 'AutoCompactThreshold|SettingsDialog.*AutoCompact' -count=1
```

Expected: PASS.

### Task 3: Documentation and full verification

**Files:**
- Modify: `README.md`
- Review: `docs/superpowers/specs/2026-07-28-configurable-auto-compact-threshold-design.md`
- Review: `docs/superpowers/plans/2026-07-28-configurable-auto-compact-threshold.md`

**Interfaces:**
- Documents: `auto_compact_threshold`
- Documents: `/settings` presets and `off` semantics

- [ ] **Step 1: Update user documentation**

Document `off`, `70%`, `80%`, `85%` default, and `90%` in the `/settings` section. Update `/compact` to state that threshold-based auto-compaction is configurable and that payload-too-large recovery remains active when the percentage trigger is off.

- [ ] **Step 2: Format changed Go files**

Run:

```sh
gofmt -w packages/agent/config.go packages/agent/settings_store.go packages/agent/settings_store_test.go packages/agent/cli.go packages/agent/modes/interactive.go packages/agent/modes/auto_compact_test.go
```

- [ ] **Step 3: Run the complete test suite**

Run:

```sh
go test ./...
```

Expected: PASS.

- [ ] **Step 4: Run static analysis**

Run:

```sh
go vet ./...
```

Expected: PASS.

- [ ] **Step 5: Inspect the final change**

Run:

```sh
git diff --check
git status --short
git diff
```

Confirm only the configuration, TUI, tests, README, design, and plan belong to the feature.

### Task 4: Publish the pull request

**Files:**
- Stage only the files listed in Tasks 1-3.

**Interfaces:**
- Produces: branch `codex/configurable-auto-compact-threshold`
- Produces: one feature commit and a ready GitHub pull request

- [ ] **Step 1: Create the feature branch**

```sh
git switch -c codex/configurable-auto-compact-threshold
```

- [ ] **Step 2: Stage explicit feature paths and review**

Stage only the implementation, tests, README, design, and plan. Review `git diff --cached --check` and `git diff --cached`.

- [ ] **Step 3: Commit**

```sh
git commit -m "Make auto-compact threshold configurable"
```

- [ ] **Step 4: Push the branch**

Push to the appropriate writable GitHub remote with upstream tracking.

- [ ] **Step 5: Open a ready pull request**

Target the upstream default branch. Include the behavior, compatibility, user impact, and exact validation commands in the PR body.
