#!/usr/bin/env bash
# lgrok — instalador do servidor de túneis em 1 comando.
# Testado em Ubuntu/Debian (ex.: droplet padrão da DigitalOcean).
#
# Uso (interativo):
#   curl -fsSL https://raw.githubusercontent.com/lucasezsoft/lgrok/main/install.sh | sudo bash
#
# Uso (sem perguntas):
#   curl -fsSL https://raw.githubusercontent.com/lucasezsoft/lgrok/main/install.sh | sudo bash -s -- \
#     --domain suaempresa.com --email voce@suaempresa.com [--cf-token TOKEN_CLOUDFLARE]
#
# Também funciona a partir de qualquer servidor lgrok já instalado:
#   curl -fsSL https://lgrok.ezsoft.com.br/download/install.sh | sudo bash
#
# Resultado: túneis em abc.suaempresa.com e distribuição do CLI em
# https://lgrok.suaempresa.com (subdomínio fixo).
set -euo pipefail

# Fonte canônica do código (sobrescreva com LGROK_REPO_TARBALL se quiser)
REPO_TARBALL="${LGROK_REPO_TARBALL:-https://github.com/lucasezsoft/lgrok/archive/refs/heads/main.tar.gz}"
INSTALL_DIR=/opt/lgrok

DOMAIN="" EMAIL="" TOKEN="" CF_TOKEN=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --domain)   DOMAIN="$2";   shift 2 ;;
    --email)    EMAIL="$2";    shift 2 ;;
    --token)    TOKEN="$2";    shift 2 ;;
    --cf-token) CF_TOKEN="$2"; shift 2 ;; # API token Cloudflare -> cert wildcard via DNS-01
    *) echo "flag desconhecida: $1" >&2; exit 1 ;;
  esac
done

[[ $EUID -eq 0 ]] || { echo "erro: rode como root (sudo)." >&2; exit 1; }
command -v apt-get >/dev/null || { echo "erro: este instalador suporta Ubuntu/Debian." >&2; exit 1; }

ask() { local v; read -rp "$1: " v </dev/tty; echo "$v"; }
[[ -n "$DOMAIN" ]] || DOMAIN="$(ask 'Domínio base (ex.: suaempresa.com — os links ficam abc.suaempresa.com)')"
[[ -n "$EMAIL"  ]] || EMAIL="$(ask "E-mail para os certificados Let's Encrypt")"
[[ -n "$DOMAIN" && -n "$EMAIL" ]] || { echo "erro: domínio e e-mail são obrigatórios." >&2; exit 1; }

echo "==> Instalando dependências..."
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq curl ca-certificates openssl >/dev/null

[[ -n "$TOKEN" ]] || TOKEN="$(openssl rand -hex 24)"

if ! command -v docker >/dev/null; then
  echo "==> Instalando Docker..."
  curl -fsSL https://get.docker.com | sh >/dev/null
fi

# Bootstrap: rodando de dentro da pasta do projeto (ex.: primeira instalação
# da matriz, via scp/git), usa o código local em vez de baixar.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-.}")" 2>/dev/null && pwd || echo "")"
if [[ "$SCRIPT_DIR" == "$INSTALL_DIR" ]]; then
  echo "==> Usando o código já presente em $INSTALL_DIR..."
elif [[ -n "$SCRIPT_DIR" && -f "$SCRIPT_DIR/deploy/docker-compose.prod.yml" ]]; then
  echo "==> Copiando o código local de $SCRIPT_DIR para $INSTALL_DIR..."
  rm -rf "$INSTALL_DIR"
  mkdir -p "$INSTALL_DIR"
  cp -a "$SCRIPT_DIR/." "$INSTALL_DIR/"
else
  echo "==> Baixando lgrok para $INSTALL_DIR..."
  rm -rf "$INSTALL_DIR"
  mkdir -p "$INSTALL_DIR"
  curl -fsSL "$REPO_TARBALL" | tar xz --strip-components=1 -C "$INSTALL_DIR"
fi

cat > "$INSTALL_DIR/deploy/.env" <<EOF
LGROK_DOMAIN=$DOMAIN
LGROK_TOKEN=$TOKEN
ACME_EMAIL=$EMAIL
EOF
if [[ -n "$CF_TOKEN" ]]; then
  cat >> "$INSTALL_DIR/deploy/.env" <<EOF
CADDYFILE=Caddyfile.cloudflare
CF_API_TOKEN=$CF_TOKEN
EOF
fi
chmod 600 "$INSTALL_DIR/deploy/.env"

# Libera as portas web se o firewall ufw estiver ativo
if command -v ufw >/dev/null && ufw status 2>/dev/null | grep -q 'Status: active'; then
  ufw allow 80/tcp >/dev/null
  ufw allow 443/tcp >/dev/null
fi

echo "==> Compilando e subindo o servidor (pode levar alguns minutos)..."
cd "$INSTALL_DIR/deploy"
docker compose -f docker-compose.prod.yml up -d --build

IP="$(curl -fsS -4 --max-time 10 https://ifconfig.me 2>/dev/null || hostname -I | awk '{print $1}')"

cat <<EOF

============================================================
 ✔ Servidor lgrok instalado e rodando!
============================================================

1) Crie estes 2 registros no DNS do domínio:

     A    lgrok.$DOMAIN   ->  $IP    (FIXO: distribuição do CLI e conexão dos clientes)
     A    *.$DOMAIN       ->  $IP    (túneis dinâmicos: abc.$DOMAIN, xyz.$DOMAIN, ...)

   (Cloudflare: deixe como "DNS only" / nuvem cinza)

2) Token de acesso dos clientes (guarde com cuidado):

     $TOKEN

3) Assim que o DNS propagar, os clientes baixam o CLI em:

     https://lgrok.$DOMAIN                (página com os links)
     https://lgrok.$DOMAIN/download/

   E geram o link público deles com:

     lgrok http 3000 --server https://lgrok.$DOMAIN --token $TOKEN

Gerenciamento:
  logs:      cd $INSTALL_DIR/deploy && docker compose -f docker-compose.prod.yml logs -f
  reiniciar: cd $INSTALL_DIR/deploy && docker compose -f docker-compose.prod.yml restart
  status:    curl -s https://lgrok.$DOMAIN
============================================================
EOF
