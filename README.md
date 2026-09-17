# zub-vault

Baixa os favoritos do Sankaku via [gallery-dl](https://github.com/mikf/gallery-dl) em Docker,
**sem duplicatas** entre execuções e com as mídias **armazenadas sem extensão**.
Visualização por Web UI local com login. Todo o conteúdo é +18: ver
"Homelab e opacidade" antes de expor qualquer pasta.

## Uso

```bash
# 1) baixar favoritos (pode ser interrompido e retomado à vontade)
docker compose run --rm downloader

# 2) abrir a galeria
docker compose up -d viewer
# http://127.0.0.1:8090  — senha padrão: Zubvault@123
```

## Como funciona

| Peça | Papel |
|---|---|
| `downloader` | container oficial `gallery-dl`; grava cada mídia em `/data/media` (volume `vault_media`) |
| `archive.sqlite` | em `vault_db`: registra o que já foi baixado → **re-execuções não duplicam nada**; falhas não são gravadas (retentam depois) |
| `strip-ext.sh` | postprocessor que renomeia cada arquivo removendo a extensão logo após o download |
| `viewer` | Go + ffmpeg; detecta o tipo real por *magic bytes*, faz streaming com seek (`Range`) e gera thumbnails |
| volumes | nomeados por padrão; com `*_BIND` no `.env` viram pastas do host (ver "Homelab e opacidade") |

### Formatos suportados (sem extensão, via magic bytes)

| Categoria | Formatos |
|---|---|
| Imagem exibível | JPEG/JPG, PNG, GIF (1º frame), WebP, BMP, AVIF |
| Vídeo exibível | MP4/M4V, WebM, MKV (`video/x-matroska`) |
| Só download* | HEIC/HEIF, TIFF, AVI, WMV/ASF, FLV, Ogg |

\* o lightbox mostra o tipo e a mensagem "sem exibição no navegador" (visualização
remota apenas — não há botão de download na interface); thumbnails são gerados por
ffmpeg como fallback universal (inclui HEIC/AVIF/TIFF quando o build do
ffmpeg os decodifica). Novos formatos caem em `application/octet-stream`
e continuam listados/baixáveis.

### Abas, ordenação e modo aleatório

A UI tem abas **Geral · ★ Favoritos · Imagens · GIFs · Vídeos** (com contadores)
e um seletor de ordenação por aba. A duração dos vídeos é extraída com `ffprobe`
em background (2 por vez) e cacheada em `/data/thumbs/meta.json` — enquanto a
análise roda, vídeos sem duração ficam **sempre no fim** das ordenações por
duração e um indicador `⏳ analisando vídeos… x/y` aparece nas abas.

| Aba | Ordenações disponíveis | Padrão |
|---|---|---|
| Geral | recentes, antigos, tamanho ↑↓, nome | Recentes |
| Imagens / GIFs | tamanho ↑↓, recentes, antigos, nome | **Menores primeiro** (carregam mais rápido) |
| Vídeos | duração ↑↓, tamanho ↑↓, recentes, nome | **Duração curta → longa** |

**Modo aleatório (🔀)**: ligado por padrão — todas as mídias aparecem em ordem
embaralhada como apresentação. O embaralhamento usa um **seed determinístico**
(`?rand=1&seed=N`): a mesma seed reproduz exatamente a mesma ordem em todas as
páginas, então o scroll infinito continua estável. Clique no 🔀 para desligar e
voltar às ordenações normais (a preferência fica em `localStorage`).

**Favoritos (★)**: cada card e o lightbox têm uma estrela; favoritos são
persistidos no servidor (`/data/thumbs/favorites.json`) e aparecem na aba
**★ Favoritos**. Endpoint: `POST /api/fav/{name}`.

No lightbox: vídeos **reiniciam em loop infinito**, imagens suportam **zoom por
pinch** (1x–4x) com pan de 1 dedo, duplo toque alterna 1x ↔ 2x ancorado no
ponto e a roda do mouse também dá zoom — com zoom ativo, o swipe arrasta a
imagem em vez de navegar.

Thumbs fora da tela (±1500px) têm o `src` liberado para economizar RAM do
celular e recarregam ao voltar; a geração de thumbs é limitada a 2 simultâneas.

Aba/ordem ficam na URL (`?tab=videos&sort=duration_asc`) para sobreviver a
refresh e ao botão voltar; a ordem escolhida é lembrada por aba
(`localStorage`). GIFs têm aba própria (`image/gif`); HEIC/TIFF só aparecem no
Geral porque o navegador não renderiza.

Endpoints: `GET /api/items?category=all|fav|image|gif|video&sort=recent|oldest|size_asc|size_desc|duration_asc|duration_desc|name|random&seed=&offset=&limit=` e `GET /api/stats` (contadores + progresso da análise).

### Tags, nomes e filtro multi-tag

Cada mídia tem um sidecar `.meta/{id}_{md5}.json` com as tags do Sankaku,
gravado pelo downloader via postprocessor `metadata` do gallery-dl e
recolhido para `.meta/` pelo `strip-ext.sh` + varredura do entrypoint. O
viewer ingere os sidecars para um índice central em `/data/thumbs/tags.db`
(SQLite write-through: cada edição é 1 `UPDATE`, nunca reescrita).

**Opacidade (+18)**: mídias sem extensão e com nomes `id_md5`, sidecars em
dir oculta, thumbs sem extensão, `umask` restritivo e grupo compartilhado
`vault` (gid 2000, viewer uid 20000): só os containers leem — o desktop não
abre nem pre-visualiza sem sudo. `.nomedia` nas duas árvores afasta
indexadores (Android/Plex).

- **Pesquisa = filtro**: um campo só no topo; Enter empilha N tags (AND sobre
  tags da fonte + personalizadas). Normalização: minúsculas, espaços→`_`.
  URL compartilhável: `?tags=a+b`.
- **Autocomplete**: `GET /api/tags?prefix=&limit=20` (top por frequência).
- **Tags personalizadas**: no lightbox, `POST /api/ctag/{name}` com
  `{"add":[],"del":[]}`. Sobrevivem à re-ingestão (fonte e customs separados).

```bash
# backfill de tags p/ acervo baixado antes dos sidecars (idempotente,
# pula quem já tem .json; N opcional limita a quantidade)
docker compose run --rm --entrypoint /app/backfill-tags.sh downloader [N]
```

Nota de escala: só as tags usam SQLite (era o único estado que estourava o
teto: rewrite integral de 12–18MB por edição @20k mídias). `favorites.json`,
`filecache.json` e `meta.json` continuam JSON — reavaliar (tabela no mesmo
`.db`) se algum rewrite ficar perceptível.

### Painel de download (engrenagem ⚙)

Mostra fase, taxa, sessão e vault (heartbeat em
`/data/media/.download-status.json`) + controles: **Iniciar** (full ou
incremental), **Parar** e **Verificar** (saúde + consistência
mídia↔sidecar). Sem socket docker: a UI escreve em `/data/db/.uictrl` e o
**autosync executa** (precisa estar no ar — sem ele, Iniciar responde 503).
Incremental pula baixados e para após **5 seguidos** (`skip=abort:5`) ou no
fim da lista. Endpoints: `GET /api/download/status`, `POST
/api/download/start|stop`, `GET /api/download/verify`.

### Sync automático diário

O serviço `autosync` roda sempre (restart: unless-stopped) e dispara um **sync
incremental** todo dia às **04:00** (`AUTOSYNC_HOUR` no `.env` para mudar).
Antes de rodar ele verifica o heartbeat do painel: se já houver um download
ativo (manual ou do próprio autosync), adia 30 min sozinho. Um lock por `mkdir` em
`/data/db/.autosync-lock` impede duas instâncias simultâneas (lock obsoleto de
restart sujo é limpo no boot). Logs:
`docker compose logs -f autosync`. Para desativar: `docker compose stop autosync`.

> **Sem backups por decisão consciente do dono** (2026-09-17): cópia é prova
> espalhada, e só ele opera docker aqui. Preço: morte do disco raiz = perda
> total do vault. Não recriar rotina de backup sem ordem explícita.

### Homelab e opacidade (+18)

Desenho: dado vivo em **volumes Docker** (não em pastas). Por quê:
`/var/lib/docker` é `drwx--x--- root` — o desktop nem lista o conteúdo, e
visualizadores/indexadores (Nautilus, Plex, apps de foto) nunca olham para
lá; somado às perms do projeto (640/2750 `root:vault`, thumbs 600 uid
20000), olhada casual e thumbnail automático morrem em 2 barreiras.

Alternativa (não recomendada por padrão): bind mounts com `*_BIND` no `.env`
— dado vivo direto no homelab, zero restore, mas a pasta existe e as perms
viram a única barreira:

```
<disco>/docker/zub-vault/   <- este repo
  media/ thumbs/ db/        <- dados (criados no 1º up com bind)
```

`.env` (não versionado):

```bash
MEDIA_BIND=<disco>/docker/zub-vault/media
THUMBS_BIND=<disco>/docker/zub-vault/thumbs
DB_BIND=<disco>/docker/zub-vault/db
```

Antes do 1º up com bind (uma vez; IDs reservados: grupo `vault` gid 2000,
usuário do viewer uid 20000 — não use esses IDs para mais nada no host):

```bash
mkdir -p media thumbs db
chgrp 2000 media thumbs db && chmod 2750 media && chmod 700 thumbs db
```

No up seguinte o entrypoint converge o resto (arquivos 640 `root:vault`,
dirs com setgid). Migração a partir dos volumes nomeados:

```bash
docker compose stop viewer
# copia o CONTEÚDO de cada volume p/ a pasta irmã (rode 1x por volume):
docker run --rm -v sank-vault_vault_media:/src -v <disco>/docker/zub-vault/media:/dst alpine sh -c 'cp -a /src/. /dst/'
docker run --rm -v sank-vault_vault_db:/src -v <disco>/docker/zub-vault/db:/dst alpine sh -c 'cp -a /src/. /dst/'
docker run --rm -v sank-vault_vault_thumbs:/src -v <disco>/docker/zub-vault/thumbs:/dst alpine sh -c 'cp -a /src/. /dst/'
docker compose up -d   # entrypoint ajusta dono/grupo/perms sozinho
```

O que a opacidade cobre e o que não cobre:

- ✅ Cobre: olhada casual no gerenciador de arquivos (sem extensão, nomes
  `id_md5`, dir oculta p/ sidecars, 640/750 fora do seu usuário → sem
  thumbnail nem preview) e indexadores (Plex/Jellyfin/Android pulam sem
  extensão + `.nomedia`).
- ❌ Não cobre: acesso **determinado** (root/sudo no host abre tudo) nem
  compartilhamento com squash/all-access (SMB com `force user` ou guest
  ignora perms do Linux — o desktop enxerga tudo). Limitação assumida.

## Variáveis (opcionais — padrões hardcoded)

`SANKAKU_USER`, `SANKAKU_PASS`, `FAV_TAG` (default `zubirous`), `SYNC_MODE`
(`full` é o padrão: varre a lista inteira até o fim, pulando o que já está no
archive; `incremental` para cedo ao achar mídia já baixada — sync rápido),
`VIEWER_PASSWORD` (default `Zubvault@123`), `AUTOSYNC_HOUR` (hora do sync automático, default `04:00`).

## Comandos úteis

```bash
# teste rápido com poucos arquivos
docker compose run --rm -e GDL_EXTRA_ARGS="--range 1-3 --sleep-request 1" downloader

# conferir o que foi baixado (nomes sem extensão)
docker compose run --rm --entrypoint sh downloader -c 'ls -la /data/media | head'

# sync rápido (para no 1º item já baixado)
docker compose run --rm -e SYNC_MODE=incremental downloader

# ver logs do viewer
docker compose logs -f viewer
```

## Estrutura

```
downloader/   Dockerfile, entrypoint.sh, strip-ext.sh, gallery-dl.conf
viewer/       servidor Go (main, sniff, thumbs, auth) + static/ (UI)
docker-compose.yml
```
