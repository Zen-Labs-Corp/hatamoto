---
title: Before you install
nav_title: Before you install
description: Check that your server is ready for Hatamoto and gather the few things the install asks for.
next: install-binary.md, install-docker.md
---

In this guide you check that your computer or server can run Hatamoto and write down the few things the install asks for. It takes about ten minutes. At the end you know which install guide to follow and you have everything it needs at hand.

## Before you start

- A computer or server running Linux or macOS, where you can open a terminal and run commands.
- Permission to install software on it, or Docker already installed.
- About ten minutes.

## Steps

1. Choose how you will install Hatamoto. There are two ways:

   - **The program file.** You download one file, the `hatamoto` program, and run it next to a database you provide. Choose this for a server you will keep. Follow [Install the binary](install-binary.md).
   - **Docker.** You start Hatamoto and its database together in containers. Choose this to try Hatamoto on your own computer. Follow [Install with Docker](install-docker.md).

2. Check that your system is supported. Run this command in a terminal:

   ```bash
   uname -sm
   ```

   It prints the system name and the processor type, for example:

   ```text
   Linux x86_64
   ```

   Hatamoto has a program file for each of these results: `Linux x86_64`, `Linux aarch64`, `Darwin x86_64` (a Mac with an Intel processor) and `Darwin arm64` (a Mac with Apple silicon). If you see something else, such as a 32-bit system, Hatamoto has no program file for it. Docker uses the same Linux program files, so it needs a 64-bit processor too.

3. Get a Postgres database. **Postgres** is the database Hatamoto stores everything in: your pages, posts, users and settings. Hatamoto is built and tested with Postgres 16. You need an empty database and a database user that owns it. If you install with Docker, the Docker setup creates both for you and you can skip to step 5.

   If your hosting provider offers a managed Postgres database, create one there. If you run Postgres yourself, [Install the binary](install-binary.md) shows the two commands that create the database and its user.

4. Write down the database address. The **database address** tells Hatamoto where the database is and how to sign in to it. Hatamoto reads it from an **environment variable**, a named setting you give a program when it starts, outside its files. That variable is called `DATABASE_URL`, and it is the only one Hatamoto requires. The address looks like this:

   ```text
   postgres://hatamoto:your-password@localhost:5432/hatamoto?sslmode=disable
   ```

   Read it left to right: the user name (`hatamoto`), its password, the computer the database runs on (`localhost` means the same computer), the port (`5432` is the Postgres default) and the database name. When the database runs on another computer, end the address with `sslmode=require` instead, so the connection is encrypted.

5. Check that port 8090 is free. A **port** is the number a program listens on for visitors; Hatamoto uses 8090 unless you tell it otherwise. On Linux, this command prints nothing when the port is free:

   ```bash
   ss -ltn | grep ':8090 '
   ```

   If it prints a line, another program uses the port. You can pick another one later with the `API_PORT` setting.

6. Decide where the server secret will live. The **server secret** is a long random value the server uses to sign links and protect stored credentials; it must stay the same across restarts. You do not need to make one: on its first start Hatamoto creates it and saves it in the folder `data/secrets`, inside the folder you start it from. Keep that folder, and include it in your backups. For a production server you can supply your own instead; see [Requirements and secrets](requirements-and-secrets.md).

7. Check that you have nothing else to install. Hatamoto needs no separate web server, no Node, no Redis and no programming tools on the server. It sends no usage data. The only connection it opens on its own is a signed check for updates, which you can turn off later.

## Check it worked

You are ready when you have these three things written down:

- The install method you chose: the program file or Docker.
- For the program file: the database address, with the real password in it.
- The port Hatamoto will use: 8090, or the one you picked.

If you cannot get a Postgres database, follow [Install with Docker](install-docker.md): Docker runs both Hatamoto and its database for you. If `uname -sm` printed a system with no program file, use another computer or server, for example a small rented Linux server.

For the full list of requirements, the folders Hatamoto writes to and every setting worth knowing on day one, see [Requirements and secrets](requirements-and-secrets.md).
