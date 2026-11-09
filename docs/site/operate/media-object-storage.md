---
title: Keep media in object storage
nav_title: Media in object storage
description: Store the media library in an S3-compatible bucket instead of on the server's disk, move existing files across with one command, and back up and restore a site that uses it.
next: backups.md, configuration-reference.md, hardening.md
---

In this guide you move the media library of a Hatamoto site from the server's disk into a bucket of S3-compatible object storage. At the end new uploads, replaced files and generated image variants are stored in the bucket, existing files are copied over and checked, and the site's `/media/...` addresses are unchanged.

Hatamoto still serves every file itself, from the same `/media/` path as before. Visitors never see the bucket, and the bucket can stay private.

## Before you start

- A bucket on an S3-compatible service (Amazon S3, Cloudflare R2, Backblaze, MinIO or similar) and an access key that can read, write, list and delete objects in it. Use a private bucket.
- Turn on versioning in the bucket if the service offers it. Versioning is then the history of your media files: a backup of this site records which files it needs, not their bytes (see [Back up and restore](#back-up-and-restore)).
- The Hatamoto command line on the server, run as the service user from the service's directory (or with the same environment as the service). `hatamoto media migrate` needs `DATABASE_URL`.
- Enough free disk for the temporary files Hatamoto uses: it has to hold one file at a time on local disk while it uploads to or reads from the bucket. Temporary files are removed when the operation ends.

The settings below are examples. Never put the secret key in a file that is committed or shared.

## Steps

1. Set the bucket in the service's environment. Do not set `MEDIA_STORAGE` yet:

   ```bash
   HATAMOTO_MEDIA_S3_ENDPOINT=https://s3.example.com
   HATAMOTO_MEDIA_S3_REGION=us-east-1
   HATAMOTO_MEDIA_S3_BUCKET=my-site-media
   HATAMOTO_MEDIA_S3_PREFIX=site/
   HATAMOTO_MEDIA_S3_ACCESS_KEY=<access key>
   HATAMOTO_MEDIA_S3_SECRET_KEY=<secret key>
   ```

   Leave `HATAMOTO_MEDIA_S3_ENDPOINT` empty to use Amazon S3 in the given region. The prefix is placed in front of every object name exactly as you write it, so end it with `/` to keep the files in a folder of the bucket. Several sites can share one bucket with different prefixes. The endpoint must use `https`, unless it is a local or private address such as a MinIO on your own network.

2. See what would be copied. A dry run reads the library and the bucket and writes nothing:

   ```bash
   hatamoto media migrate --to s3 --dry-run
   ```

3. Copy the files:

   ```bash
   hatamoto media migrate --to s3
   ```

   The output looks like this:

   ```text
   media migrate: local -> s3
     bucket  my-site-media/site/
     [ 128/1284] 230 MiB
     [1284/1284] 2.3 GiB  done

   copied      1,280   verified sha256
   skipped         4   already at the destination, same sha256
   failed          0
   source kept: /opt/hatamoto/uploads
   next: set MEDIA_STORAGE=s3 and restart
   ```

   Every file is read back from the bucket and compared by SHA-256 before it counts as copied. The command never deletes the source. If it stops or some files fail, fix the cause and run it again: files that are already in the bucket with the same SHA-256 are skipped, so it resumes where it stopped. The exit code is non-zero when any file failed.

4. Switch the site over. Add `MEDIA_STORAGE=s3` to the environment and restart Hatamoto:

   ```bash
   MEDIA_STORAGE=s3
   ```

   Files uploaded between the copy and the restart are still only on disk. Run `hatamoto media migrate --to s3` once more before the restart, with the site idle, to copy them.

5. Keep the old upload directory until you have checked the result and taken a backup. Then remove it, or keep it as an extra copy.

## What changes and what does not

- **Addresses.** `/media/...` addresses, and `MEDIA_BASE_URL`, work as before. Existing pages and links keep working.
- **Serving.** The server answers each request from the bucket, with an `ETag` from the object, so a browser that already has the file gets a `304` without the file being fetched. `Cache-Control` is the same as for local files. Range requests work. A file that is not in the bucket gives `404`. When the bucket cannot be reached, the file gives `502` with a message and a line in the log.
- **Everything in the library works:** upload, replace, delete, resized and WebP/modern variants, rebuilding variants, folders, usage and the media functions that plugins use. Plugin runs that import files from the web or from an owner's upload are staged in the bucket too.
- **Local disk.** With `MEDIA_STORAGE=s3` nothing is written under `MEDIA_UPLOAD_DIR`.
- **Size limits.** `MEDIA_MAX_SIZE_MB` still limits each upload. The total-size quota counts local disk only and does not apply to object storage: use the provider's own limits and alerts.
- **Large files.** Hatamoto copies a file to a temporary file on disk before it uploads it or serves it, so very large files need that much free space for the moment they are in use.

## When the bucket is wrong or down

`hatamoto serve` refuses to start when `MEDIA_STORAGE=s3` and a required setting is missing or invalid (the message names the setting: region, bucket, access key, secret key or endpoint, or an unknown value of `MEDIA_STORAGE`). It does not contact the bucket at boot, so an unreachable bucket, a wrong endpoint or wrong credentials do not stop the site. Pages still render. Uploads and `/media/...` requests fail with a clear error and a line in the log until the bucket is back.

For owners, the dashboard's Site health card has a **Media storage** row on a site that uses object storage. It shows "Object storage, reachable" or "Object storage: bucket unreachable". The row never shows the endpoint or the credentials.

## Back up and restore

A backup (`hatamoto backup create`, and the one taken before an update) of a site that keeps media in object storage does not copy the files. It stores `media-manifest.json`, a list of every file the media library needs with its object name, size and SHA-256, and the database. The backup stays small, and the bucket is where the files live: turn on versioning, and use the provider's replication or backup features, if you need the files themselves to survive deletion or loss of the bucket.

To put the bytes in the backup too, add `--with-media`:

```bash
hatamoto backup create --with-media
```

`hatamoto backup restore` checks the manifest against the bucket before it writes anything and reports files that are missing from the bucket or whose size or SHA-256 differ. That report is a warning: the restore continues, so a missing file is something you fix afterwards, not a reason the restore stops. It runs in `--dry-run` too. Restore a site that uses object storage with `MEDIA_STORAGE=s3` and the `HATAMOTO_MEDIA_S3_*` settings in the environment, so the check can reach the bucket. If you restore a backup that holds the media bytes (`--with-media`) into object storage, the prefix must be empty, and the restore writes the files into it. See [Back up and restore](backups.md) for the rest of the procedure.

`hatamoto export` and `hatamoto import` read and write media through the same storage, so an export of such a site includes the files.

## Move back to local disk

To go back, make the local settings correct and copy the files down:

```bash
hatamoto media migrate --to local --dry-run
hatamoto media migrate --to local
```

The upload directory is `MEDIA_UPLOAD_DIR`. The bucket is read with the `HATAMOTO_MEDIA_S3_*` settings whatever `MEDIA_STORAGE` is set to. The command checks each copied file by SHA-256 and never deletes anything in the bucket. When it ends, unset `MEDIA_STORAGE` (or set it to `local`) and restart.

## Check it worked

Run `hatamoto media migrate --to s3 --dry-run` again after the switch: it should report nothing to copy. Upload a small image in the media library and confirm the file appears in the bucket under your prefix and that its `/media/...` address shows it. Check that the Media storage row on the dashboard is green.

## Rolling back to an older release

Releases that came before this feature do not know `MEDIA_STORAGE`: they read `MEDIA_UPLOAD_DIR` only. Before you roll back to one, run `hatamoto media migrate --to local` and unset `MEDIA_STORAGE`.
