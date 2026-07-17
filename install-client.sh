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

# Grava servidor + token (o servidor injeta o token real ao servir este script).
# Nada de perguntas aqui: a 1ª execução do lgrok já sobe um subdomínio aleatório
# na hora. Um domínio próprio depois é só "lgrok http <porta> --config".
CFG="${LGROK_CONFIG:-$HOME/.lgrok.json}"
TOKEN="__LGROK_TOKEN__"
if [[ ! -f "$CFG" ]]; then
  printf '{\n  "server": "%s",\n  "token": "%s"\n}\n' "$SERVER" "$TOKEN" > "$CFG"
  chmod 600 "$CFG"
fi

cat <<EOF

✔ lgrok instalado em $INSTALL_DIR/lgrok

Agora é só rodar (com sua aplicação no ar, ex.: porta 3000):

  lgrok http 3000

Na 1ª vez ele já sobe com um link temporário. Para um domínio próprio e fixo
(com senha): lgrok http 3000 --config
EOF
