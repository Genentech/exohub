Add a new VHS demo clip to the website carousel.

The user provides:
1. A name for the clip (e.g. "clone", "safe")
2. The commands to demonstrate
3. A title for each step

You create these files in `scripts/vhs/`:

## clip-<name>.tape

Copy the boilerplate from an existing tape (e.g. `clip-safe.tape`). Keep the same theme, font, PS1 setup, and Hide block. Change `Output` to `static/images/demo/clip-<name>.mp4`.

Structure the commands into sections separated by `Type ""` + `Enter`. Each section = one title.

Set `Sleep` values by benchmarking each command with `time`. Add 1-2s buffer.

Use `Set TypingSpeed 15ms` for long commands (URLs), `120ms` for normal ones.

## clip-<name>.titles

One title per section, one per line. Empty first line if the tape starts with `Type ""` + `Enter` before the first command. Timings are auto-computed by `compute-titles.py` — no timestamps needed.

## clip-<name>.pre.sh (if needed)

Setup script: check prerequisites, clean previous state. Use `chmod -R u+w` before `rm -rf` for git-annex dirs.

## clip-<name>.post.sh (if needed)

Cleanup/restore script (e.g. `exo safe restore`).

## hugo.toml

Add a `[[params.DemoCarousel]]` entry with `src`, `label`, and `caption`.

## Timing

Before recording, benchmark every command in the tape:

```bash
cd /working/dir
time command-one 2>&1
time command-two 2>&1
```

Set each `Sleep` to the measured time + 1-2s buffer. If a recording looks off (command output appears after the next command starts, or too much dead time), re-benchmark and adjust the Sleep values. The title timings update automatically via `compute-titles.py` — no manual timestamp editing needed.

## Record

```bash
bash scripts/vhs/record.sh scripts/vhs/clip-<name>.tape
```

## Re-timing an existing tape

When the user asks to re-time or fix timing on an existing clip:

1. Read the tape to find all commands and their current `Sleep` values
2. Run each command with `time` in the appropriate working directory (check the tape's `cd` in the Hide block, and run the pre-flight script first if one exists)
3. Update each `Sleep` to measured time + 1-2s buffer
4. Title timings update automatically — no changes needed to `.titles`
5. Re-record: `bash scripts/vhs/record.sh scripts/vhs/clip-<name>.tape`

## Key constraints

- Shell must be `bash` (zsh rejects `#` comments)
- Never use `Wait` or `Wait+Screen` — always `Sleep`
- PS1 must use `\[...\]` around escape codes (line wrapping breaks otherwise)
- VHS Hide/Show loses ANSI colors — don't try to print colored text while hidden
- Titles use `textfile=` in ffmpeg (supports apostrophes)
