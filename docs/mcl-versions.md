# MCL SDKバージョン

各CMP Sidecarイメージは、ビルド時に選択したMCL SDKと対応する`mcl.lock`のToolchain identityを使います。レンダリング中にMCLをダウンロードしません。

同じArgo CDで複数のMCL Toolchainを使う場合は、ToolchainごとのイメージをSidecarとして追加します。現行イメージは`mcl`として登録されます。Release WorkflowはMCL v0.4.7対応イメージへ`v1.0.0-mcl-v0.4.7`の形式でタグを付け、Argo CD上では`mcl-v0.4.7`として登録します。

```yaml
spec:
  source:
    plugin:
      name: mcl-v0.4.7
```

各Sidecarの`plugin.yaml`に設定する`spec.version`が、ApplicationのPlugin名のsuffixになります。ひとつのApplicationでは、`mcl.lock`のToolchain identityに一致するSidecarを指定してください。異なるSDKを使うApplicationは、それぞれ対応するPlugin名を指定します。

MCL v0.4.7 SDKには評価中の入力ファイルを返すAPIとGit credential provider APIがありません。この互換イメージはEntrypointと`parameter-files`をAnnouncementしますが、importやChartなど追加で読んだ入力を追跡できず、Git credential連携も使えません。v0.4.7のApplicationでは`manifest-generate-paths`でファイル転送を絞らず、Git認証が必要な依存を使わないでください。これらの機能を使う場合は現行SDKイメージを選びます。

Helm Chartでは`repoServer.extraContainers`へToolchainごとのSidecarを追加します。各Sidecarは`/var/run/argocd`と`/home/argocd/cmp-server/plugins`を共有し、専用の`/tmp` volumeを使います。既定のMCL SDKはルート`go.mod`に固定し、追加のSDKは[Release Workflow](../.github/workflows/release.yml)のmatrixで選択します。
