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

MCL v0.4.7 SDKには評価中の入力ファイルを返すAPI、Git Credential Provider API、新しいKubernetes Resource Identity APIがありません。この互換イメージでは`mcl-argocd inputs`がimportやChartなどの入力を列挙できず、Git認証連携も使えません。また、NamespaceをDestinationで補完してResourceのScopeを正規化できません。同じGroup、Kind、NameでNamespaceの省略と明示が異なる場合は生成を拒否します。v0.4.7のApplicationでは`manifest-generate-paths`でファイル転送を絞らず、Git認証が必要な依存を使わないでください。新しい入力追跡、Git認証、Resource Identity APIを使う場合は現行SDKイメージを選びます。

Helm Chartでは`repoServer.extraContainers`へToolchainごとのSidecarを追加します。各Sidecarは`/var/run/argocd`と`/home/argocd/cmp-server/plugins`を共有し、専用の`/tmp` volumeを使います。既定のMCL SDKはルート`go.mod`に固定し、追加のSDKは[Release Workflow](../.github/workflows/release.yml)のmatrixで選択します。
