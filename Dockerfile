FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags='-s -w' -o /out/mcl-argocd ./cmd/mcl-argocd

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/mcl-argocd /usr/local/bin/mcl-argocd
COPY plugin/plugin.yaml /home/argocd/cmp-server/config/plugin.yaml
USER 999:999
ENV MCL_CACHE_DIRECTORY=/tmp/mcl-cache
ENTRYPOINT ["/var/run/argocd/argocd-cmp-server"]
