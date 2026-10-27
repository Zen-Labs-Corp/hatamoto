---
title: Operating it
nav_title: Operating it
description: Run Hatamoto in production: deploy it with Docker or systemd, serve it over HTTPS, lock it down, back it up and send email.
---

This section is for the person who runs the server. Hatamoto is one program plus a Postgres database, so running it in production comes down to a few decisions: how the process is started (Docker or systemd), which reverse proxy handles HTTPS in front of it, which settings you give it, and how you back up the database and the files next to it.

If you are setting up a server for the first time, work through the guides in order, starting with a deployment. Come back to the reference pages when you need a specific setting, command or health check. Updates and rollback have their own section, [How updates work](../updates/index.md), and how the parts fit together is described in [Architecture](../architecture/index.md).
