# Artifacts: pages the agent makes

**Date:** 2026-09-22
**Author:** Kyle (corporealshift), with Claude
**Status:** Proposed in the PR for issue 41

## 1. Purpose

Claude can make a page to show something: a chart of what it measured, a table, a diagram,
a small interactive tool. Issue 41 asks for the same in nabu.

## 2. Decision

**A module tool, `artifact {name, title, html}`, whose page lives in the log.**

- The page is the argument of the tool call, so the call event *is* the artifact. Every
  client already has it, it replays with the session, and requests are unchanged
  (invariant 3). Writing the same name again is a new version; clients show the latest.
- One self-contained HTML document, up to 256 KB. It stays in the conversation, so the
  model pays for it on every later turn, and the tool says to keep it small.
- **TUI:** the transcript says a page was made; `o` opens the newest, `/open <name>` a
  named one, in the system browser.
- **Android:** the transcript shows the page's title with an Open button, which opens it in
  a WebView.

## 3. The page is untrusted

The HTML is the model's, and the model reads text it did not write: a fetched web page can
steer it. Opened, the page is a program on the owner's machine or phone, near a daemon that
runs shell commands, and on loopback the daemon asks for no token.

- **No network.** Clients open every page under
  `default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data: blob:; …`.
  Scripts and styles in the page run; nothing can be loaded or connected to, and
  `default-src` covers `connect-src`, so fetch and WebSocket are refused.
- **The policy comes first.** It is placed straight after the doctype, before anything in
  the page: a policy in the page's own head would come after a script placed before it.
- **The phone doubles it:** the WebView refuses network loads and file and content access,
  and there is no JavaScript bridge.
- **The daemon refuses a WebSocket whose Origin is not its own**, including `null`, which
  is what a file or a sandboxed page sends. This was already the behaviour of the WebSocket
  library's default; it now has a test.

Checked in headless Edge against a local server that logs every request. The unwrapped
page's fetch, image and WebSocket all reached the server; wrapped, none did, and the page's
script still ran.

## 4. Rejected

- **Serve pages from the daemon over HTTP.** A second thing to authenticate and keep in
  step with the log. And a page served from the daemon's own origin is exactly the origin
  the WebSocket check lets in.
- **Files in the workspace.** They mix the agent's scratch output into the owner's
  repository, and they are invisible to the phone.
- **Markdown or SVG only.** Safer, and it can't sort a table, filter a chart, or answer a
  hover. Sandboxed HTML keeps interactivity and removes the network.
- **A persistent artifact gallery across sessions.** Worth having later; the log already
  holds everything needed to build one.
