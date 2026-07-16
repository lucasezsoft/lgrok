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

cat <<EOF

✔ lgrok instalado em $INSTALL_DIR/lgrok

Para gerar seu link público (peça o token ao administrador):

  lgrok http 3000 --server $SERVER --token SEU_TOKEN

Dica — configure uma vez no seu ~/.zshrc (ou ~/.bashrc):

  export LGROK_SERVER=$SERVER
  export LGROK_TOKEN=SEU_TOKEN

e o comando vira só:  lgrok http 3000
EOF
