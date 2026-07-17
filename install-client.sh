#!/usr/bin/env bash
# lgrok — instalador do CLI para macOS e Linux.
#
#   curl -fsSL __LGROK_SERVER__/download/install-client.sh | bash
#
# O servidor substitui __LGROK_SERVER__ pelo endereço real ao servir este
# arquivo, então o cliente final não configura nada.
set -euo pipefail

SERVER="${LGROK_SERVER:-__LGROK_SERVER__}"
INSTALL_DIR="${LGROK_INSTALL_DIR:-/usr/local/bin}"

case "$(uname -s)-$(uname -m)" in
  Darwin-arm64)             BIN=lgrok-darwin-arm64 ;;
  Darwin-x86_64)            BIN=lgrok-darwin-amd64 ;;
  Linux-x86_64|Linux-amd64) BIN=lgrok-linux-amd64 ;;
  *) echo "erro: plataforma não suportada: $(uname -s) $(uname -m)" >&2; exit 1 ;;
esac

TMP="$(mktemp)"
echo "==> Baixando $BIN de $SERVER..."
curl -fsSL "$SERVER/download/$BIN" -o "$TMP"
chmod +x "$TMP"

if [[ -w "$INSTALL_DIR" ]]; then
  mv "$TMP" "$INSTALL_DIR/lgrok"
else
  echo "==> Instalando em $INSTALL_DIR (pode pedir sua senha)..."
  sudo mv "$TMP" "$INSTALL_DIR/lgrok"
fi

# Grava servidor + token (o servidor injeta o token real ao servir este script)
# e já pergunta o subdomínio + senha, salvando tudo no config local. Assim as
# próximas execuções são só "lgrok http 3000". Não sobrescreve config existente.
CFG="${LGROK_CONFIG:-$HOME/.lgrok.json}"
TOKEN="__LGROK_TOKEN__"
BASE="${SERVER#*://}"; BASE="${BASE#lgrok.}"   # ex.: uberlandia.dev.br

if [[ -f "$CFG" ]]; then
  echo "==> $CFG já existe — mantendo sua configuração atual."
elif [[ -e /dev/tty ]]; then
  # curl | bash deixa o stdin ocupado pelo script; lemos do terminal real.
  printf 'Subdomínio que você quer (ex.: meuapp.%s — vazio = aleatório): ' "$BASE" >/dev/tty
  read -r SUB </dev/tty
  SUB="$(printf '%s' "$SUB" | tr 'A-Z' 'a-z' | tr -d '[:space:]')"; SUB="${SUB%%.*}"  # só o 1º rótulo
  SECRET=""
  if [[ -n "$SUB" ]]; then
    printf 'Senha para travar "%s.%s" (criada agora, exigida depois): ' "$SUB" "$BASE" >/dev/tty
    read -rs SECRET </dev/tty; echo >/dev/tty
  fi
  printf '{\n  "server": "%s",\n  "token": "%s",\n  "subdomain": "%s",\n  "secret": "%s"\n}\n' \
    "$SERVER" "$TOKEN" "$SUB" "$SECRET" > "$CFG"
  chmod 600 "$CFG"
else
  # sem terminal (instalação automatizada): grava só server+token, o CLI
  # pergunta subdomínio/senha na primeira execução interativa.
  printf '{\n  "server": "%s",\n  "token": "%s"\n}\n' "$SERVER" "$TOKEN" > "$CFG"
  chmod 600 "$CFG"
fi

cat <<EOF

✔ lgrok instalado em $INSTALL_DIR/lgrok

Agora é só rodar (com sua aplicação no ar, ex.: porta 3000):

  lgrok http 3000

Configuração salva em $CFG.
EOF
