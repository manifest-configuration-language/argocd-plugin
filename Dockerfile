FROM --platform=$BUILDPLATFORM golang:1.27.2-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags='-s -w' -o /out/mcl-argocd ./cmd/mcl-argocd && \
    go build -trimpath -ldflags='-s -w' -o /out/mcl-git-e2e-server ./e2e/gitserver

FROM alpine:3.22 AS git-e2e-server
RUN apk add --no-cache git
COPY --from=build /out/mcl-git-e2e-server /usr/local/bin/mcl-git-e2e-server
ENTRYPOINT ["/usr/local/bin/mcl-git-e2e-server"]

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/mcl-argocd /usr/local/bin/mcl-argocd
COPY plugin/plugin.yaml /home/argocd/cmp-server/config/plugin.yaml
USER 999:999
ENV MCL_CACHE_DIRECTORY=/tmp/mcl-cache
ENTRYPOINT ["/var/run/argocd/argocd-cmp-server"]
