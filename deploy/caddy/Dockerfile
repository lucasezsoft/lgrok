# Caddy com o plugin de DNS da Cloudflare (para certificado wildcard via DNS-01)
FROM caddy:2-builder AS build
RUN xcaddy build --with github.com/caddy-dns/cloudflare

FROM caddy:2
COPY --from=build /usr/bin/caddy /usr/bin/caddy
