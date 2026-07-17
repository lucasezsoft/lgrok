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

DOMAIN="" EMAIL="" TOKEN="" CF_TOKEN="" ADMIN_PASS="" BEHIND_NGINX=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --domain)     DOMAIN="$2";     shift 2 ;;
    --email)      EMAIL="$2";      shift 2 ;;
    --token)      TOKEN="$2";      shift 2 ;;
    --cf-token)   CF_TOKEN="$2";   shift 2 ;; # API token Cloudflare -> cert wildcard via DNS-01
    --admin-pass) ADMIN_PASS="$2"; shift 2 ;; # senha do painel /admin
    --behind-nginx) BEHIND_NGINX=1; shift ;;  # VPS já tem nginx nas portas 80/443
    *) echo "flag desconhecida: $1" >&2; exit 1 ;;
  esac
done

[[ $EUID -eq 0 ]] || { echo "erro: rode como root (sudo)." >&2; exit 1; }
command -v apt-get >/dev/null || { echo "erro: este instalador suporta Ubuntu/Debian." >&2; exit 1; }

# Reinstalação: derruba a stack lgrok anterior antes de tudo, para liberar as
# portas 80/443 que são dela mesma (senão o preflight abaixo se auto-bloqueia).
if command -v docker >/dev/null && [[ -d /opt/lgrok/deploy ]]; then
  for f in docker-compose.prod.yml docker-compose.cloudflare.yml docker-compose.behind-proxy.yml; do
    [[ -f "/opt/lgrok/deploy/$f" ]] && (cd /opt/lgrok/deploy && docker compose -f "$f" down >/dev/null 2>&1) || true
  done
fi

# As portas 80/443 precisam estar livres (o Caddy usa as duas para o HTTPS
# automático). Falha aqui é muito mais barata do que depois de compilar tudo.
# Com --behind-nginx quem usa as portas é o nginx da máquina — por isso pulamos.
if [[ -z "$BEHIND_NGINX" ]] && command -v ss >/dev/null; then
  for p in 80 443; do
    line="$(ss -tlnpH "sport = :$p" 2>/dev/null | head -1)" || true
    [[ -n "$line" ]] || continue
    who="$(printf '%s' "$line" | grep -oE 'users:\(\("[^"]+' | cut -d'"' -f2)"
    if [[ "$who" == docker-proxy ]]; then
      cat >&2 <<EOF
erro: a porta $p está em uso por OUTRO container Docker (docker-proxy).
      Veja qual é e pare-o:
        docker ps            # descubra o container
        docker stop <nome>   # libere a porta
      Depois rode o instalador de novo.
EOF
    else
      cat >&2 <<EOF
erro: a porta $p já está em uso${who:+ pelo processo "$who"}.
      O lgrok precisa das portas 80 e 443 livres (HTTPS automático).

      Se essa VPS já roda sites em um nginx que você quer manter, instale
      no modo "atrás do nginx" (o lgrok não toca nas portas nem nos sites):

        curl -fsSL .../install.sh | sudo bash -s -- --behind-nginx \\
          --domain SEUDOMINIO --email VOCE@EXEMPLO.COM

      Se o serviço não é usado, pare e desabilite antes de tentar de novo:
        systemctl disable --now ${who:-nginx}
EOF
    fi
    exit 1
  done
fi

# Reinstalação: preserva token e senha de admin do .env anterior. Assim
# atualizar o servidor NÃO invalida os clientes (o token continua o mesmo).
ENVOLD=/opt/lgrok/deploy/.env
getold() { [[ -f "$ENVOLD" ]] && grep -E "^$1=" "$ENVOLD" | cut -d= -f2- | head -1; }
OLD_TOKEN="$(getold LGROK_TOKEN)"
OLD_ADMIN="$(getold LGROK_ADMIN_PASS)"

ask() { local v; read -rp "$1: " v </dev/tty; echo "$v"; }
asksecret() { local v; read -rsp "$1: " v </dev/tty; echo >/dev/tty; echo "$v"; }
[[ -n "$DOMAIN" ]] || DOMAIN="$(ask 'Domínio base (ex.: suaempresa.com — os links ficam abc.suaempresa.com)')"
[[ -n "$EMAIL"  ]] || EMAIL="$(ask "E-mail para os certificados Let's Encrypt")"
if [[ -z "$ADMIN_PASS" ]]; then
  if [[ -n "$OLD_ADMIN" ]]; then
    ADMIN_PASS="$(asksecret 'Senha do administrador (Enter para manter a atual)')"
    [[ -n "$ADMIN_PASS" ]] || ADMIN_PASS="$OLD_ADMIN"
  else
    ADMIN_PASS="$(asksecret 'Senha do administrador (para acessar lgrok.'"$DOMAIN"'/admin)')"
  fi
fi
[[ -n "$DOMAIN" && -n "$EMAIL" ]] || { echo "erro: domínio e e-mail são obrigatórios." >&2; exit 1; }
[[ -n "$ADMIN_PASS" ]] || { echo "erro: a senha do administrador é obrigatória." >&2; exit 1; }

echo "==> Instalando dependências..."
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq curl ca-certificates openssl >/dev/null

# token: flag > .env anterior > novo aleatório
[[ -n "$TOKEN" ]] || TOKEN="$OLD_TOKEN"
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
LGROK_ADMIN_PASS=$ADMIN_PASS
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

# Monta a lista de arquivos compose. Padrão usa a imagem oficial do Caddy
# (só baixa). Cloudflare wildcard adiciona um override que COMPILA o plugin.
COMPOSE=(-f docker-compose.prod.yml)
if [[ -n "$BEHIND_NGINX" ]]; then
  COMPOSE=(-f docker-compose.behind-proxy.yml)
elif [[ -n "$CF_TOKEN" ]]; then
  COMPOSE+=(-f docker-compose.cloudflare.yml)
fi

echo "==> Subindo o servidor (pode levar alguns minutos)..."
cd "$INSTALL_DIR/deploy"
docker compose "${COMPOSE[@]}" up -d --build

# Modo atrás do nginx: gera a config pronta, mas NÃO ativa nada — mexer no
# nginx de uma máquina com sites em produção é decisão do administrador.
if [[ -n "$BEHIND_NGINX" ]]; then
  sed "s/__LGROK_DOMAIN__/$DOMAIN/g" "$INSTALL_DIR/deploy/nginx-lgrok.conf" \
    > /etc/nginx/sites-available/lgrok
fi

IP="$(curl -fsS -4 --max-time 10 https://ifconfig.me 2>/dev/null || hostname -I | awk '{print $1}')"

if [[ -n "$BEHIND_NGINX" ]]; then
cat <<EOF

============================================================
 ✔ lgrokd rodando em 127.0.0.1:8080 (atrás do seu nginx)
============================================================
 Seus sites atuais NÃO foram tocados. Faltam 3 passos manuais:

1) DNS — crie estes 2 registros apontando para $IP:

     A    lgrok.$DOMAIN
     A    *.$DOMAIN

