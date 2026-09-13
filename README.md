# zklens

A terminal UI for exploring and administering Apache ZooKeeper ensembles.

zklens opens your ZooKeeper ensemble as an interactive, keyboard-driven
tree: browse znodes, inspect their data and metadata, edit them safely,
and watch changes happen live — without writing a single four-letter
command.

```
╭─ Tree — prod ────────────────╮╭─ Data — /app/conf ───────────────╮
│▾ /                           ││Raw:                              │
│  ▸ zookeeper                 ││{"replicas":3,"timeout":"30s"}    │
│  ▾ app                       ││                                  │
│    ▸ conf ◆                  ││Decoded (JSON):                   │
│    ▸ locks                   ││{                                 │
│  ▸ services                  │╰──────────────────────────────────╯
│                              │╭─ Stat ───────────────────────────╮
│                              ││version:        3                 │
╰──────────────────────────────╯╰──────────────────────────────────╯
tab load data • shift+tab switch panel • ? help • q quit
```

## Features

- **Tree explorer** — lazy-loaded znode tree with expand/collapse,
  markers for leaves, subtrees, errors and live watches.
- **Data & Stat panels** — payload rendered as text or pretty-printed
  JSON; anything with binary or terminal control bytes falls back to a
  byte-accurate hex dump, so data can never corrupt your terminal.
- **Safe editing** — create, edit and delete znodes from dialogs, with
  optimistic locking: if someone else changes a node while you edit it,
  your save is rejected instead of silently overwriting their write.
- **Live watches** — press `w` to follow a node's children or data; the
  UI updates the moment something changes, no polling.
- **Multiple contexts** — keep all your ensembles (local, staging,
  prod…) in one config file and pick one at startup.
- **Resilient connections** — reconnects across ensemble outages and
  slow-starting servers, with connection state always visible.

## Install

macOS and Linux (amd64/arm64), downloads the latest release binary:

```bash
curl -fsSL https://raw.githubusercontent.com/hugantdev/zklens/main/scripts/install.sh | bash
```

It installs to `~/.local/bin` by default; set `ZKLENS_INSTALL_DIR` to
change that, or `ZKLENS_VERSION` to pin a specific release instead of
the latest one.

Or build from source:

```bash
go install github.com/hugantdev/zklens/cmd/zklens@latest
```

Or clone and build:

```bash
git clone https://github.com/hugantdev/zklens.git
cd zklens
go build -o zklens ./cmd/zklens
```

## Quick start

With a ZooKeeper ensemble on `localhost:2181`, just run:

```bash
zklens
```

Point it at a remote ensemble with `--hosts`:

```bash
zklens --hosts zk1.example.com:2181,zk2.example.com:2181
```

Or with digest authentication:

```bash
zklens --hosts zk1.example.com:2181 --auth-digest admin:secretpassword
```

## Using the TUI

The screen is laid out in panels: the znode tree on the left, and the
selected node's **Data** above its **Stat** on the right, plus a
contextual help bar and a status bar along the bottom. Only the focused
panel responds to the arrow keys, so scrolling data never moves the tree
cursor.

| Key | Action |
|-----|--------|
| **↑ / ↓** or **j / k** | Move through the tree |
| **→ / Enter** | Expand the selected node (loads its children on demand) |
| **←** | Collapse, or jump to the parent if already collapsed |
| **Tab** (on the tree) | Load the node's data and Stat into the right panels |
| **Tab / Esc** (on a panel) | Return focus to the tree |
| **Shift+Tab** | Cycle the focus: Tree → Data → Stat |
| **?** | Expand or collapse the help bar |
| **q** or **Ctrl+C** | Quit |

The right-hand panels are populated on demand — browsing the tree never
reads data you didn't ask for. Loaded children stay cached, so collapsing
and re-expanding doesn't hit the ensemble again.

The tree rows carry markers: `▸` a collapsed subtree, `▾` an expanded
one, `·` a leaf, `!` a node that failed to list, and `◆` a live watch.

