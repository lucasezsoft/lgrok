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

Na **primeira vez**, ele faz 3 perguntas e nunca mais:

```
Token da empresa (peça ao administrador): ********
Subdomínio desejado (ex.: meuapp.suaempresa.com — vazio = aleatório): meuapp
Senha do subdomínio (criada na 1ª vez, exigida nas seguintes): ********
lgrok: configuração salva em ~/.lgrok.json — da próxima vez rode só: lgrok http 3000
lgrok: forwarding https://meuapp.suaempresa.com -> 127.0.0.1:3000
```

A senha **trava o subdomínio para você**: ela fica registrada no servidor (como
hash) e, a partir daí, só quem tem a senha consegue subir `meuapp.suaempresa.com`.
Tudo fica salvo em `~/.lgrok.json` na sua máquina — nas próximas vezes é só
`lgrok http 3000`, sem perguntas.

Dicas:
- Flags `--subdomain`, `--secret`, `--token` e `--server` sobrescrevem a config
  salva (útil para um segundo subdomínio ou para scripts/CI).
- Caiu a internet? O CLI reconecta sozinho e **mantém a mesma URL**.
- Funciona com WebSocket e SSE (streaming) normalmente.
- Para recomeçar do zero, apague o `~/.lgrok.json`.

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

O script instala o Docker, sobe o servidor e imprime no final: os registros DNS
para criar, o token secreto dos clientes e os links de instalação do CLI. Rodar de
novo = atualizar. Sem flags, ele pergunta tudo interativamente.

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

**Opcional — se o seu DNS estiver na Cloudflare:** passe `--cf-token SEU_TOKEN` no
instalador (token criado em *My Profile → API Tokens → Edit zone DNS*) e o servidor
emite **um único certificado wildcard** que cobre todos os subdomínios de uma vez:
sem espera no primeiro acesso e sem o limite do Let's Encrypt de 50 certificados
novos/semana (que só importa se seus clientes criarem dezenas de subdomínios
aleatórios novos toda semana). É um upgrade, não uma dependência. Na Cloudflare,
deixe os registros como "DNS only" (nuvem cinza).

### Gerenciar o servidor

```bash
cd /opt/lgrok/deploy
docker compose -f docker-compose.prod.yml logs -f     # ver túneis abrindo/fechando
docker compose -f docker-compose.prod.yml restart     # reiniciar
curl -s https://lgrok.suaempresa.com                  # status (nº de túneis ativos)
```

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
[servidor](cmd/lgrokd/main.go)). O subdomínio `lgrok` é reservado ao sistema.

```
cmd/lgrok/       CLI (Mac/Windows/Linux)
cmd/lgrokd/      servidor de túneis
deploy/          produção: Docker Compose + Caddy (TLS automático)
install.sh       instalador do servidor (1 comando na VPS)
install-client.* instaladores do CLI (servidos pelo próprio servidor)
```

## Segurança e limitações

- **Token obrigatório**: sem ele, ninguém abre túnel no seu servidor. Trafega
  sempre dentro do TLS. Um token por empresa (sem contas individuais).
- **Subdomínios com dono**: o primeiro cliente que sobe um subdomínio com senha o
  reserva; o servidor guarda só o hash (com salt) e as senhas nunca aparecem em
  logs. As reservas sobrevivem a restarts/updates (volume Docker `lgrokd_data`).
- Apenas túneis **HTTP/HTTPS/WebSocket** — TCP puro (banco, SSH) fica de fora.
- Sem painel de inspeção de requisições — decisão de escopo: só o funcional.
