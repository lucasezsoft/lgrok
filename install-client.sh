#!/usr/bin/env bash
# lgrok — instalador do CLI para macOS e Linux.
#
#   curl -fsSL __LGROK_SERVER__/download/install-client.sh | bash
#
# O servidor substitui __LGROK_SERVER__ pelo endereço real ao servir este
# arquivo, então o cliente final não configura nada.
set -euo pipefail

SERVER="${LGROK_SERVER:-__LGROK_SERVER__}"

case "$(uname -s)-$(uname -m)" in
  Darwin-arm64)             BIN=lgrok-darwin-arm64 ;;
  Darwin-x86_64)            BIN=lgrok-darwin-amd64 ;;
  Linux-x86_64|Linux-amd64) BIN=lgrok-linux-amd64 ;;
  *) echo "erro: plataforma não suportada: $(uname -s) $(uname -m)" >&2; exit 1 ;;
esac

# Instala SEM sudo/senha: usa uma pasta sua. /usr/local/bin só se já for
# gravável (evita o prompt de senha do Mac, que falha via 'curl | bash').
if [[ -n "${LGROK_INSTALL_DIR:-}" ]]; then
  INSTALL_DIR="$LGROK_INSTALL_DIR"
elif [[ -w /usr/local/bin ]]; then
  INSTALL_DIR=/usr/local/bin
else
  INSTALL_DIR="$HOME/.local/bin"
fi
mkdir -p "$INSTALL_DIR"

TMP="$(mktemp)"
echo "==> Baixando $BIN de $SERVER..."
curl -fsSL "$SERVER/download/$BIN" -o "$TMP"
chmod +x "$TMP"
mv "$TMP" "$INSTALL_DIR/lgrok"

# Garante a pasta no PATH (sem sudo). Prepend para vencer um lgrok antigo.
if [[ ":$PATH:" != *":$INSTALL_DIR:"* ]]; then
  rc="$HOME/.zshrc"; [[ "${SHELL:-}" == */bash ]] && rc="$HOME/.bashrc"
  grep -qs '# lgrok PATH' "$rc" 2>/dev/null || printf '\n# lgrok PATH\nexport PATH="%s:$PATH"\n' "$INSTALL_DIR" >> "$rc"
  echo "==> Adicionei $INSTALL_DIR ao seu PATH ($rc)."
  echo "    Para usar agora nesta janela:  export PATH=\"$INSTALL_DIR:\$PATH\""
fi

# Sempre atualiza server + token (o servidor injeta o token real ao servir este
# script), PRESERVANDO subdomínio/senha/auto de uma config existente. Assim
# reinstalar/atualizar conserta um token trocado sem perder seu domínio próprio.
# Nada de perguntas: a 1ª execução do lgrok já sobe um link temporário; domínio
# próprio depois é "lgrok http <porta> --config".
CFG="${LGROK_CONFIG:-$HOME/.lgrok.json}"
TOKEN="__LGROK_TOKEN__"
SUB=""; SECRET=""; AUTO=""
if [[ -f "$CFG" ]]; then
  jget() { grep -oE "\"$1\"[[:space:]]*:[[:space:]]*\"[^\"]*\"" "$CFG" | sed -E 's/.*"([^"]*)"$/\1/' | head -1; }
  SUB="$(jget subdomain)"; SECRET="$(jget secret)"
  grep -qE '"auto"[[:space:]]*:[[:space:]]*true' "$CFG" && AUTO=1
fi
{
  printf '{\n  "server": "%s",\n  "token": "%s"' "$SERVER" "$TOKEN"
  [[ -n "$SUB"    ]] && printf ',\n  "subdomain": "%s"' "$SUB"
  [[ -n "$SECRET" ]] && printf ',\n  "secret": "%s"' "$SECRET"
  [[ -n "$AUTO"   ]] && printf ',\n  "auto": true'
  printf '\n}\n'
} > "$CFG"
chmod 600 "$CFG"

cat <<EOF

✔ lgrok instalado em $INSTALL_DIR/lgrok

Agora é só rodar (com sua aplicação no ar, ex.: porta 3000):

  lgrok http 3000

Na 1ª vez ele já sobe com um link temporário. Para um domínio próprio e fixo
(com senha): lgrok http 3000 --config
EOF
