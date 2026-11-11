---
title: Keep memory use low
nav_title: Memory use
description: What a running Hatamoto site holds in memory, the limit it sets from its container, and how to set your own with GOMEMLIMIT.
next: ../admin/performance-cache.md, deploy-systemd.md, configuration-reference.md
---

In this guide you check how much memory your Hatamoto server holds, learn which limit it applies on its own, and set a limit yourself when you want a different one. Hatamoto is written in Go, and the Go runtime decides when to hand unused memory back to the operating system. A **soft memory limit** tells it to work harder at that as the process gets close to a ceiling.

## Before you start

- A running install, started with `hatamoto supervise` (the shipped systemd unit and the Docker image do this) or `hatamoto serve`.
- A shell on the server, to read process memory.
- Owner access to the admin, to read the memory page.

## Steps

### Know what to expect

A site with the starter content, a few dozen pages and posts, and no plugins held about 130 MiB in total after it had been idle for a couple of minutes: roughly 105 MiB for the server process and 25 MiB for the supervisor that starts it. The project measures this on every full test run and fails the run when the number grows past its recorded baseline or past 200 MiB. This is a measurement of one small site, not a promise: plugins, many concurrent visitors and the first media upload each add memory.

Two things make the number move:

- **Signing in.** Hashing a password takes 64 MiB for a moment. The runtime keeps that room until its next collection, so the process can look larger for up to two minutes after a sign-in, then shrinks.
- **The first media upload.** The image tools load a WebAssembly runtime that stays loaded afterwards, which adds memory.

### Read the number

1. In the admin, open **Settings > Performance** and the memory view (`<admin>/settings/performance/memory`). It shows the live heap, the total heap, the memory limit and the time spent on garbage collection. It does not show the whole process.
2. For the whole process, ask the kernel. The `Pss` line is the size that counts each shared page once, so the program file is not counted twice for the supervisor and the server:

   ```bash
   for p in $(pgrep -f 'hatamoto (supervise|serve)'); do
     grep '^Pss:' /proc/$p/smaps_rollup
   done
   ```

### Use the default limit

When you set no `GOMEMLIMIT`, Hatamoto reads the memory limit of its container or systemd cgroup at boot and sets the soft limit to 88 percent of it, so a service started with `MemoryMax=512M` or `docker run --memory 512m` stays under about 450 MiB before the kernel steps in. It logs `memory limit set from the container limit` with the value in bytes. Without a cgroup limit, or with an unlimited one (`max`), Hatamoto sets nothing and the runtime uses its normal growth rules.

The limit is soft: it makes the runtime collect garbage and return memory more often as it gets near. It does not refuse requests. A limit below what the site really needs makes the server spend most of its time collecting, so leave room above the measured figure.

### Set your own limit

`GOMEMLIMIT` always wins over the default. It takes a size such as `300MiB` or `1GiB`.

1. With systemd, add the line to the environment file, which the unit reads with `EnvironmentFile=` (see [Deploy with systemd](deploy-systemd.md)), then restart:

   ```ini
   GOMEMLIMIT=300MiB
   ```

   ```bash
   sudo systemctl restart hatamoto-api
   ```

2. With Docker Compose, add it under `environment:` of the `hatamoto` service in `production.yml` (see [Deploy with Docker](deploy-docker.md)), or pass `-e GOMEMLIMIT=300MiB` to `docker run`.

## Check it worked

Restart the server and read the log for the line that names the limit, or open the memory view again and read **Memory limit**. It shows the size you set, or the 88 percent value, or `not set` when neither applies.

```bash
journalctl -u hatamoto-api --since '5 minutes ago' | grep 'memory limit'
```

If you set `GOMEMLIMIT`, the log line is absent, because Hatamoto leaves your value alone: read **Memory limit** instead. The page cache also lives in memory; see [Performance and the page cache](../admin/performance-cache.md) for how to size it.
