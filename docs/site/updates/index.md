---
title: How updates work
nav_title: How updates work
description: Check for and apply updates, pin a version or turn updates off, and roll back safely when something goes wrong.
---

This section is for the person who runs a Hatamoto server. Hatamoto can keep itself current: it checks for new releases every few hours, installs the ones your settings allow, checks that each new version starts and answers, and goes back to the previous version on its own when it does not.

The guides walk you through the everyday tasks: seeing what the updater is doing, installing an update now, holding updates back, and rolling back. The reference pages describe every command, setting, file and check in detail. In short: run the shipped Docker image or systemd unit, keep `HATAMOTO_DATA` on a writable volume, and leave `HATAMOTO_UPDATES` at its default. To publish a release yourself, see [Release process](../migrate-contribute/release-process.md). To compare the keys your install trusts with the published ones, see [Signing keys and fingerprints](keys.md).
