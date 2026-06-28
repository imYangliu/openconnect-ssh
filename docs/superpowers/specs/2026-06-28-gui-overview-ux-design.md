# GUI Overview UX Design

## Scope

Only improve the SwiftUI GUI. Do not change the Rust TUI or CLI behavior.

## Current Context

OCH is a macOS-first VPN and SSH configuration tool. The GUI already uses a sidebar with task panes and a raw TOML editor, so this change must preserve the split settings model and the TOML/settings dual editing flow.

## Options Considered

1. Visual polish only: adjust spacing, colors, and card styling. This is low risk, but it does not help users understand what needs attention.
2. Overview-first status hierarchy: make the Overview pane summarize readiness, show the next actionable issues, and keep detailed controls in existing panes. This is the recommended slice because it improves first-run and daily use without changing navigation.
3. Navigation restructure: move to a native `NavigationSplitView` or settings scene. This may be worth doing later, but it is too broad for the first verified GUI task.

## Selected Design

Use option 2. The Overview pane becomes a clear command surface:

- A readiness panel at the top states whether OCH is ready or needs attention.
- The readiness panel lists the concrete issues that affect setup: missing VPN config, service needing attention, missing SSH Include, and unsaved TOML edits.
- Status cards remain visible, but their presentation becomes more scannable and less cramped.
- Primary actions stay limited to Connect, Disconnect, and Refresh All on Overview.
- Existing detailed panes remain the place to fix VPN, SSH, routes, service, advanced, TOML, and logs.

## UX Rules

- Preserve native SwiftUI controls and SF Symbols.
- Keep the current sidebar + pane model.
- Do not introduce decorative animation or custom imagery.
- Use color with text and symbols, not color alone.
- Keep status decisions testable outside SwiftUI where practical.

## Verification

- Add a Swift smoke test for the overview readiness issue calculation.
- Run the smoke test once before implementation and confirm it fails for the missing API.
- Run `make check-swift-parsing` after implementation.
- Run `make check` before declaring the GUI task complete.
