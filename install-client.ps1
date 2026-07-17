# lgrok — instalador do CLI para Windows (PowerShell).
#
#   irm __LGROK_SERVER__/download/install-client.ps1 | iex
#
# O servidor substitui __LGROK_SERVER__ pelo endereço real ao servir este
# arquivo, então o cliente final não configura nada.
$ErrorActionPreference = "Stop"

$server = "__LGROK_SERVER__"
$dir = Join-Path $env:LOCALAPPDATA "lgrok"
New-Item -ItemType Directory -Force -Path $dir | Out-Null

Write-Host "==> Baixando lgrok.exe de $server..."
Invoke-WebRequest -UseBasicParsing "$server/download/lgrok-windows-amd64.exe" -OutFile (Join-Path $dir "lgrok.exe")

# Adiciona a pasta ao PATH do usuário, se ainda não estiver
$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($userPath -notlike "*$dir*") {
    [Environment]::SetEnvironmentVariable("Path", "$userPath;$dir", "User")
    $env:Path += ";$dir"
    Write-Host "==> Pasta $dir adicionada ao PATH (abra um novo terminal se o comando nao for encontrado)"
}

# Grava servidor + token e ja pergunta subdominio + senha, salvando tudo no
# config local. Assim as proximas execucoes sao so "lgrok http 3000".
$cfg = Join-Path $env:USERPROFILE ".lgrok.json"
$token = "__LGROK_TOKEN__"
$base = $server -replace '^https?://','' -replace '^lgrok\.',''   # ex.: uberlandia.dev.br
if (Test-Path $cfg) {
    Write-Host "==> $cfg ja existe - mantendo sua configuracao atual."
} else {
    $sub = Read-Host "Subdominio que voce quer (ex.: meuapp.$base - vazio = aleatorio)"
    $sub = (($sub.Trim().ToLower()) -split '\.')[0]   # so o 1o rotulo
    $secret = ""
    if ($sub -ne "") {
        $sec = Read-Host "Senha para travar `"$sub.$base`" (criada agora, exigida depois)" -AsSecureString
        $secret = [Runtime.InteropServices.Marshal]::PtrToStringAuto(
            [Runtime.InteropServices.Marshal]::SecureStringToBSTR($sec))
    }
    $obj = [ordered]@{ server = $server; token = $token; subdomain = $sub; secret = $secret }
    $obj | ConvertTo-Json | Set-Content -Encoding UTF8 $cfg
}

Write-Host ""
Write-Host "OK lgrok instalado em $dir\lgrok.exe"
Write-Host ""
Write-Host "Agora e so rodar (com sua aplicacao no ar, ex.: porta 3000):"
Write-Host ""
Write-Host "  lgrok http 3000"
Write-Host ""
Write-Host "Configuracao salva em $cfg."
