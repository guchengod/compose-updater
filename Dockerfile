ARG GO_VERSION=1.26.5
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown

ENV GOPROXY=https://goproxy.cn,direct
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath \
    -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.buildDate=${BUILD_DATE}" \
    -o /out/compose-updater ./cmd/compose-updater

# 容器只运行于 Linux；docker:cli 内含 Docker CLI 与 Compose v2 插件。
FROM docker:cli

RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.ustc.edu.cn/g' /etc/apk/repositories && \
    apk add --no-cache ca-certificates tzdata
COPY --from=build /out/compose-updater /usr/local/bin/compose-updater

USER root
EXPOSE 8080
ENTRYPOINT ["compose-updater"]
CMD ["serve", "-config", "/config/config.json"]
