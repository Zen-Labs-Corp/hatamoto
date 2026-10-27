---
title: Hatamoto documentation
nav_title: Documentation
description: Install Hatamoto, run your site, build themes and plugins, and keep the server updated and backed up.
---

Hatamoto is an open-source system for running a website: you write pages and posts in the admin, and Hatamoto shows them to your visitors. It is one program file plus a Postgres database, the database where Hatamoto stores everything. These docs help you install it, run your site, change its look and keep the server healthy.

## I want to...

Pick the path that matches what you came to do.

- **Run my own site.** Follow the [Start here](getting-started/index.md) path in Getting started. It takes you from a download to a running site, your owner account and your first published page.
- **Build a theme.** Follow [Your first theme](themes/your-first-theme.md) to make a small theme and see your changes on a live preview.
- **Run Hatamoto on a server.** Follow [Deploy with systemd](operate/deploy-systemd.md) to install it as a service on a Linux server, then add HTTPS and backups.

Every other topic has a section of its own, listed below this introduction. Each section starts with its guides and ends with its reference pages.

## What Hatamoto gives you

- A website out of the box: the server builds each page itself, with a default theme, a blog and pages.
- An admin with a blog editor and a visual page editor, where you arrange blocks on a page and see the result as you work.
- An import from WordPress that keeps every old address working, so links and search results still lead to the right page.
- Tools for AI and search engines: reports on which **crawlers**, the programs that fetch pages for a search engine or an AI company, read which page and whether they sent you visitors. There is also an `llms.txt` file that tells AI tools what they may use, and **JSON-LD**, machine-readable data in each page that tells search engines what the page is about.
- An **MCP** server, using the Model Context Protocol, a standard way for AI tools to read your site and to write and publish through it.
- Plugins and themes you install from the admin, with nothing to compile. Plugins run in a **sandbox**, a closed space where each plugin can do only what its permissions allow.

## Privacy and licence

Hatamoto is licensed under Apache-2.0. It sends no usage data about your site. The only call it makes on its own is an update check: one request for a signed list of current releases, and you can turn it off. [How updates work](updates/overview.md) explains what the check does and how to stop it.

## Two kinds of page

The docs have two kinds of page, and each section lists them apart.

- A **guide** walks you through one task from start to finish: what you need, the steps, and how to check that it worked. Read it from the top.
- A **reference** page lists everything about one area: every setting, command or option, with its default and its meaning. Use it to look something up.

Each guide ends with suggested next steps, so you can keep going without searching.

## Getting help

Every page of these docs is a Markdown file in the project repository, and each one is checked against the code. If something is wrong or unclear, you can propose a fix: [Contributing](migrate-contribute/contributing.md) explains how to send a change.
