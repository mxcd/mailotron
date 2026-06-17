# mailotron

A non-interactive email CLI built to be driven by AI agents. Compose responsive
HTML + plain-text emails from **Markdown** and **MJML** templates, send them via
**SMTP** or **Resend**, and manage **IMAP** mailboxes — every feature is a
command with flags, and every command speaks JSON.

- **Write Markdown, not HTML.** Bodies are Markdown by default; frames and
  signatures are authored in MJML and compiled to responsive, email-safe HTML
  with the native Go [`gomjml`](https://github.com/preslavrachev/gomjml) library
  (no Node.js).
- **Agent-first.** `-o json` on any command, deterministic exit codes, no
  prompts, and a baked-in operating manual: `mailotron guide`.
- **Multi-account.** Configure any number of mailboxes in
  `~/.mailotron/config.yml`; pick one with `-a/--account`.
- **Full IMAP management.** List/create/rename/delete folders; list, read,
  move, copy, flag, delete and download messages and attachments.
- **Mailbox backup & restore.** Pull an entire mailbox into a directory of
  individual files (one `.eml` per message, no archive) and append it back.
  Incremental and restic-friendly, so an external backup tool can ship it to S3.

## Install

```sh
go install github.com/mxcd/mailotron/cmd/mailotron@latest
```

Or grab a binary from the [releases](https://github.com/mxcd/mailotron/releases)
(Linux, macOS, Windows; amd64 + arm64).

## Quick start

```sh
mailotron config init          # writes ~/.mailotron/config.yml + seeds default templates/signatures
# edit ~/.mailotron/config.yml and export the referenced secrets, then:
export SMTP_PASSWORD=...  IMAP_PASSWORD=...
mailotron config validate
mailotron account test -a personal

# send a Markdown email through a template
mailotron send -a personal \
  --to "Jane <jane@example.com>" --subject "Hello" \
  --template default --signature default \
  --var Name=Jane \
  --body $'# Hi Jane\n\nThis is **Markdown**, rendered into a responsive template.'

# triage an inbox
mailotron message list -a personal --unseen -o json
mailotron message show 4213 -a personal -o json
mailotron message move 4213 --to-folder Archive -a personal
```

## For agents

Run `mailotron guide` for the full operating manual (mental model, JSON output
contract, exit codes, every command with examples). `mailotron guide -o json`
adds a machine-readable command catalog generated from the live command tree.

## Configuration

`~/.mailotron/config.yml` holds any number of accounts. Secrets are referenced
as `${ENV_VAR}` and resolved from the environment at load time — they are never
written to disk by mailotron.

```yaml
defaults:
  account: personal
  template: default
  signature: default

accounts:
  personal:
    from: "Your Name <you@example.com>"
    outbound: { type: smtp, host: smtp.example.com, port: 587, tls: starttls,
                username: you@example.com, password: ${SMTP_PASSWORD} }
    imap:     { host: imap.example.com, port: 993, tls: tls,
                username: you@example.com, password: ${IMAP_PASSWORD} }

  newsletter:                       # send-only via Resend (no imap block)
    from: "News <news@example.com>"
    outbound: { type: resend, api_key: ${RESEND_API_KEY} }
```

Override the location with `--config <path>` or `MAILOTRON_CONFIG` /
`MAILOTRON_CONFIG_DIR`.

## Templates & signatures

Templates are MJML "frames" with `{{.Body}}`/`{{.Signature}}` slots plus
arbitrary `{{.Var}}` variables; signatures are MJML snippets. Both live under
`~/.mailotron/` and are managed with `mailotron template …` and
`mailotron signature …`. Discover a template's required variables with
`mailotron template show <name> --vars`.

The body is Markdown by default; use `--body-format mjml|text|html` for other
inputs.

## Mailbox backup & restore

`mailotron backup` mirrors a whole mailbox into a directory of plain files — one
`.eml` per message (verbatim RFC822) plus a JSON index per folder and a
top-level `manifest.json`. There is no zip: the directory is the contract with a
content-addressed backup tool such as [restic](https://restic.net), which
mailotron intentionally does **not** wrap. Because every message lands at a
stable path with identical bytes across runs, restic deduplicates it and each
incremental snapshot is tiny.

```sh
# Pull the mailbox (incremental — only new messages download):
mailotron backup --out ./backup -a work

# Ship it to S3 with restic (run separately; mailotron does not call restic):
export RESTIC_PASSWORD=…  AWS_ACCESS_KEY_ID=…  AWS_SECRET_ACCESS_KEY=…
restic -r s3:s3.amazonaws.com/your-bucket/mailbox backup ./backup

# Recover, into a namespace so live folders are untouched (idempotent):
mailotron restore --in ./backup --prefix "Restored/" -a work
```

Backups are **additive** by default (messages deleted on the server are kept);
add `--mirror` to prune them so each restic snapshot is an exact point-in-time
copy. Restore matches messages by `Message-ID` and skips ones already present,
so it is safe to re-run.

## Development

```sh
just test          # unit tests (race)
just e2e           # end-to-end tests (Docker: GreenMail + Mailpit)
just build         # ./bin/mailotron
just snapshot      # local cross-platform GoReleaser build
```

CI runs `vet`, unit tests, and the e2e suite on every push/PR. Pushing a `v*`
tag builds and publishes cross-platform binaries via GoReleaser.

## License

MIT
