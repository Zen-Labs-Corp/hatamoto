---
title: Follow a page request through the server
nav_title: How a page is served
description: Send requests to your own server with curl and watch each stage answer: headers, the log, redirects, missing pages and the admin.
next: request-flow.md, worker-and-background.md, ../operate/reverse-proxy-tls.md
---

In this guide you send a few requests to your own server and read what comes back. Each request shows one stage a page goes through, so at the end you can tell from a response which part of the server answered it and why.

Every request goes through the same stages, in this order:

1. Security headers, the request log and a request id are added.
2. The server picks a handler from a route table that is fixed at start-up.
3. For a public page, the page cache is asked first.
4. Redirect rules are checked before any content.
5. The address is matched to a published page, post or listing.
6. The theme's templates build the HTML, which is sent and, when it is safe, cached.

## Before you start

- A running Hatamoto site you can sign in to as an owner. To set one up, follow [the install guide](../getting-started/install-binary.md).
- A shell on the server, with `curl`, and the service's settings loaded so `DATABASE_URL` is set. [See how Hatamoto is put together](overview.md) shows how, in step 1.
- Access to the server log: `journalctl -u hatamoto-api` with systemd, `docker compose logs hatamoto` with Docker.

On Docker, put `docker compose exec hatamoto` in front of each `hatamoto` command; `curl` runs on the host, where the compose file publishes port 8090.

The examples call the server directly on `http://127.0.0.1:8090`, so your **reverse proxy** (the web server in front of Hatamoto that handles HTTPS) does not add or change headers.

## Steps

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

1. Request the home page and look at the headers only:

   ```bash
   curl -s -o /dev/null -D - http://127.0.0.1:8090/
   ```

   The output, shortened:

   ```text
   HTTP/1.1 200 OK
   Cache-Control: public, max-age=60, s-maxage=300
   Content-Security-Policy-Report-Only: default-src 'self'; ...
   Content-Type: text/html; charset=utf-8
   X-Content-Type-Options: nosniff
   X-Frame-Options: SAMEORIGIN
   X-Request-Id: bd8bc1433d109735deeea10c6db995ae
   ```

   No header says what built the page: the theme engine renders it and the response names no software. The security headers come from stage 1. The policy is report-only until you set `CSP_ENFORCE=true`, and `Strict-Transport-Security` is added only when `ENVIRONMENT=production`.

2. Send your own request id. The server keeps an `X-Request-ID` you send and makes one up when you do not:

   ```bash
   curl -s -o /dev/null -D - -H 'X-Request-ID: test-123' http://127.0.0.1:8090/ | grep -i request-id
   ```

   ```text
   X-Request-Id: test-123
   ```

   A reverse proxy can set this header, so one id follows a request through both servers.

3. Find the request in the log. Each request writes one line:

   ```bash
   journalctl -u hatamoto-api --no-pager | grep 'http request' | tail -1
   ```

   With `ENVIRONMENT=production`, as in [Deploy with systemd](../operate/deploy-systemd.md), the log is JSON, one object per line:

   ```text
   {"time":"2026-10-20T09:15:02.418Z","level":"INFO","msg":"http request","method":"GET","path":"/","remote_addr":"127.0.0.1:44012","status":200,"size":8231,"duration":2100000,"user_agent":"curl/8.22.0"}
   ```

   In any other environment the same line is plain text, such as `level=INFO msg="http request" method=GET path=/ status=200 size=8231 duration=2.1ms`. The line has the method, path, status, size and time taken (in JSON, `duration` is in nanoseconds). It does not include the request id.

4. Ask for an address that does not exist:

   ```bash
   curl -s -o /dev/null -D - http://127.0.0.1:8090/no-such-page | grep -E '^HTTP|Cache-Control'
   ```

   ```text
   HTTP/1.1 404 Not Found
   Cache-Control: private, no-cache, no-store
   ```

   The body is your theme's 404 page. The miss is written to the 404 log, which you can see in the admin at **Settings > Redirects**, `<admin>/settings/redirects/404`.

5. Ask for a missing address under `/api`:

   ```bash
   curl -s http://127.0.0.1:8090/api/nope
   ```

   ```text
   {"error":"not found"}
   ```

   Addresses under `/api` and `/media` are never pages, so the server answers with JSON instead of a themed page.

6. Add a redirect and follow it. Redirect rules are checked before content, so a rule wins over a page at the same address:

   ```bash
   hatamoto redirect create --from /old-page --to /
   curl -s -o /dev/null -D - http://127.0.0.1:8090/old-page | grep -E '^HTTP|Location'
   ```

   ```text
   HTTP/1.1 301 Moved Permanently
   Location: /
   ```

   Remove the test rule. Use the id that `hatamoto redirect list` prints in the first column:

   ```bash
   hatamoto redirect list
   hatamoto redirect delete <id>
   ```

7. Ask for the admin without signing in. Put your own address, printed by `hatamoto admin-url`, where the command says `<admin>`:

   ```bash
   curl -s -o /dev/null -D - -H 'Sec-Fetch-Mode: navigate' -H 'Sec-Fetch-Dest: document' \
     http://127.0.0.1:8090<admin>/ | grep -E '^HTTP|Location|Cache-Control'
   ```

   ```text
   HTTP/1.1 303 See Other
   Cache-Control: no-store
   Location: <admin>/login
   ```

   The admin skips the page stages. It has its own stricter security policy, never lets a browser cache its pages, and sends you to sign in when there is no session (the `Location` line also carries a `next` value that brings you back). The two `Sec-Fetch` headers say "a top-level page load", which is what a browser sends from the address bar; without them the admin answers 404, so a script on a public page can never read an admin page.

## Check it worked

You saw a page built by the theme (step 1), a 404 from the theme (step 4), a JSON 404 (step 5), a redirect (step 6) and the admin's sign-in redirect (step 7). Each response told you which stage answered it.

If step 1 returns `502` or `connection refused`, the server is not listening on that port. Check `API_PORT` in the settings file and `systemctl status hatamoto-api`. If step 6 prints `DATABASE_URL is required`, load the service's settings in your shell first.

The page cache in stage 3 is used only when the cache is on, the site's public address is set, the address has no query string (except `?page=2` and higher) and the page is not a preview. Those rules, and every other detail of each stage, are on [Request flow](request-flow.md).
