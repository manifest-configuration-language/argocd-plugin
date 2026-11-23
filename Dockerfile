FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine AS dependencies
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

FROM dependencies AS plugin-build
COPY cmd/mcl-argocd ./cmd/mcl-argocd
COPY internal ./internal
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags='-s -w' -o /out/mcl-argocd ./cmd/mcl-argocd

FROM dependencies AS git-e2e-build
COPY e2e/gitserver ./e2e/gitserver
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags='-s -w' -o /out/mcl-git-e2e-server ./e2e/gitserver

FROM alpine:3.22 AS git-e2e-server
RUN apk add --no-cache git
COPY --from=git-e2e-build /out/mcl-git-e2e-server /usr/local/bin/mcl-git-e2e-server
ENTRYPOINT ["/usr/local/bin/mcl-git-e2e-server"]

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=plugin-build /out/mcl-argocd /usr/local/bin/mcl-argocd
COPY plugin/plugin.yaml /home/argocd/cmp-server/config/plugin.yaml
USER 999:999
ENV MCL_CACHE_DIRECTORY=/tmp/mcl-cache
ENTRYPOINT ["/var/run/argocd/argocd-cmp-server"]
