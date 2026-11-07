FROM golang:1.27.3-alpine AS build
WORKDIR /src
ARG MCL_SDK_VERSION
ARG MCL_PLUGIN_VERSION
ARG MCL_LEGACY_SDK=false
COPY go.mod go.sum ./
RUN if [ -z "$MCL_SDK_VERSION" ]; then go mod download; fi
COPY . .
RUN if [ -n "$MCL_SDK_VERSION" ]; then go get "github.com/manifest-configuration-language/mcl@$MCL_SDK_VERSION"; fi && go mod download
ARG TARGETOS
ARG TARGETARCH
RUN if [ "$MCL_LEGACY_SDK" = "true" ]; then \
      CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -tags='mcl_without_git_credentials mcl_without_read_files' -trimpath -ldflags='-s -w' -o /out/mcl-argocd ./cmd/mcl-argocd; \
    else \
      CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags='-s -w' -o /out/mcl-argocd ./cmd/mcl-argocd; \
    fi
RUN cp plugin/plugin.yaml /out/plugin.yaml && \
    if [ -n "$MCL_PLUGIN_VERSION" ]; then sed -i "/^spec:/a\\  version: $MCL_PLUGIN_VERSION" /out/plugin.yaml; fi

FROM quay.io/argoproj/argocd:v3.6.1 AS argocd

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=argocd /usr/local/bin/argocd-cmp-server /var/run/argocd/argocd-cmp-server
COPY --from=argocd /usr/local/bin/argocd /usr/local/bin/argocd
COPY --from=build /out/mcl-argocd /usr/local/bin/mcl-argocd
COPY --from=build /out/plugin.yaml /home/argocd/cmp-server/config/plugin.yaml
USER 999:999
ENV XDG_CACHE_HOME=/tmp/mcl-cache
ENTRYPOINT ["/var/run/argocd/argocd-cmp-server"]
