FROM golang:1.27.3-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags='-s -w' -o /out/mcl-argocd ./cmd/mcl-argocd

FROM quay.io/argoproj/argocd:v3.6.1 AS argocd

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=argocd /usr/local/bin/argocd-cmp-server /var/run/argocd/argocd-cmp-server
COPY --from=build /out/mcl-argocd /usr/local/bin/mcl-argocd
COPY --from=build /src/plugin/plugin.yaml /home/argocd/cmp-server/config/plugin.yaml
USER 999:999
ENV XDG_CACHE_HOME=/tmp/mcl-cache
ENTRYPOINT ["/var/run/argocd/argocd-cmp-server"]
