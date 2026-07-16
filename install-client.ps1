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

# Pré-configura o servidor para a primeira execução (não sobrescreve config existente)
$cfg = Join-Path $env:USERPROFILE ".lgrok.json"
if (-not (Test-Path $cfg)) {
    "{`n  `"server`": `"$server`"`n}" | Set-Content -Encoding UTF8 $cfg
}

Write-Host ""
Write-Host "OK lgrok instalado em $dir\lgrok.exe"
Write-Host ""
Write-Host "Para gerar seu link publico, rode (com sua aplicacao no ar, ex.: porta 3000):"
Write-Host ""
Write-Host "  lgrok http 3000"
Write-Host ""
Write-Host "Na primeira vez ele pergunta o token da empresa, o subdominio que voce"
Write-Host "quer e uma senha que trava esse subdominio para voce. Fica tudo salvo"
Write-Host "em $cfg - nas proximas vezes e so rodar o comando."
