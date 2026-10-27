---
title: Architecture
nav_title: Architecture
description: What runs inside a Hatamoto server, how it answers a request, where it keeps your data and what can fail, for the people who run it.
---

This section is for the person who runs a Hatamoto server and wants to know what is inside it. Hatamoto is one program plus one Postgres database: the public site, the admin, the JSON API, the endpoint for AI tools, the background jobs and the plugins all run in that one process, and the database structure is built into the program and updated when it starts.

Start with the guides to look at your own server: what runs, how a page request travels through it and where each piece of data is kept. The reference pages then hold the detail: every stage of a request, every table, the migration rules, the security controls, the plugin sandbox, the background jobs, the third-party code and the source packages.
