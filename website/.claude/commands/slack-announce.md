---
name: "Slack: Announce"
description: Draft and post a Slack announcement to #proj-exohub (with preview and confirmation).
category: Communication
tags: [slack, announce, release]
---
**Purpose**

Draft a Slack announcement message, preview it for the user, and post it after explicit confirmation.

**Arguments**

- Optional: channel name (default: `#proj-exohub`)
- Optional: topic hint (e.g., "exo CLI v0.83.0 release", "new user guide section")

**Guardrails**
- NEVER post without showing the full message and getting explicit user confirmation first.
- NEVER hardcode the bot token. Always read `SLACK_BOT_TOKEN` from the `.env` file in the repo root.
- If `.env` does not exist or `SLACK_BOT_TOKEN` is not set, stop and tell the user to add it.

**Steps**

1. **Check token availability.**
   - Read `.env` from the repo root and verify `SLACK_BOT_TOKEN` is present.
   - If missing, tell the user: "Add `SLACK_BOT_TOKEN=xoxb-...` to `.env` in the repo root."

2. **Determine what to announce.**
   - If the user provided a topic hint in the argument, use that as context.
   - Otherwise, ask the user what they'd like to announce.
   - Look at recent work (git log, modified files, version bumps) for context if helpful.

3. **Draft the message.**
   - Use Slack mrkdwn formatting: `*bold*` for emphasis, `• ` for bullets, backticks for code, emoji sparingly.
   - Keep it concise — a title line and 2-5 bullet points is ideal.
   - Do NOT use markdown headers (`#`, `##`) — Slack doesn't render them.

4. **Preview and confirm.**
   - Show the full message to the user exactly as it will appear.
   - Use `AskUserQuestion` with options: "Post it", "Edit first" (let user provide changes), "Cancel".
   - If the user picks "Edit first", incorporate their feedback and preview again.
   - Only proceed to posting when the user explicitly confirms.

5. **Post the message.**
   - Read the token from `.env` using `source .env`.
   - Determine the channel: use the argument if provided, otherwise `#proj-exohub`.
   - POST to `https://slack.com/api/chat.postMessage` via curl:
     ```bash
     source .env && curl -s -X POST https://slack.com/api/chat.postMessage \
       -H "Authorization: Bearer $SLACK_BOT_TOKEN" \
       -H "Content-Type: application/json; charset=utf-8" \
       -d '{"channel":"<channel>","text":"<message>"}'
     ```
   - Parse the JSON response.

6. **Report the result.**
   - On success (`"ok": true`): tell the user the message was posted and show the channel.
   - On `"not_in_channel"` error: tell the user to invite the bot to the channel with `/invite @bot-name`.
   - On `"channel_not_found"`: suggest checking the channel name or using the channel ID (starts with `C`).
   - On `"invalid_auth"` or `"token_revoked"`: tell the user to check their `SLACK_BOT_TOKEN` in `.env`.
   - On any other error: show the raw Slack API error.
