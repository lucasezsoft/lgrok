# lgrok

Túnel HTTP auto-hospedado: gere uma **URL pública** para uma aplicação que roda
na sua máquina, direto do terminal.

```
$ lgrok http 3000
lgrok: forwarding https://meuapp.suaempresa.com -> 127.0.0.1:3000
```

Sem dashboard, sem banco de dados, sem mensalidade: um CLI para Mac/Windows/Linux,
um servidor que roda em qualquer VPS com Docker, e o seu próprio domínio.

---

## 🧑‍💻 Sou cliente — quero gerar meus links

### 1. Instale o CLI (uma linha)

Troque `lgrok.suaempresa.com` pelo endereço que o administrador te passou.

**macOS / Linux** (terminal):
```bash
curl -fsSL https://lgrok.suaempresa.com/download/install-client.sh | bash
```

**Windows** (PowerShell):
```powershell
irm https://lgrok.suaempresa.com/download/install-client.ps1 | iex
```

### 2. Gere seu link

```bash
lgrok http 3000
```

Na **primeira vez**, ele faz 2 perguntas e nunca mais:

```
Subdomínio desejado (ex.: meuapp.suaempresa.com — vazio = aleatório): meuapp
Senha do subdomínio (criada na 1ª vez, exigida nas seguintes): ********
lgrok: configuração salva em ~/.lgrok.json — da próxima vez rode só: lgrok http 3000
lgrok: forwarding https://meuapp.suaempresa.com -> 127.0.0.1:3000
```

Você **não precisa de token**: o instalador já embute o acesso da empresa quando
você baixa o CLI do domínio dela. Basta instalar e usar.

A senha **trava o subdomínio para você**: ela fica registrada no servidor (como
hash) e, a partir daí, só quem tem a senha consegue subir `meuapp.suaempresa.com`.
Tudo fica salvo em `~/.lgrok.json` na sua máquina — nas próximas vezes é só
`lgrok http 3000`, sem perguntas.

Dicas:
- Pode digitar só o subdomínio (`meuapp`) ou o endereço inteiro
  (`meuapp.suaempresa.com`) — o lgrok extrai só o subdomínio automaticamente.
- Flags `--subdomain`, `--secret` e `--server` sobrescrevem a config salva
  (útil para um segundo subdomínio ou para scripts/CI).
- Caiu a internet? O CLI reconecta sozinho e **mantém a mesma URL**.
- Funciona com WebSocket e SSE (streaming) normalmente.
- Para recomeçar do zero, apague o `~/.lgrok.json`.

### Atualização automática

Toda vez que você roda `lgrok http ...`, ele confere a versão do servidor
(`/health`). Se o servidor foi atualizado, ele avisa e pergunta *"Atualizar
agora? [Enter]"* — apertando Enter, baixa a versão nova, se reinstala e reconecta
sozinho. Para forçar a qualquer momento: `lgrok update`. Ver a versão: `lgrok
version`.

---

## 🏢 Sou a empresa — quero montar meu servidor

### 1. Crie uma VPS

Qualquer VPS Ubuntu serve — na DigitalOcean, o menor droplet (1 vCPU / 512 MB) dá
conta. Só precisa das portas **80 e 443** abertas.

### 2. Rode o instalador (uma linha, na VPS)

```bash
curl -fsSL https://raw.githubusercontent.com/lucasezsoft/lgrok/main/install.sh | sudo bash -s -- \
  --domain suaempresa.com --email voce@suaempresa.com
```

Durante a instalação ele pede uma **senha de administrador** (para o painel
`/admin`). No final, imprime os registros DNS para criar e os links de instalação
do CLI. Rodar de novo = atualizar. Sem flags, ele pergunta tudo interativamente
(domínio, e-mail e senha de admin).

### 3. Aponte o DNS (2 registros)

No painel do seu provedor de DNS, aponte para o IP da VPS:

| Tipo | Nome    | Valor       | Para quê                                          |
|------|---------|-------------|---------------------------------------------------|
| A    | `lgrok` | IP da VPS   | endereço fixo: downloads do CLI + conexão dos clientes |
| A    | `*`     | IP da VPS   | os links gerados (`abc.suaempresa.com`, ...)      |