2) Certificado wildcard (o nginx termina o TLS). Com certbot + DNS-01,
   ex. Cloudflare (token com permissão Zone > DNS > Edit):

     apt-get install -y python3-certbot-dns-cloudflare
     printf 'dns_cloudflare_api_token = SEU_TOKEN\n' > /root/.cloudflare.ini
     chmod 600 /root/.cloudflare.ini
     certbot certonly --dns-cloudflare \\
       --dns-cloudflare-credentials /root/.cloudflare.ini \\
       --dns-cloudflare-propagation-seconds 30 \\
       -d '$DOMAIN' -d '*.$DOMAIN' -m $EMAIL --agree-tos --non-interactive \\
       --deploy-hook "systemctl reload nginx"

   O --deploy-hook é essencial: sem ele o certbot renova o certificado a
   cada 90 dias mas o nginx segue servindo o antigo até um reload manual.
   (Outro provedor de DNS: troque o plugin — certbot-dns-route53,
    certbot-dns-digitalocean etc.)

3) Ative o site do lgrok (a config já está pronta e revisável):

     cat /etc/nginx/sites-available/lgrok        # revise antes
     ln -s /etc/nginx/sites-available/lgrok /etc/nginx/sites-enabled/lgrok
     nginx -t && systemctl reload nginx

Depois disso: https://lgrok.$DOMAIN (clientes) e /admin (painel).

Token dos clientes (embutido no instalador; guarde para referência):
  $TOKEN

Gerenciamento:
  logs:      cd $INSTALL_DIR/deploy && docker compose -f docker-compose.behind-proxy.yml logs -f
  reiniciar: cd $INSTALL_DIR/deploy && docker compose -f docker-compose.behind-proxy.yml restart
  senha admin / token: edite $INSTALL_DIR/deploy/.env e rode 'restart'
============================================================
EOF
exit 0
fi

cat <<EOF

============================================================
 ✔ Servidor lgrok instalado e rodando!
============================================================

1) Crie estes 2 registros no DNS do domínio:

     A    lgrok.$DOMAIN   ->  $IP    (FIXO: distribuição do CLI e conexão dos clientes)
     A    *.$DOMAIN       ->  $IP    (túneis dinâmicos: abc.$DOMAIN, xyz.$DOMAIN, ...)

   (Cloudflare: deixe como "DNS only" / nuvem cinza)

2) Assim que o DNS propagar, seus clientes instalam o CLI (o token já vai
   embutido — eles não precisam dele) com uma linha:

     macOS/Linux:  curl -fsSL https://lgrok.$DOMAIN/download/install-client.sh | bash
     Windows:      irm https://lgrok.$DOMAIN/download/install-client.ps1 | iex

   e geram o link deles com:  lgrok http 3000

3) Painel do administrador (com a senha que você definiu agora):

     https://lgrok.$DOMAIN/admin

   Mostra os túneis ativos, requisições por túnel, e botões para bloquear
   um IP abusivo ou deletar/liberar um subdomínio.

Token dos clientes (fica embutido no instalador; guarde para referência):
  $TOKEN

Gerenciamento:
  logs:      cd $INSTALL_DIR/deploy && docker compose -f docker-compose.prod.yml logs -f
  reiniciar: cd $INSTALL_DIR/deploy && docker compose -f docker-compose.prod.yml restart
  senha admin / token: edite $INSTALL_DIR/deploy/.env e rode 'restart'
============================================================
EOF
