<p align="center">
  <img src="docs/design/logo/agentnook.svg" alt="agentnook" width="160">
</p>

# agentnook

A nook of its own for your coding agent. `nook` runs **Pi**, **OpenCode v2**, or **Claude Code** in a short-lived [smolvm](https://github.com/smol-machines/smolvm) VM. Logins, settings, skills, and extensions persist after the VM exits.

## Quick start

Requirements: macOS on Apple Silicon or Linux with KVM, and **smolvm ≥ 1.36.1** on your `PATH`.

```sh
curl -qfsS https://raw.githubusercontent.com/flobilosaurus/agentnook/main/install.sh | sh
```

Then run `nook` from your project directory.

## Forward environment variables

```sh
nook --env GITHUB_TOKEN --env OPENAI_API_KEY
```

`--env` replaces the saved list for this project directory and agent. `config.toml` stores names only, never values. On later launches, `nook` reuses the list and reads current values from the host. `nook --clear-env` clears the list for future launches.

Export the variables on the host before launching. If a variable is missing, launch fails. `--env` does not accept `NAME=VALUE` assignments. `--dry-run` shows names only and does not change configuration.

**Forward only what you intend to share:** The agent and its child processes can read and pass on these values on every launch.

## Images and tools

`nook images build` uses [mise](https://mise.jdx.dev/) to install extra tools into the image.

**`--tool NAME@VERSION`** specifies a tool directly. Repeat the flag to add more tools:

```sh
nook images build pi-dev --agent pi --tool rg@14.1.1 --tool fd@10.3.0
```

**`--mise-config PATH`** reads the `[tools]` section of a mise TOML file without running tasks or hooks:

```sh
nook images build pi-dev --agent pi --mise-config ./mise.toml
```

You can combine both flags. See built images and their tools in the [Images tab](#4-images).

## Customize agent settings, skills, and extensions

A **profile**, such as `work` or `private`, stores logins, settings, skills, and extensions separately for each agent. This data survives VM shutdown and is shared across projects that use the same profile and agent.

On the host, agent data lives at `~/.agentnook/profiles/<profile>/<agent>/`. If `AGENTNOOK_HOME` is set, `profiles/` lives there instead. Inside the VM, the agent's directory is its home directory (`~`).

**Settings and skills:** If you change settings or add a skill in a Pi VM using the `work` profile, other Pi sessions with `work` use the same files. In running sessions, `/reload` picks up changes; new Pi sessions read them automatically. Some settings, such as the default thinking level, apply only at startup. Other profiles are unaffected.

| Agent | Settings file in the VM | Skills directory in the VM |
| --- | --- | --- |
| Pi | `~/.pi/agent/settings.json` | `~/.pi/agent/skills/` |
| OpenCode v2 | `~/.config/opencode/opencode.json` | `~/.config/opencode/skills/` |
| Claude Code | `~/.claude/settings.json` | `~/.claude/skills/` |

**Install extensions:** Replace the placeholders with package or plugin names:

```sh
nook --agent pi --profile work -- install npm:PACKAGE </dev/null
nook --agent opencode --profile work -- plugin add PACKAGE
nook --agent claude --profile work -- plugin install PLUGIN@MARKETPLACE --scope user
```

Inside the VM, put local Pi extensions in `~/.pi/agent/extensions/` and OpenCode plugins in `~/.config/opencode/plugins/`. Then restart the agent.

## `nook watch`

Open `nook watch` in a separate terminal. Watch covers all projects in the current `AGENTNOOK_HOME` without starting or stopping VMs. At 80 columns or wider, the list and details appear side by side.

- `1`–`4`, `Tab`, or `Shift+Tab`: switch tabs.
- `↑`/`↓` or `j`/`k`: select an entry.
- `q`: close Watch. Running VMs stay active.

### 1 VMs

Running agent VMs, grouped by profile. Details show VM status and agent activity separately, along with launch limits, mounts, and the names of variables forwarded at launch, never their values. Press `Enter` to open the audit for the selected VM.

![Watch VMs tab: four sample VMs in two profiles, with the VM list on the left and details on the right](docs/screenshots/watch.png)

### 2 Approvals

Pending TCP requests in `--network ask` mode and recent decisions. Select a request and press `a` to allow it once, `s` to allow that exact destination IP and port for the current VM run, or `d` to deny it. Unanswered requests are denied after 12 seconds. `nook network` opens this tab directly.

![Watch Approvals tab: two pending TCP requests and details for the selected request](docs/screenshots/network.png)

### 3 Audit

Read-only network history, including VMs that have stopped. `ask` records both allowed and blocked decisions; `full` records only blocked ones. Use `PgUp`/`PgDn` to page through history, `f` to toggle between all VMs and running VMs only, and `x` to clear a single-VM filter.

`nook network --machine NAME` opens the audit for that VM. `nook network --json` outputs the data without opening the TUI.

![Watch Audit tab: allowed and blocked decisions for running and stopped VMs](docs/screenshots/network-history.png)

### 4 Images

Managed images, installed tools, and saved project assignments. Assignment details show configured environment variable names, never values. Press `Enter` to expand or collapse an image's assignments and `r` to refresh. `nook images status` opens this tab in a terminal and prints plain text otherwise.

![Watch Images tab: a Pi image with mise tools and expanded project assignments](docs/screenshots/images-status.png)

## Herdr

When you run `nook` interactively in a [Herdr](https://herdr.dev/) pane, it reports the status of Pi, OpenCode v2, or Claude Code to that pane. You can see which agent is working (`working`), ready (`idle`), or waiting for input (`blocked`) without switching tabs.

Herdr can notify you when an agent in a background tab needs attention (changes to `blocked`) or finishes (changes from `working` or `blocked` to `idle`).
