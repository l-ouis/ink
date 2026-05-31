# ink

A single-binary personal website. Content is Markdown files on disk, each served
at a path you choose; you edit everything from the browser. No database — the
binary, a content directory, and a small config file are all there is.

## Features

- **Pages at any path.** A page lives at whatever URL you give it — `/about`,
  `/projects/ink`, `/notes/2026/trip`. The home page (`/`) is just an editable
  page too.
- **Web editor** with live Markdown preview, drafts, and Post/Redirect/Get
  saving (refresh-safe).
- **Images.** Upload via a button, paste, or drag-and-drop; resize by dragging a
  corner in the preview; browse/delete everything in an admin gallery.
- **Syntax highlighting** for fenced code blocks (server-side, no JavaScript).
- **Choosable serif font** — 10 self-hosted typefaces, picked in the admin panel.
- **Custom header title and favicon**, set from the admin panel.
- **Single-owner auth**: HMAC-signed session cookies, CSRF protection, login
  throttling. No accounts, no sessions table.

## Build & run

Requires Go 1.25+.

```sh
go build -o ink .
./ink passwd            # set the owner password (needed to sign in)
./ink serve             # listen on :8080, content in ./content
```

Open <http://localhost:8080>; sign in at `/admin/login`.

### Commands & flags

```
ink serve [-addr :8080] [-content content] [-config data/config.json]
ink passwd [-config data/config.json]
```

`passwd` reads the new password from the terminal (or from stdin when piped:
`printf 'secret\n' | ./ink passwd`).

## How content is stored

```
content/
  pages/
    index.md            ->  /            (the home page)
    about.md            ->  /about
    projects/ink.md     ->  /projects/ink
  uploads/              ->  /uploads/...  (images, served from disk at runtime)
data/
  config.json           site settings, password hash, session secret
  favicon.png           uploaded favicon (if any)
```

Each page is Markdown with YAML front matter:

```markdown
---
title: About
date: 2026-05-30
draft: false
summary: A short description (used for the <meta> description and previews).
---

Body in **Markdown**, with GitHub extensions and ```code``` highlighting.
```

Set `draft: true` to hide a page from visitors (still visible to you when
signed in). You can edit files on disk directly or from the web — they're the
same files.

## Deploying

ink speaks plain HTTP and expects a TLS-terminating reverse proxy in front
(Caddy, nginx, …). Behind HTTPS, set `"secure_cookies": true` in
`data/config.json` so the session cookie is only sent over TLS.

Example with Caddy:

```
example.com {
    reverse_proxy localhost:8080
}
```

Example systemd unit:

```ini
[Service]
ExecStart=/srv/ink/ink serve -addr 127.0.0.1:8080
WorkingDirectory=/srv/ink
Restart=always

[Install]
WantedBy=multi-user.target
```

### Backups

Everything that matters is on disk:

- **`content/`** — all pages *and* uploaded images. Back this up.
- **`data/`** — config, the password hash, the session secret, and the favicon.
  Losing the session secret signs everyone out; losing the password hash means
  re-running `ink passwd`.

A periodic copy (or a git repo, or a snapshotted volume) of those two
directories is a complete backup.

## Customising

- **Fonts.** Admin → Settings → *Site font*. The 10 options are self-hosted in
  `static/fonts/` (latin subset). To change the set, edit `internal/config`'s
  `Fonts` list and add the corresponding `@font-face` rules to
  `static/fonts.css`.
- **Syntax theme.** Change `syntaxStyle` in `internal/render/markdown.go`, then
  regenerate `static/syntax.css` (see the note at the top of that file).
- **Header title & favicon.** Admin → Settings.

## Development

```sh
go test ./...        # unit tests
go test -race ./...
go vet ./...
```

Tests cover the security-critical pieces: slug/path-traversal validation,
image upload validation, and session/CSRF handling.
