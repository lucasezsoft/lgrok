# Build do servidor (lgrokd) + tudo que ele distribui em /download/:
# binários do CLI, install.sh e o código-fonte (lgrok-src.tar.gz).
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY . .
# -mod=vendor: compila usando ./vendor, sem tocar em proxy.golang.org.
# GOFLAGS=-p=1 + GOGC baixo: serializa a compilação e coleta lixo com mais
# frequência, para caber em VPS de pouca RAM (512 MB) sem estourar (OOM).
ENV GOFLAGS="-mod=vendor -p=1" GOGC=20 CGO_ENABLED=0
RUN go build -trimpath -ldflags "-s -w" -o /lgrokd ./cmd/lgrokd && \
    GOOS=darwin  GOARCH=arm64 go build -trimpath -ldflags "-s -w" -o /dist/lgrok-darwin-arm64      ./cmd/lgrok && \
    GOOS=darwin  GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o /dist/lgrok-darwin-amd64      ./cmd/lgrok && \
    GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o /dist/lgrok-windows-amd64.exe ./cmd/lgrok && \
    GOOS=linux   GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o /dist/lgrok-linux-amd64       ./cmd/lgrok && \
    cp install.sh install-client.sh install-client.ps1 /dist/ && \
    tar czf /dist/lgrok-src.tar.gz -C / src

FROM alpine:3.21
COPY --from=build /lgrokd /usr/local/bin/lgrokd
COPY --from=build /dist /srv/dist
EXPOSE 8080
ENTRYPOINT ["lgrokd"]
