# Build do servidor (lgrokd) + tudo que ele distribui em /download/:
# binários do CLI, install.sh e o código-fonte (lgrok-src.tar.gz).
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /lgrokd ./cmd/lgrokd && \
    CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build -trimpath -ldflags "-s -w" -o /dist/lgrok-darwin-arm64      ./cmd/lgrok && \
    CGO_ENABLED=0 GOOS=darwin  GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o /dist/lgrok-darwin-amd64      ./cmd/lgrok && \
    CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o /dist/lgrok-windows-amd64.exe ./cmd/lgrok && \
    CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o /dist/lgrok-linux-amd64       ./cmd/lgrok && \
    cp install.sh install-client.sh install-client.ps1 /dist/ && \
    tar czf /dist/lgrok-src.tar.gz -C / src

FROM alpine:3.21
COPY --from=build /lgrokd /usr/local/bin/lgrokd
COPY --from=build /dist /srv/dist
EXPOSE 8080
ENTRYPOINT ["lgrokd"]
