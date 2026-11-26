# eajdias-vault

A local, privacy-first media vault: archive galleries from any
[gallery-dl](https://github.com/mikf/gallery-dl)-supported source in Docker,
**deduplicated** between runs, with media stored **without file extensions**.
Browse everything through a lightweight Go web UI with login.

Use cases: offline archive of artists and creators you follow, reference
collections, tutorials, and any long-lived media library you want to keep out
of platform feeds and thumbnails indexes.

## Usage

```bash
# 1) download favorites (can be interrupted and resumed freely)
docker compose run --rm downloader

# 2) open the gallery
docker compose up -d viewer
# http://127.0.0.1:8090 (or http://<ip-tailnet>:8090 on mobile) — default password: Zubvault@123
```

## How it works

| Piece | Role |
|---|---|
| `downloader` | official `gallery-dl` container; writes each media to `/data/media` (volume `vault_media`) |
| `archive.sqlite` | in `vault_db`: records what was already downloaded → **re-runs never duplicate anything**; failures are not recorded (retried later) |
| `strip-ext.sh` | postprocessor that renames each file, removing the extension right after download |
| `viewer` | Go + ffmpeg; detects the real type by *magic bytes*, streams with seek (`Range`) and generates thumbnails |
| volumes | named by default; with `*_BIND` in `.env` they become host folders (see "Privacy design") |

### Supported formats (extensionless, via magic bytes)

| Category | Formats |
|---|---|
| Displayable image | JPEG/JPG, PNG, GIF (1st frame), WebP, BMP, AVIF |
| Displayable video | MP4/M4V, WebM, MKV (`video/x-matroska`) |
| Download-only* | HEIC/HEIF, TIFF, AVI, WMV/ASF, FLV, Ogg |

\* the lightbox shows the type and a "no browser preview" message (remote
viewing only — there is no download button in the UI); thumbnails are generated
by ffmpeg as a universal fallback (including HEIC/AVIF/TIFF when the ffmpeg
build decodes them). New formats fall back to `application/octet-stream` and
remain listed/downloadable.

### Tabs, sorting and random mode

The UI has **General · ★ Favorites · Images · GIFs · Videos** tabs (with
counters) and a per-tab sort selector. Video durations are extracted with
`ffprobe` in background (2 at a time) and cached in `/data/thumbs/meta.json` —
while the analysis runs, videos without duration stay **at the end** of
duration sortings and a `⏳ analyzing videos… x/y` indicator shows in the tabs.

| Tab | Available sortings | Default |
|---|---|---|
| General | newest, oldest, size ↑↓, name | Newest |
| Images / GIFs | size ↑↓, newest, oldest, name | **Smallest first** (loads faster) |
| Videos | duration ↑↓, size ↑↓, newest, name | **Short → long** |

**Random mode (🔀)**: on by default — all media appears in shuffled order like a
slideshow. Shuffling uses a **deterministic seed** (`?rand=1&seed=N`): the same
seed reproduces exactly the same order on every page, so infinite scroll stays
stable. Click 🔀 to disable and go back to normal sortings (preference kept in
`localStorage`).

**Favorites (★)**: every card and the lightbox have a star; favorites are
persisted on the server (`/data/thumbs/favorites.json`) and appear in the
**★ Favorites** tab. Endpoint: `POST /api/fav/{name}`.

In the lightbox: videos **loop forever**, images support **pinch zoom**
(1x–4x) with 1-finger pan, double-tap toggles 1x ↔ 2x anchored at the point
and the mouse wheel zooms too — with zoom active, swiping drags the image
instead of navigating.

Thumbs off-screen (±1500px) have their `src` released to save mobile RAM and
reload when returning; thumb generation is limited to 2 concurrent jobs.

Tab/order live in the URL (`?tab=videos&sort=duration_asc`) to survive refresh
and the back button; the chosen order is remembered per tab (`localStorage`).
GIFs have their own tab (`image/gif`); HEIC/TIFF only appear in General because
the browser can't render them.

Endpoints: `GET /api/items?category=all|fav|image|gif|video&sort=recent|oldest|size_asc|size_desc|duration_asc|duration_desc|name|random&seed=&offset=&limit=` and `GET /api/stats` (counters + analysis progress).

### Tags, names and multi-tag filter

Every media has a `.meta/{id}_{md5}.json` sidecar with the source tags, written
by the downloader via gallery-dl's `metadata` postprocessor and collected into
`.meta/` by `strip-ext.sh` + the entrypoint scan. The viewer ingests sidecars
into a central index in `/data/thumbs/tags.db` (SQLite write-through: each edit
is 1 `UPDATE`, never a rewrite).

**Privacy design**: extensionless media with `id_md5` names, sidecars in a
hidden dir, extensionless thumbs, restrictive `umask` and the shared `vault`
group (gid 2000, viewer uid 20000): only the containers read them — the desktop
can't open or preview without sudo. `.nomedia` in both trees keeps indexers
(Android/Plex) away.

- **Search = filter**: a single field on top; Enter stacks N tags (AND over
  source tags + custom ones). Normalization: lowercase, spaces → `_`.
  Shareable URL: `?tags=a+b`.
- **Autocomplete**: `GET /api/tags?prefix=&limit=20` (top by frequency).
- **Custom tags**: in the lightbox, `POST /api/ctag/{name}` with
  `{"add":[],"del":[]}`. They survive re-ingestion (source and custom are kept separate).

```bash
# backfill tags for media downloaded before sidecars existed (idempotent,
# skips what already has a .json; optional N limits the amount)
docker compose run --rm --entrypoint /app/backfill-tags.sh downloader [N]
```

Scale note: only tags use SQLite (it was the only state blowing the ceiling:
full rewrites of 12–18MB per edit @20k media). `favorites.json`,
`filecache.json` and `meta.json` remain JSON — revisit (table in the same
`.db`) if any rewrite becomes noticeable.

### Download panel (gear ⚙)

Shows phase, rate, session and vault (heartbeat in
`/data/media/.download-status.json`) + controls: **Start** (full or
incremental), **Stop** and **Verify** (health + media↔sidecar consistency).
No docker socket: the UI writes to `/data/db/.uictrl` and the **autosync
executes** (must be up — without it, Start answers 503). Incremental skips
already-downloaded items and stops after **5 in a row** (`skip=abort:5`) or at
the end of the list. Endpoints: `GET /api/download/status`, `POST
/api/download/start|stop`, `GET /api/download/verify`.

### Daily automatic sync

The `autosync` service always runs (restart: unless-stopped) and fires an
**incremental sync** every day at **04:00** (`AUTOSYNC_HOUR` in `.env` to
change). Before running, it checks the panel heartbeat: if a download is
already active (manual or autosync itself), it defers 30 min. An `mkdir` lock
in `/data/db/.autosync-lock` prevents two simultaneous instances (stale lock
from a dirty restart is cleaned at boot). Logs:
`docker compose logs -f autosync`. To disable: `docker compose stop autosync`.

> **No backups by conscious owner decision (2026-09-17)**: a copy is scattered
> evidence, and only the owner operates docker here. Price: root disk death =
> total vault loss. Do not recreate a backup routine without an explicit order.

### Storage and privacy design

Design: live data in **Docker volumes** (not folders). Why:
`/var/lib/docker` is `drwx--x--- root` — the desktop can't even list the
contents, and viewers/indexers (Nautilus, Plex, photo apps) never look there;
on top of the project perms (640/2750 `root:vault`, thumbs 600 uid 20000),
casual browsing and automatic thumbnailing die at 2 barriers.

Alternative (not recommended by default): bind mounts with `*_BIND` in `.env`
— live data directly in the homelab, zero restore, but the folder exists and
perms become the only barrier:

```
<disk>/docker/eajdias-vault/   <- this repo
  media/ thumbs/ db/           <- data (created on 1st up with bind)
```

`.env` (not versioned):

```bash
MEDIA_BIND=<disk>/docker/eajdias-vault/media
THUMBS_BIND=<disk>/docker/eajdias-vault/thumbs
DB_BIND=<disk>/docker/eajdias-vault/db
```

Before the 1st up with bind (once; reserved IDs: `vault` group gid 2000,
viewer user uid 20000 — don't use these IDs for anything else on the host):

```bash
mkdir -p media thumbs db
chgrp 2000 media thumbs db && chmod 2750 media && chmod 700 thumbs db
```

On the next up the entrypoint converges the rest (files 640 `root:vault`,
dirs with setgid). Migration from named volumes:

```bash
docker compose stop viewer
# copy the CONTENTS of each volume to the sibling folder (run 1x per volume):
docker run --rm -v sank-vault_vault_media:/src -v <disk>/docker/eajdias-vault/media:/dst alpine sh -c 'cp -a /src/. /dst/'
docker run --rm -v sank-vault_vault_db:/src -v <disk>/docker/eajdias-vault/db:/dst alpine sh -c 'cp -a /src/. /dst/'
docker run --rm -v sank-vault_vault_thumbs:/src -v <disk>/docker/eajdias-vault/thumbs:/dst alpine sh -c 'cp -a /src/. /dst/'
docker compose up -d   # entrypoint adjusts owner/group/perms by itself
```

What the privacy design covers and doesn't:

- ✅ Covers: casual look in the file manager (no extension, `id_md5` names,
  hidden dir for sidecars, 640/750 outside your user → no thumbnail or
  preview) and indexers (Plex/Jellyfin/Android skip extensionless files +
  `.nomedia`).
- ❌ Doesn't cover: **determined** access (root/sudo on the host opens
  everything) nor sharing with squash/all-access (SMB with `force user` or
  guest ignores Linux perms — the desktop sees everything). Assumed limitation.

## Variables (optional — hardcoded defaults)

`SANKAKU_USER`, `SANKAKU_PASS`, `FAV_TAG` (default `zubirous`), `SYNC_MODE`
(`full` is the default: scans the whole list to the end, skipping what's
already in the archive; `incremental` stops early when it finds already
downloaded media — fast sync), `VIEWER_PASSWORD` (default `Zubvault@123`),
`AUTOSYNC_HOUR` (auto sync hour, default `04:00`).

> The default config follows the Sankaku Channel extractor; gallery-dl
> supports hundreds of sites, so point the config at any source you want.

## Useful commands

```bash
# quick test with few files
docker compose run --rm -e GDL_EXTRA_ARGS="--range 1-3 --sleep-request 1" downloader

# check what was downloaded (extensionless names)
docker compose run --rm --entrypoint sh downloader -c 'ls -la /data/media | head'

# fast sync (stops at the 1st already-downloaded item)
docker compose run --rm -e SYNC_MODE=incremental downloader

# watch viewer logs
docker compose logs -f viewer
```

## Structure

```
downloader/   Dockerfile, entrypoint.sh, strip-ext.sh, gallery-dl.conf
viewer/       Go server (main, sniff, thumbs, auth) + static/ (UI)
docker-compose.yml
```
