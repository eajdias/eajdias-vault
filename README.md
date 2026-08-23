# sank-vault

Baixa os favoritos do Sankaku via [gallery-dl](https://github.com/mikf/gallery-dl) em Docker,
**sem duplicatas** entre execuções e com as mídias **armazenadas sem extensão** dentro de um
volume nomeado (invisível para curiosos no host). Visualização por Web UI local com login.

## Uso

```bash
# 1) baixar favoritos (pode ser interrompido e retomado à vontade)
docker compose run --rm downloader

# 2) abrir a galeria
docker compose up -d viewer
# http://127.0.0.1:8090  — senha padrão: sankvault
```

## Como funciona

| Peça | Papel |
|---|---|
| `downloader` | container oficial `gallery-dl`; grava cada mídia em `/data/media` (volume `vault_media`) |
| `archive.sqlite` | em `vault_db`: registra o que já foi baixado → **re-execuções não duplicam nada**; falhas não são gravadas (retentam depois) |
| `strip-ext.sh` | postprocessor que renomeia cada arquivo removendo a extensão logo após o download |
| `viewer` | Go + ffmpeg; detecta o tipo real por *magic bytes*, faz streaming com seek (`Range`) e gera thumbnails |
| volumes nomeados | arquivos vivem dentro do disco do Docker Desktop (WSL2), não em pastas do host |

### Formatos suportados (sem extensão, via magic bytes)

| Categoria | Formatos |
|---|---|
| Imagem exibível | JPEG/JPG, PNG, GIF (1º frame), WebP, BMP, AVIF |
| Vídeo exibível | MP4/M4V, WebM, MKV (`video/x-matroska`) |
| Só download* | HEIC/HEIF, TIFF, AVI, WMV/ASF, FLV, Ogg |

\* o lightbox mostra o tipo e um botão **baixar**; thumbnails são gerados por
ffmpeg como fallback universal (inclui HEIC/AVIF/TIFF quando o build do
ffmpeg os decodifica). Novos formatos caem em `application/octet-stream`
e continuam listados/baixáveis.

### Abas e ordenação

A UI tem abas **Geral · Imagens · GIFs · Vídeos** (com contadores) e um seletor
de ordenação por aba. A duração dos vídeos é extraída com `ffprobe` em
background (2 por vez) e cacheada em `/data/thumbs/meta.json` — enquanto a
análise roda, vídeos sem duração ficam **sempre no fim** das ordenações por
duração e um indicador `⏳ analisando vídeos… x/y` aparece nas abas.

| Aba | Ordenações disponíveis | Padrão |
|---|---|---|
| Geral | recentes, antigos, tamanho ↑↓, nome | Recentes |
| Imagens / GIFs | tamanho ↑↓, recentes, antigos, nome | **Menores primeiro** (carregam mais rápido) |
| Vídeos | duração ↑↓, tamanho ↑↓, recentes, nome | **Duração curta → longa** |

Aba/ordem/busca ficam na URL (`?tab=videos&sort=duration_asc&q=x`) para
sobreviver a refresh e ao botão voltar; a ordem escolhida é lembrada por aba
(`localStorage`). GIFs têm aba própria (`image/gif`); HEIC/TIFF só aparecem no
Geral porque o navegador não renderiza.

Endpoints: `GET /api/items?category=all|image|gif|video&sort=recent|oldest|size_asc|size_desc|duration_asc|duration_desc|name&q=&offset=&limit=` e `GET /api/stats` (contadores + progresso da análise).

## Variáveis (opcionais — padrões hardcoded)

`SANKAKU_USER`, `SANKAKU_PASS`, `FAV_TAG` (default `zubirous`), `SYNC_MODE`
(`full` é o padrão: varre a lista inteira até o fim, pulando o que já está no
archive; `incremental` para cedo ao achar mídia já baixada — sync rápido),
`VIEWER_PASSWORD` (default `sankvault`).

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
