# mailotron — Agent Operating Guide

`mailotron` is a non-interactive CLI for composing, sending, and managing email.
It is built to be driven by an AI agent: every feature is a command with flags,
there is no interactive UI, and every command can emit JSON.

If you are an agent seeing this tool for the first time, read this whole guide
once. It tells you the mental model, the machine-readable output contract, and
every command with copy-pasteable examples.

## Mental model

- **Account** — a configured connection in `~/.mailotron/config.yml`. Each
  account has an identity (`from`), an outbound transport (`smtp` or `resend`),
  and an optional inbound `imap` mailbox. Multiple accounts may be configured;
  select one with `--account <name>`, otherwise `defaults.account` is used.
- **Template** — an MJML "frame" (the outer shell of the email) stored under
  `~/.mailotron/templates/<name>.mjml`. You never write raw HTML; templates are
  MJML and are compiled to responsive HTML.
- **Signature** — an MJML snippet stored under
  `~/.mailotron/signatures/<name>.mjml`, injected into the template's
  `{{.Signature}}` slot. Its plain-text form is derived automatically.
- **Body** — the message content *you* supply. Default format is **Markdown**.
  It is converted to HTML and injected into the template's `{{.Body}}` slot.
- **Rendering** — `body → MJML fragment → injected into frame (text/template) →
  compiled to HTML by gomjml → reduced to a plain-text alternative`. Sending a
  message produces a `multipart/alternative` email (HTML + text).

Template/signature variables use Go `text/template` syntax: `{{.Name}}`. The
renderer always provides `Subject`, `PreviewText`, `Year`, `Date`, `Body`, and
`Signature`. Any other variable must be supplied with `--var key=value`. Use
`template show <name> --vars` to discover what a template needs.

## Output contract (IMPORTANT for agents)

- Add `-o json` (or `--output json`) to **any** command for machine-readable
  output on **stdout**. Human errors and logs go to **stderr**.
- Exit codes: `0` success, `1` runtime error (send failed, not found, IMAP
  error), `2` usage error (bad flags/arguments).
- In JSON mode, success prints a single JSON object/array to stdout and nothing
  to stderr. On failure, stdout is empty and stderr contains
  `{"error":"...message..."}` with a non-zero exit.
- Commands never prompt. There is no interactive mode.

## Global flags

| Flag | Meaning |
|------|---------|
| `--config <path>` | Use a specific config file (default `~/.mailotron/config.yml`). |
| `-o, --output text\|json` | Output format. Default `text`. |
| `-a, --account <name>` | Account to use. Default `defaults.account`. |
| `--verbose` | Extra diagnostics on stderr. |

## Environment

- Config secrets are referenced as `${ENV_VAR}` in the YAML and resolved from
  the environment at load time. Set e.g. `SMTP_PASSWORD`, `IMAP_PASSWORD`,
  `RESEND_API_KEY` before running.
- `MAILOTRON_CONFIG` overrides the config file path; `MAILOTRON_CONFIG_DIR`
  overrides the whole config/store directory.

## First-run setup

```sh
mailotron config init            # writes ~/.mailotron/config.yml + seeds default templates/signatures
mailotron config validate        # checks the config and that referenced env vars are set
mailotron account list -o json   # see configured accounts
mailotron account test -a work   # check outbound + IMAP connectivity for an account
```

## Composing and sending

`render` composes and outputs without sending (use it to preview/inspect).
`send` composes and delivers.

```sh
# Render to JSON (html, text, mjml) from a Markdown body on stdin:
echo "# Hi\n\nThis is **markdown**." | \
  mailotron render --template default --signature default \
    --subject "Hello" --var Name=Max --body-file - -o json

# Send, body from a file, with variables and an attachment:
mailotron send -a work \
  --to "Jane <jane@example.com>" --cc ops@example.com \
  --subject "Q2 report" \
  --template default --signature default \
  --var Name=Jane --var CompanyName="Wilde IT" \
  --attach ./report.pdf \
  --body-file ./body.md

# Body inline, plain-text body format, dry run (render but do not send):
mailotron send --to a@b.com --subject "Ping" --body "just text" \
  --body-format text --dry-run -o json
```

Key `render`/`send` flags: `--template`, `--signature`, `--body`, `--body-file`
(`-` = stdin), `--body-format markdown|mjml|text|html`, `--subject`,
`--preview`, `--align left|center` (left = normal flush-left mail; default left),
`--var key=value` (repeatable), `--vars-file <json>`. `send` adds
`--to`/`--cc`/`--bcc`/`--reply-to` (repeatable, RFC 5322 addresses),
`--attach <path>` (repeatable), `--embed cid=path` (inline image referenced as
`cid:<cid>` from a template/signature, repeatable), `--dry-run`.

## Templates & signatures

```sh
mailotron template list -o json
mailotron template show default --vars -o json     # includes required user variables
mailotron template add promo --file ./promo.mjml   # add/overwrite (also accepts stdin via -)
mailotron template rm promo
mailotron signature list -o json
mailotron signature add work --file ./work.mjml
```

## IMAP: folders (directories)

```sh
mailotron folder list -a work -o json
mailotron folder create "Clients/Acme" -a work
mailotron folder rename "Clients/Acme" "Clients/AcmeCorp" -a work
mailotron folder delete "Clients/Old" -a work
```

