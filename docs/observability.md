# OpenTelemetry

Argo CD v3.6.1のCMP ServerはOTLP/gRPCでTraceを送信し、Application ControllerからRepo Server、CMP SidecarまでのTrace Contextを伝搬します。CMP Sidecarは`argocd-cmd-params-cm`を読み込まないため、Collectorの設定はSidecarの環境変数に追加してください。

```yaml
repoServer:
  extraContainers:
    - name: mcl
      env:
        - name: ARGOCD_CMP_SERVER_OTLP_ADDRESS
          value: otel-collector.observability.svc.cluster.local:4317
        - name: ARGOCD_CMP_SERVER_OTLP_INSECURE
          value: "false"
        - name: ARGOCD_CMP_SERVER_OTLP_SAMPLE_RATIO
          value: "0.1"
```

TLS接続ではCollectorのCAをCMP Sidecarへマウントし、`OTEL_EXPORTER_OTLP_CERTIFICATE`にCAファイルのパスを設定してください。クライアント証明書や認証ヘッダーが必要な場合は、Kubernetes Secretから環境変数とファイルを注入します。認証情報をHelm Valuesへ直接記述しないでください。

同じTraceのサンプリングを揃えるには、Application Controller、Repo Server、CMP Serverに同じサンプリング率を設定してください。OTLPの環境変数、TLS、ヘッダー設定の詳細は[Argo CD Observability](https://argo-cd.readthedocs.io/en/release-3.6/operator-manual/observability/)を参照してください。
