# Nível 2 — cifra em repouso (gocryptfs). DESENHO, não implementado.

Status: proposta aprovada como desenho em 2026-09-17; Nível 1 (perms+grupo
vault+opacidade) é o que está no ar. Implementar quando: acesso determinado
for plausível (outros sudores no host, SMB com squash/all-access, backups em
lugar não confiável).

## O que a cifra cobre (e o que não cobre)

- ✅ Cobre: disco roubado/perdido, backup levado para qualquer lugar
  (blobs cifrados valem sozinhos), leitura direta dos arquivos com o
  filesystem desmontado, troca de SO (basta reinstalar gocryptfs + chave).
- ❌ Não cobre: root/sudo no host **com o vault montado** (plaintext na
  hora), RAM/live forensics. Mitigação parcial: `idle` timeout desmonta
  sozinho sem uso (ver § mount).

## Por que gocryptfs (e não as alternativas)

- Arquivo-a-arquivo (1 blob cifrado por arquivo): backup incremental,
  restic/Syncthing-friendly; corrupção local não mata o vault inteiro.
- LUKS protege o disco, não a pasta: montado, o desktop vê tudo — não
  resolve o requisito. cryfs: overhead maior + histórico de instabilidade;
  encfs: auditoria condenou (não usar).
- Zero mudança de código: viewer/downloader já leem `MEDIA_DIR`/`THUMB_DIR`
  por env — só aponta para o mount plaintext.

## Layout alvo

```
<disco>/docker/zub-vault/
  docker-compose.yml  .env  downloader/ viewer/   <- repo (claro)
  cipher/             <- blobs cifrados (É ISSO que vai no backup)
  media/  thumbs/  db/   <- NÃO versionar, NÃO backupar: mounts plaintext
                             efêmeros (recriados a cada boot)
```

`.env` (não versionado):

```bash
MEDIA_BIND=<disco>/docker/zub-vault/media
THUMBS_BIND=<disco>/docker/zub-vault/thumbs
DB_BIND=<disco>/docker/zub-vault/db
```

Só `media/` precisa de cifra de verdade (o +18). `thumbs/` (derivados
identificáveis!) e `db/` (archive + sidecars... na verdade sidecars moram em
`media/.meta/`) entram no mesmo mount por simplicidade: 1 senha, 1 mount.
Alternativa minimalista: cifrar só `media/` e manter thumbs/db em claro —
mais mounts, menos superfície cifrada. Decidir na implementação.

## Chave — fora da árvore, sempre

- `gocryptfs -init cipher/` interativo UMA vez; senha forte única.
- A senha/chave mora **fora** de `<disco>/docker/zub-vault` (ex.:
  `/root/zub-vault.key` 600 no host, ou password manager + digitação no
  boot). Cópia selada em 2º lugar físico (papel/cofre).
- **Perdeu a chave = perdeu o vault**, mesmo com backup perfeito. Sem
  "recuperar senha". Documentar isso no README na implementação.

## Boot e ordem (host homelab, systemd)

1. `gocryptfs -idle 30m -extpass ... cipher/ media/` (um por mount; `-idle`
   desmonta após 30min sem acesso — janela de exposição mínima; autosync
   diário remonta? NÃO sozinho: mount precisa da chave).
   - Conflito honesto: `-idle` + autosync 04:00 = sync falha se desmontado.
     Opções: (a) sem `-idle` (montado sempre; protege disco/backup, não
     live-root); (b) com `-idle` + autosync removido do compose nesse host
     (sync vira manual). **Recomendado: (a)** — o requisito é
     anti-descobrimento + backup, não anti-root-live.
2. `docker compose up -d` com `RequiresMountsFor=` no unit (ou
   `depends_on` + healthcheck de dir — compose não espera mount; systemd
   ordena: `After=` no service do compose).
3. FUSE no host: pacote `gocryptfs` + `/dev/fuse` (sem `--privileged` nos
   containers — o mount é no HOST, containers só recebem bind).

## Migração (volumes atuais → cipher)

```bash
# 1. init + mount vazio; 2. copiar CONTEÚDO por cima do mount plaintext:
docker run --rm -v sank-vault_vault_media:/src -v <disco>/docker/zub-vault/media:/dst alpine sh -c 'cp -a /src/. /dst/'
# 3. .env com os BINDS; 4. up; 5. entrypoint converge perms sozinho (640/2750).
# 6. validar items+thumbs+tags; 7. SÓ ENTÃO apagar volumes antigos.
```

Perms dentro do plaintext continuam valendo (defesa em profundidade:
cifra + grupo vault + uids reservados).

## Backup com cifra

- Backup = `cipher/` (rsync/restic normal, qualquer destino, sem medo).
- Chave separada, nunca junto do backup.
- Restore em SO novo: instalar gocryptfs + docker, montar, up. Tags.db e
  archive.sqlite viajam dentro do cipher (estão em `thumbs/`/`db/`... ou
  dentro de `media/`? definir: hoje thumbs/db são mounts separados — com
  cifra, colocar os 3 dirs sob UM mount `data/` simplifica backup para 1
  `cipher/`).

## Checklist de implementação (quando aprovado)

1. `pacman -S gocryptfs`; decidir 1 mount (`data/`) vs 3.
2. `-init`, chave fora da árvore + cópia selada.
3. Unit systemd do mount + `After=` no compose; `.env` com BINDS.
4. Migração (§ acima) + validação ponta a ponta + apagar volumes velhos.
5. README: trocar "Homelab e opacidade" pelo fluxo com cifra; registrar a
   regra "chave nunca no backup".
