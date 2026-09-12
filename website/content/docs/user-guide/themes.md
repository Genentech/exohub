---
title: "Themes & Appearance"
description: "Customize terminal colors and dark/light mode"
weight: 11
---

The `exo` CLI supports color themes that apply across all commands, the Atlas TUI, and the web UI. Themes adapt automatically to dark and light terminal backgrounds.

## Choosing a Theme

Run the interactive theme picker:

```bash
exo theme
```

This shows each available theme with a color preview. Select one to apply it immediately.

### Available Themes

| Theme | Description |
|-------|-------------|
| **exohub** | ExoHub brand gradient — orange, pink, and purple (default) |
| **mono** | Black, white, and grayscale — useful for accessibility or low-color terminals |

### Setting a Theme Non-Interactively

```bash
# Set a theme by name
exo theme set mono

# List available themes
exo theme list

# Show the current theme and mode
exo theme show
```

## Dark and Light Mode

Exo auto-detects your terminal background and picks the right color variant. To override:

```bash
# Interactive mode picker
exo theme mode

# Set directly
exo theme mode dark
exo theme mode light
exo theme mode auto    # restore auto-detection
```

## How Preferences Are Stored

Theme choices are saved in a YAML preferences file:

- **Linux**: `~/.config/exo/preferences.yaml` (or `$XDG_CONFIG_HOME/exo/preferences.yaml`)
- **macOS**: `~/Library/Application Support/exo/preferences.yaml`

Example:

```yaml
theme: mono
theme_mode: dark
tips_enabled: false   # optional — set to false to disable daily tips
```

You don't need to edit this file manually — `exo theme` commands manage it for you.

## Environment Variable Overrides

Environment variables take precedence over the preferences file:

| Variable | Description | Values |
|----------|-------------|--------|
| `EXO_THEME` | Override the active theme | Theme name (e.g., `mono`) |
| `EXO_THEME_MODE` | Override dark/light mode | `auto`, `dark`, `light` |

```bash
# Temporarily use the mono theme for one command
EXO_THEME=mono exo info
```

Resolution order (highest priority first):
1. `EXO_THEME` / `EXO_THEME_MODE` environment variable
2. Preferences file (`preferences.yaml`)
3. Default (`exohub` theme, `auto` mode)

## Tips of the Day

The `exo` CLI shows a random tip from the ExoHub tip bank once per day — on your first command of the day. Tips appear briefly in the terminal and don't interrupt your workflow.

You can also fetch a tip on demand:

```bash
exo tip
```

### Opting Out

To disable daily tips, set `EXOHUB_TIPS=false` in your shell profile:

```bash
export EXOHUB_TIPS=false
```

Or set `tips_enabled: false` in your preferences file:

```yaml
tips_enabled: false
```

#### Disabling Tips in CI / Server Environments

For non-interactive environments (CI pipelines, containers, servers), use `EXOHUB_DISABLE_TIPS=1` instead:

```bash
export EXOHUB_DISABLE_TIPS=1
```

This variable is purely opt-in for disabling — only `1`, `true`, or `yes` disable tips; any other non-empty value is ignored and falls through to the normal `EXOHUB_TIPS` / preferences check.

#### Resolution Order

1. `EXOHUB_DISABLE_TIPS=1|true|yes` — disables tips (highest priority; intended for CI)
2. `EXOHUB_TIPS=false|0` — disables; any other non-empty value enables
3. `tips_enabled` in preferences file
4. Default: enabled

## Web UI

When using `exo atlas` in the web browser (via `exo serve`), the landing page toolbar includes:

- A **theme dropdown** to switch between themes
- A **mode toggle** to switch dark/light mode
- **Font size buttons** (S / M / L) to adjust the terminal font size

Theme and mode selections are passed to the terminal session, so the TUI matches the web UI appearance.
