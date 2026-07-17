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

# Sempre atualiza server + token, preservando subdominio/senha/auto de uma
# config existente. Sem perguntas: a 1a execucao ja sobe um link temporario;
# dominio proprio depois: lgrok http <porta> --config
$cfg = Join-Path $env:USERPROFILE ".lgrok.json"
$token = "__LGROK_TOKEN__"
if (Test-Path $cfg) {
    try { $c = Get-Content $cfg -Raw | ConvertFrom-Json } catch { $c = [pscustomobject]@{} }
} else { $c = [pscustomobject]@{} }
$c | Add-Member -NotePropertyName server -NotePropertyValue $server -Force
$c | Add-Member -NotePropertyName token  -NotePropertyValue $token  -Force
$c | ConvertTo-Json | Set-Content -Encoding UTF8 $cfg

Write-Host ""
Write-Host "OK lgrok instalado em $dir\lgrok.exe"
Write-Host ""
Write-Host "Agora e so rodar (com sua aplicacao no ar, ex.: porta 3000):"
Write-Host ""
Write-Host "  lgrok http 3000"
Write-Host ""
Write-Host "Na 1a vez ele ja sobe com um link temporario. Para um dominio proprio"
Write-Host "e fixo (com senha): lgrok http 3000 --config"
