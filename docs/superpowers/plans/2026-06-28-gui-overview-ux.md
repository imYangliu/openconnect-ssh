# GUI Overview UX Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Improve the SwiftUI GUI Overview pane so users can immediately see whether OCH is ready and what needs attention.

**Architecture:** Keep the existing `ContentView` sidebar/pane layout. Add a small testable readiness issue model in `AppConfig.swift`, expose it through `AppModel`, and render it in `ContentView`.

**Tech Stack:** SwiftPM, SwiftUI, SF Symbols, existing Swift smoke tests.

## Global Constraints

- Only change GUI-facing SwiftUI and supporting Swift model code.
- Do not change Rust TUI or CLI behavior.
- Preserve TOML/settings dual editing.
- Use native SwiftUI controls and SF Symbols.
- Verify with `make check-swift-parsing` and `make check`.

---

### Task 1: Add Testable Overview Readiness State

**Files:**
- Modify: `tests/app-config-smoke.swift`
- Modify: `Sources/OCHApp/AppConfig.swift`

**Interfaces:**
- Produces: `OverviewIssue.issues(for:includeInstalled:serviceAvailable:hasUnsavedConfigTextChanges:) -> [OverviewIssue]`

- [ ] **Step 1: Write the failing smoke test**

Add assertions that an incomplete setup reports missing VPN config, service attention, missing SSH Include, and unsaved TOML edits. Add assertions that a complete setup reports no issues.

- [ ] **Step 2: Run test to verify it fails**

Run: `make check-swift-parsing`

Expected: compile failure because `OverviewIssue` does not exist.

- [ ] **Step 3: Implement the minimal readiness model**

Add `OverviewIssue` to `AppConfig.swift` with four cases and the `issues` helper.

- [ ] **Step 4: Run test to verify it passes**

Run: `make check-swift-parsing`

Expected: `app config smoke passed`.

### Task 2: Render the Overview Readiness Panel

**Files:**
- Modify: `Sources/OCHApp/AppModel.swift`
- Modify: `Sources/OCHApp/ContentView.swift`
- Modify: `Sources/OCHApp/UILayout.swift`
- Modify: `Sources/OCHApp/Resources/en.lproj/Localizable.strings`
- Modify: `Sources/OCHApp/Resources/zh-Hans.lproj/Localizable.strings`
- Modify: `Sources/OCHApp/Resources/zh-Hant.lproj/Localizable.strings`

**Interfaces:**
- Consumes: `OverviewIssue.issues(...)`
- Produces: `AppModel.overviewIssues`

- [ ] **Step 1: Add localized copy**

Add readiness title/body strings and issue title/detail strings in all three localization files.

- [ ] **Step 2: Expose issues from the model**

Add `overviewIssues` to `AppModel`.

- [ ] **Step 3: Add a readiness panel view**

In `ContentView.swift`, place the panel above the status cards in `overviewPane`.

- [ ] **Step 4: Improve status card scanability**

Keep the existing four cards, but use adaptive columns, clearer icon treatment, and stable card sizing.

- [ ] **Step 5: Run full verification**

Run: `make check`

Expected: shell, Rust, Swift parsing, Swift build, and smoke tests pass.