> O wildcard `*` só responde por subdomínios **sem registro próprio** — o `www`,
> `app` e outros que você já usa continuam intocados. Se preferir isolamento total,
> use um domínio dedicado só para túneis.

Pronto. Quando o DNS propagar, `https://lgrok.suaempresa.com` já mostra a página
com os comandos de instalação para os seus clientes.

### E o SSL/HTTPS? Automático — Cloudflare NÃO é obrigatória

O servidor emite certificados **Let's Encrypt** sozinho (via
[Caddy](https://caddyserver.com)), com **qualquer provedor de DNS** — Registro.br,
GoDaddy, DigitalOcean DNS, qualquer um. Você não configura nada além dos 2
registros acima. Cada subdomínio ganha seu certificado automaticamente no primeiro
acesso (~2s só nessa primeira vez).

O Caddy do modo padrão é a **imagem oficial** (`caddy:2`), que o Docker só baixa —
nada é compilado no servidor. O `lgrokd` compila com dependências *vendoradas*
(`vendor/`), então também não depende de `proxy.golang.org`: a instalação funciona
em VPS com rede restrita ao proxy de módulos do Go.

**Opcional — se o seu DNS estiver na Cloudflare:** passe `--cf-token SEU_TOKEN` no
instalador (token criado em *My Profile → API Tokens → Edit zone DNS*) e ele emite
**um único certificado wildcard** que cobre todos os subdomínios de uma vez: sem
espera no primeiro acesso e sem o limite do Let's Encrypt de 50 certificados
novos/semana. Na Cloudflare, deixe os registros como "DNS only" (nuvem cinza).

> Esse modo **compila** o Caddy com o plugin de DNS (via xcaddy), o que exige que o
> servidor alcance `proxy.golang.org` durante o build. Se o build falhar por rede,
> use o modo padrão (on-demand) — ele funciona igual com DNS na Cloudflare, só emite
> um certificado por subdomínio em vez de um wildcard.

### A VPS já tem nginx/apache rodando outros sites?

O lgrok não precisa das portas 80/443 para si. Instale no modo **atrás do nginx** —
o `lgrokd` sobe só em `127.0.0.1:8080` e o seu nginx repassa o tráfego:

```bash
curl -fsSL https://raw.githubusercontent.com/lucasezsoft/lgrok/main/install.sh | sudo bash -s -- \
  --behind-nginx --domain seudominio.com --email voce@exemplo.com
```

Seus sites atuais continuam intactos: o nginx casa `server_name` **exato** antes do
wildcard, então só os subdomínios que ainda não existem chegam ao lgrok. Ainda
assim, use um **domínio dedicado** para os túneis (ex.: `uberlandia.dev.br`) e não
o mesmo dos seus sites — assim nenhum cliente consegue reservar um nome que colida
com produção.

O instalador **gera** `/etc/nginx/sites-available/lgrok` mas **não ativa nada** —
mexer num nginx com produção é decisão sua. Faltam 3 passos manuais que ele imprime:

1. **DNS**: `A lgrok → IP` e `A * → IP`.
2. **Certificado wildcard** — o nginx termina o TLS, e wildcard exige validação por
   DNS (DNS-01). Com Cloudflare:

```bash
apt-get install -y python3-certbot-dns-cloudflare
printf 'dns_cloudflare_api_token = SEU_TOKEN\n' > /root/.cloudflare.ini
chmod 600 /root/.cloudflare.ini
certbot certonly --dns-cloudflare \
  --dns-cloudflare-credentials /root/.cloudflare.ini \
  --dns-cloudflare-propagation-seconds 30 \
  -d 'seudominio.com' -d '*.seudominio.com' \
  -m voce@exemplo.com --agree-tos --non-interactive \
  --deploy-hook "systemctl reload nginx"
```

   O `--deploy-hook` é essencial: sem ele o certbot renova a cada 90 dias mas o
   nginx segue servindo o certificado antigo. Outros provedores: troque o plugin
   (`certbot-dns-route53`, `certbot-dns-digitalocean`, ...).

3. **Ativar o site**:

```bash
cat /etc/nginx/sites-available/lgrok       # revise antes
ln -s /etc/nginx/sites-available/lgrok /etc/nginx/sites-enabled/lgrok
nginx -t && systemctl reload nginx
```

Gerenciamento nesse modo usa `docker-compose.behind-proxy.yml` no lugar de
`docker-compose.prod.yml`.

### Gerenciar o servidor

```bash
cd /opt/lgrok/deploy
docker compose -f docker-compose.prod.yml logs -f     # ver túneis abrindo/fechando
docker compose -f docker-compose.prod.yml restart     # reiniciar
curl -s https://lgrok.suaempresa.com                  # status (nº de túneis ativos)
```

Para trocar a senha de admin ou o token dos clientes: edite
`/opt/lgrok/deploy/.env` e rode `restart`.

### Painel do administrador — `https://lgrok.suaempresa.com/admin`

Protegido pela senha definida na instalação (usuário: qualquer, senha: a de
admin). Uma página só, sem banco de dados, que mostra:

- **túneis ativos** — subdomínio, IP de origem e **quantas requisições** cada um
  recebeu (contador em memória, leve, zera quando o túnel fecha);
- **bloquear IP** — corta na hora todos os túneis daquele IP e recusa novas
  conexões dele (o bloqueio persiste);
- **deletar subdomínio** — derruba o túnel e **libera o nome** para outro usuário
  reservar com uma senha nova.

Serve para ver quem está abusando e agir com um clique. Atualize a página para os
números mais recentes.

---

## 🔬 Testar tudo local (sem domínio, só Docker)

```bash
git clone https://github.com/lucasezsoft/lgrok.git && cd lgrok
docker compose up -d --build      # servidor local na porta 8080
make dist                         # compila os CLIs em ./dist

./dist/lgrok-darwin-arm64 http 3000 --server http://lgrok.lvh.me:8080 --token dev-token --subdomain meuapp
curl http://meuapp.lvh.me:8080    # sua porta 3000, via "URL pública"
```

> `lvh.me` resolve qualquer subdomínio para `127.0.0.1` — simula o DNS wildcard
> sem configurar nada.

## ⚙️ Como funciona

```
┌──────────┐  HTTPS  ┌────────────────────┐   1 conexão persistente   ┌──────────┐
│ visitante│ ──────> │ SERVIDOR (VPS)     │ <════════════════════════ │ SEU PC   │
│          │         │ Caddy ──> lgrokd   │   multiplexada (yamux)    │ lgrok CLI│
└──────────┘         │ roteia pelo Host   │                           │  └> :3000│
  abc.empresa.com    └────────────────────┘                           └──────────┘
```

Sua máquina não aceita conexões de fora (NAT/firewall), então o CLI **inverte a
direção**: abre uma única conexão de saída para o servidor e mantém. Cada
requisição que chega em `abc.suaempresa.com` é casada pelo header `Host` e viaja
por essa conexão até o CLI, que entrega na porta local — a arquitetura clássica
de túnel reverso, em ~500 linhas de Go ([cliente](cmd/lgrok/main.go) e
[servidor](cmd/lgrokd/main.go)). O subdomínio `lgrok` é reservado ao sistema —
abrir `https://lgrok.suaempresa.com` no navegador redireciona para este
repositório (com `utm_source` da instalação de origem).

```
cmd/lgrok/       CLI (Mac/Windows/Linux)
cmd/lgrokd/      servidor de túneis + painel /admin
deploy/          produção: Docker Compose + Caddy (TLS automático)
install.sh       instalador do servidor (1 comando na VPS)
install-client.* instaladores do CLI (servidos pelo próprio servidor)
```

O estado do servidor (senha de admin, reservas de subdomínio e IPs bloqueados) fica
num único `state.json` dentro do volume Docker `lgrokd_data`, sobrevivendo a
restarts e updates.

## Segurança e limitações

- **Token da empresa**: separa clientes autorizados de estranhos. Fica embutido no
  instalador do CLI (o cliente final nunca o vê) e trafega sempre dentro do TLS.
  Um token por empresa — se vazar, o admin troca no `.env` e reinstala os clientes.
- **Subdomínios com dono**: o primeiro cliente que sobe um subdomínio com senha o
  reserva; o servidor guarda só o hash (com salt) e as senhas nunca aparecem em
  logs.
- **Senhas locais**: o `~/.lgrok.json` na máquina do cliente guarda token e senha em
  claro (permissão 0600) — é o padrão de CLIs; no servidor, só o hash.
- Apenas túneis **HTTP/HTTPS/WebSocket** — TCP puro (banco, SSH) fica de fora.
