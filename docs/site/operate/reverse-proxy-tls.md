---
title: Put a reverse proxy in front
nav_title: Reverse proxy and TLS
description: Serve your site over HTTPS by putting Caddy or nginx in front of Hatamoto, with the forwarded headers and public address set correctly.
next: hardening.md, email.md, configuration-reference.md
---

In this guide you put a reverse proxy in front of Hatamoto so visitors reach your site over HTTPS. Hatamoto speaks plain HTTP on one port (8090 by default) and does not handle TLS itself. At the end the proxy holds the certificate, forwards every request to that one port, and Hatamoto knows its public address and which proxy to trust.

## Before you start

- Hatamoto running and answering on `127.0.0.1:8090`. See [Deploy with systemd](deploy-systemd.md) or [Deploy with Docker](deploy-docker.md).
- A domain, here `example.com`, whose DNS points at the server, with ports 80 and 443 open.
- Caddy installed, or nginx with a certificate for the domain.

## Steps

> Your admin address is private to your install, for example `/k7qm-x2rf-9tdc`. Print it on the server with `hatamoto admin-url`. This page writes it as `<admin>`.

1. Write the Caddy site file, `/etc/caddy/Caddyfile`. Caddy obtains and renews the certificate itself. Pages, the admin (`<admin>`), `/api/*`, `/media/*`, `/health`, `robots.txt`, sitemaps, `llms.txt`, theme files and site icons all come from the same process, so no path rules are needed, and Caddy's `reverse_proxy` sets `X-Forwarded-For`, `X-Forwarded-Proto` and `X-Forwarded-Host` by default, which is what Hatamoto reads. This is the whole site:

   ```text
   www.example.com {
       redir https://example.com{uri} permanent
   }

   example.com {
       encode gzip
       reverse_proxy localhost:8090
   }
   ```

2. Check and load the file:

   ```bash
   caddy validate --config /etc/caddy/Caddyfile
   sudo systemctl reload caddy
   ```

3. If you use nginx instead, forward all paths to the one port and set the same three headers. Keep the proxy's body limit above `MEDIA_MAX_SIZE_MB` (default 10) and above the largest import archive you plan to upload, or uploads fail at the proxy.

   ```nginx
   server {
       listen 443 ssl http2;
       server_name example.com;
       # ssl_certificate and ssl_certificate_key go here

       location / {
           proxy_pass http://127.0.0.1:8090;
           proxy_set_header Host $host;
           proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
           proxy_set_header X-Forwarded-Proto $scheme;
           proxy_set_header X-Forwarded-Host $host;
           client_max_body_size 20m;
       }
   }
   ```

4. Do not repeat Hatamoto's security headers in the proxy. It already sends them (see [Harden an instance](hardening.md)), and a different value set in the proxy replaces Hatamoto's. For example, public pages send `X-Frame-Options: SAMEORIGIN` so the admin preview can show them; a proxy that sets `DENY` breaks that preview.

5. Set the public address in Hatamoto's environment and restart it. It fixes the host used in canonical links, sitemaps, structured data, `llms.txt` and emailed links, because the host the server sees may be an internal address. Password reset and invitation emails are sent only when this or the Site address setting is set, and the page cache needs it.

   ```bash
   PUBLIC_BASE_URL=https://example.com
   ```

6. Check which proxies Hatamoto trusts. It believes `X-Forwarded-*` headers only from a trusted address. The default is loopback only, which covers a proxy on the same host that connects to `127.0.0.1` and Hatamoto itself runs on that host. **Docker is different:** a host proxy that reaches a published port (`127.0.0.1:8090:8090`) arrives inside the container from the network's bridge gateway (for example `172.18.0.1`), not from loopback, so Docker deployments must set `HATAMOTO_TRUSTED_PROXIES` to that gateway, or to the proxy container's address when the proxy is a Compose service. Trust the gateway only while the port is published on `127.0.0.1`: with `"8090:8090"` (every interface) remote clients arrive from the gateway too and can forge `X-Forwarded-For`. Find the gateway with `docker network inspect <project>_default --format '{{(index .IPAM.Config 0).Gateway}}'`. If your proxy is in another container (for example a Caddy service in Docker Compose), on another machine, or a cloud load balancer, you must list its address or range, comma separated; until you do, Hatamoto ignores the forwarded client IP and scheme and logs a one-time warning. Private ranges are not trusted automatically. A non-empty value replaces the default, and an invalid entry stops the boot. What trust changes (client IP, HTTPS, host) is in the [configuration reference](configuration-reference.md).

   ```bash
   HATAMOTO_TRUSTED_PROXIES=10.0.0.0/8,203.0.113.7
   ```

   For Docker with a host proxy, set the bridge gateway in `production.yml` under the `hatamoto` service `environment:`:

   ```yaml
   HATAMOTO_TRUSTED_PROXIES: 172.18.0.1
   ```

7. Only then set `ENVIRONMENT=production`. Hatamoto then sends `Strict-Transport-Security: max-age=31536000; includeSubDomains`, which browsers cache for a year for every subdomain. First make sure no subdomain of your domain is still served over plain HTTP, or it becomes unreachable for anyone who has visited. Do not add a second HSTS header in the proxy. HSTS tells browsers to use only the secure address of your site.

The repository's own site file, `deploy/caddy/hatamoto.dev.caddy`, adds extras for the project's own host: it serves signed release files and the plugin registry as static files, and answers Go import paths. A self-hosted site needs none of that; if you run your own package registry or release server, see [SDK and registry](../migrate-contribute/sdk-and-registry.md). To feed the proxy's access log into crawler analytics, see [Access-log ingest](../ai-seo/access-log-ingest.md); it needs `AGENT_LOG_INGEST`, a secret and, for Caddy, the path of the JSON log file.

## Outbound requests

Hatamoto itself makes few outbound requests. The one that runs by default is the daily download of AI-crawler IP ranges used to verify crawler analytics. If your server sits behind an egress firewall, or you want no third-party contact, set `HATAMOTO_AGENT_RANGES=off`. Verification then falls back to reverse DNS, which uses your resolver, not HTTP; details in the [configuration reference](configuration-reference.md#crawler-ip-range-refresh).

## Check it worked

From any machine:

```bash
curl -sI https://example.com/health
curl -s https://example.com/ready
```

The first prints a `200` status line and the headers, including Hatamoto's `X-Content-Type-Options: nosniff`; the second prints `ok`. Add `-H "X-API-Key: ..."` to `/ready` to see the JSON detail, `{"database":"connected","status":"ready"}`. `/health` answers as soon as the process is up; `/ready` also checks the database and answers 503 when it cannot reach it. See [health checks](multi-replica-and-health.md).

If you get `502 Bad Gateway`, the proxy cannot reach Hatamoto: check that `curl -s http://127.0.0.1:8090/health` works on the server. If uploads fail with `413`, raise the proxy's body limit as in step 3. If the login lockout or analytics show the proxy's address for every visitor, Hatamoto does not trust the proxy: add its address as in step 6.