## IMAP: messages

Messages are addressed by `--folder <name>` (default `INBOX`) plus their UID.

```sh
# List, with server-side filters:
mailotron message list -a work --folder INBOX --unseen --limit 20 -o json
mailotron message list -a work --since 2026-06-01 --from boss@corp.com -o json

# Show one message (headers + body); --no-body for headers only:
mailotron message show 4213 --folder INBOX -a work -o json

# Organize:
mailotron message move 4213 --to-folder "Clients/Acme" -a work
mailotron message copy 4213 --to-folder Archive -a work
mailotron message flag 4213 --seen -a work          # also: --unseen --flagged --unflagged
mailotron message delete 4213 -a work               # moves to Trash; --hard expunges
mailotron message download 4213 --out ./dl --attachments-only -a work

# Search (server-side IMAP SEARCH):
mailotron message search --folder INBOX --text "invoice" --since 2026-01-01 -a work -o json
```

A `message list`/`show` JSON object includes `uid`, `uidValidity`, `folder`,
`from`, `to`, `subject`, `date`, `flags`, `size`, and (for `show`) `text`,
`html`, and `attachments`. Use `uid` + `folder` for follow-up operations. If a
folder's `uidValidity` changes between calls, re-list before acting.

## Backup & restore (whole mailbox)

`backup` pulls an entire mailbox into a directory of plain files — one `.eml`
per message (verbatim RFC822, fetched with PEEK so flags are untouched) plus a
JSON index per folder and a top-level `manifest.json`. `restore` appends those
messages back into IMAP. No archive/zip: the directory is meant to be handed to
a content-addressed backup tool (restic, borg, `aws s3 sync`) which mailotron
deliberately does **not** wrap — the directory is the contract between them.

```sh
# Pull the whole mailbox to ./backup (incremental: only new messages download):
mailotron backup --out ./backup -a work -o json

# Limit to specific folders:
mailotron backup --out ./backup --folder INBOX --folder "Clients/Acme" -a work

# Make the directory exactly mirror the live mailbox (prune deletions):
mailotron backup --out ./backup --mirror -a work

# Then archive to S3 with restic (mailotron does not do this for you):
export RESTIC_PASSWORD=… AWS_ACCESS_KEY_ID=… AWS_SECRET_ACCESS_KEY=…
restic -r s3:s3.amazonaws.com/your-bucket/mailbox backup ./backup

# Restore everything back into IMAP (idempotent — skips messages already there):
mailotron restore --in ./backup -a work -o json

# Restore into a namespace instead of clobbering live folders, or preview first:
mailotron restore --in ./backup --prefix "Restored/" -a work
mailotron restore --in ./backup --dry-run -a work -o json
```

On-disk layout (stable paths → restic dedups; incremental snapshots are small):

```
backup/
  manifest.json                 # account, server, time, folder list, counts
  folders/
    INBOX/
      index.json                # uidvalidity, per-message {uid, messageId, flags, internalDate, size}
      4213.eml                  # raw RFC822 bytes, named by IMAP UID
    Clients%2FAcme/             # folder names percent-encoded to one path segment
      …
```

Behavior worth knowing:

- **Incremental.** Message bodies already on disk are never re-downloaded; only
  flags/index are refreshed. Cheap to run repeatedly (e.g. before each restic run).
- **Additive by default.** Messages deleted on the server stay in the backup.
  Pass `--mirror` to prune them so each restic snapshot is an exact point-in-time
  mirror. Pruning a vanished *folder* only happens on a full mirror (no `--folder`
  filter); a scoped `--folder … --mirror` run leaves out-of-scope folders alone.
- **UIDVALIDITY reset.** If a folder's `uidValidity` changes, the local folder is
  re-fetched from scratch (old UIDs no longer identify the same messages). Prior
  restic snapshots still hold the old state.
- **Restore is idempotent.** Messages are matched by `Message-ID`; ones already
  present in the target folder are skipped, so re-running never duplicates. APPEND
  preserves flags and the original internal date (the server-managed `\Recent`
  flag is dropped). Messages without a `Message-ID` cannot be deduplicated and
  will be re-appended on a second run.

## Common agent workflows

1. **Send a templated email**: `config validate` → pick `--account` →
   `template show <t> --vars` to learn required vars → `send` with `--var`s and
   `--body-file -`.
2. **Triage an inbox**: `message list --unseen -o json` → for each, `message
   show <uid> --no-body` → `message move` / `message flag` / `message delete`.
3. **Archive a thread**: `folder create Archive/2026` → `message move <uid>
   --to-folder Archive/2026`.
4. **Back up a mailbox to S3**: `backup --out ./backup -a work` → hand `./backup`
   to `restic backup`. Re-run both on a schedule; both are incremental.
5. **Recover a mailbox**: `restore --in ./backup --prefix "Restored/" -a work`
   (use `--dry-run` first to preview).

## Gotchas

- Templates and signatures are MJML, never HTML. The body is Markdown by
  default; pass `--body-format mjml` only when you need block-level MJML layout
  (sections/columns) in the body itself.
- gomjml does not support `<mj-include>`; compose with variables instead.
- `message delete` is reversible by default (move to Trash). `--hard` is not.
- Always prefer `-o json` when parsing output programmatically.
