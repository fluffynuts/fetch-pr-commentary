fetch-pr-commentary
---

Dumps all commentary on a GitHub pull request (description, conversation comments, review
summaries and inline review comments) to stdout as chronological Markdown, suitable for feeding
to an agent. Requires the [`gh`](https://cli.github.com) CLI, authenticated.

```sh
fetch-pr-commentary 123 > pr.md                      # inside a checkout with a GitHub remote
fetch-pr-commentary --repo org/repo --unresolved-only 123
fetch-pr-commentary                                  # pick org / repo / PR interactively
```

Interactive prompts render on stderr, so redirecting stdout still works.

## Install

**Linux and macOS:**

```sh
curl -fsSL https://raw.githubusercontent.com/fluffynuts/fetch-pr-commentary/master/install.sh | sh
```

**Windows** (PowerShell):

```powershell
irm https://raw.githubusercontent.com/fluffynuts/fetch-pr-commentary/master/install.ps1 | iex
```

Either one downloads the latest release for your machine, checks it against the release's
checksums, and runs `fetch-pr-commentary --install` from it. That puts the `fetch-pr-commentary` binary in `~/.local/bin`. On
Windows it adds `~\.local\bin` to your user `PATH` if it isn't there already (open a new terminal to
pick it up); on Linux and macOS, where that depends on your shell's profile, it tells you what to add
if the folder isn't on your `PATH`. `install.sh` needs `curl` or `wget`, and `unzip` (or `python3`).

Run the same command again to upgrade, or, once fetch-pr-commentary is installed:

```sh
fetch-pr-commentary --upgrade
```

You can also download the zip for your platform from the
[latest release](https://github.com/fluffynuts/fetch-pr-commentary/releases/latest) (`fetch-pr-commentary-linux-amd64.zip`,
`fetch-pr-commentary-macos-arm64.zip`, `fetch-pr-commentary-windows-amd64.zip` and so on, each for x86-64 and ARM, plus a
`SHA256SUMS`), unzip it anywhere, and run `fetch-pr-commentary --install` from the folder it makes.