The layout adapts to small terminals: below ~42 columns the panels take
turns full width, and on short screens Data and Stat merge into one
scrolling panel.

### Creating, editing and deleting

| Key | Action |
|-----|--------|
| **n** | Create a child node (wizard: name → data → persistent / ephemeral / sequential) |
| **e** | Edit the node's data |
| **x** | Delete the node (asks for confirmation) |

Edits are version-checked: if another client changes the node while you
edit, saving fails with a version mismatch instead of overwriting their
change. The editor is single-line, so payloads it cannot preserve
(multiline data, invalid UTF-8, embedded control bytes, very large
values) are refused up front with Save disabled — your data is never
silently truncated. After a successful edit the Data and Stat panels
refresh automatically, and the tree reloads the affected branch while
keeping your place.

### Watching changes live

Press **w** to start or stop watching:

- **On the tree**, it watches the selected node's children: the branch
  refreshes itself when a child is added, removed or modified.
- **On the Data panel**, it watches the node's data: the panel updates
  as soon as the payload changes.

A watched node shows a `◆` marker. ZooKeeper watches fire once and stop,
but zklens re-arms them automatically, so monitoring continues until you
toggle the watch off, load another node, or the node is deleted.

## Configuration

Everything can also come from a JSON config file. zklens reads
`--config <path>`, or `$ZKLENS_CONFIG`, or — by default —
`~/.config/zklens/config.json` (`$XDG_CONFIG_HOME/zklens/config.json` if
`XDG_CONFIG_HOME` is set) when it exists.

### Contexts: multiple ensembles

Keep one entry per environment and pick at startup:

```json
{
  "contexts": [
    {"name": "local", "hosts": ["localhost:2181"]},
    {"name": "staging", "hosts": ["zk1.stg:2181", "zk2.stg:2181"], "sessionTimeout": "30s"},
    {"name": "prod", "hosts": ["zk1.prod:2181"], "auth": {"scheme": "digest", "credential": "admin:secret"}}
  ]
}
```

With more than one context, zklens shows a picker when it starts:
**Enter** connects, **Esc** cancels. To skip the picker, pass
`--context prod` or set `ZKLENS_CONTEXT=prod`; with a single context it
is used directly, and explicit `--hosts`/`--auth-digest` still bypass it.
Command-line flags and environment variables override individual fields
of the selected context.

A config file without `contexts` works too — the same `hosts`,
`sessionTimeout` and `auth` fields at the top level configure a single
connection.

### Flags and environment variables

| Flag | Environment | Description |
|------|-------------|-------------|
| `--hosts zk1:2181,zk2:2181` | `ZKLENS_HOSTS` | Ensemble members |
| `--session-timeout 30s` | `ZKLENS_SESSION_TIMEOUT` | Session timeout (default `10s`) |
| `--auth-digest user:pass` | `ZKLENS_AUTH_DIGEST` | Digest authentication |
| `--context prod` | `ZKLENS_CONTEXT` | Context from the config file |
| `--config <path>` | `ZKLENS_CONFIG` | Config file path |

Flags win over environment variables, which win over the config file.

### If the connection drops

zklens reconnects on its own whenever the ensemble hiccups, as long as
the session survives; the status bar at the bottom always shows the
connection state (`connected`, `reconnecting…`, `session expired`…), and
node-level errors (permission denied, node missing, non-empty delete)
appear inline and in the status bar. The tree stays where you left it
when the ensemble comes back.

## Development

`dev/docker-compose.yml` starts three single-node ZooKeeper ensembles on
ports 2181–2183 for trying the context picker against real clusters.

```bash
docker compose -f dev/docker-compose.yml up -d --wait
```

Tests, including Docker-backed integration tests (build tag
`integration`, skipped automatically without Docker):

```bash
go test ./...
go test -tags integration ./internal/zk/...
```
