# MCL Argo CD Plugin

MCL Argo CD PluginはArgo CD Config Management Plugin SidecarとしてMCLプロジェクトを評価します。MCLのLockに固定された依存を取得し、MCLのLifecycleイベントをArgo CD Hook、Sync Wave、Hook Cleanup Policyに変換します。

## インストール

Argo CD Helm Chartの`repoServer.extraContainers`へSidecarを追加し、共有ソケットと独立した一時領域をマウントしてください。SidecarのUIDは999で実行します。

現時点では公開ReleaseとGHCRイメージがないため、以下の`v1.0.0`は仮値です。公開後に実際のRelease tagまたは検証済みDigestへ置き換えてください。

```yaml
repoServer:
  volumes:
    - name: mcl-tmp
      emptyDir:
        sizeLimit: 4Gi
  extraContainers:
    - name: mcl
      image: ghcr.io/manifest-configuration-language/argocd-plugin:v1.0.0
      imagePullPolicy: IfNotPresent
      securityContext:
        runAsNonRoot: true
        runAsUser: 999
        runAsGroup: 999
        readOnlyRootFilesystem: true
        allowPrivilegeEscalation: false
        capabilities:
          drop: [ALL]
        seccompProfile:
          type: RuntimeDefault
      volumeMounts:
        - name: var-files
          mountPath: /var/run/argocd
        - name: plugins
          mountPath: /home/argocd/cmp-server/plugins
        - name: mcl-tmp
          mountPath: /tmp
      env:
        - name: ARGOCD_EXEC_TIMEOUT
          value: 10m
        - name: MCL_CACHE_DIRECTORY
          value: /tmp/mcl-cache
configs:
  params:
    server.repo.server.timeout.seconds: "660"
    controller.repo.server.timeout.seconds: "660"
```

各Applicationのソースディレクトリに`main.mcl`を置きます。Applicationは`plugin: {}`で自動検出できます。

```yaml
spec:
  source:
    path: apps/example
    plugin:
      name: mcl
      parameters:
        - name: parameter-files
          array: [parameters.json]
        - name: parameters
          string: '{"namespace":"example"}'
```

`main.mcl`をEntrypointとして使用します。`parameter-files`はJSONオブジェクトを含むファイルの配列で、`parameters`はJSONオブジェクト文字列です。`allow-empty`と`include-verify`は`true`または`false`を指定します。`kubernetes-version`を指定するとMCLがそのKubernetes Schemaで検証します。複数の入力で同じキーを指定するとエラーになります。パラメータファイルはApplicationのソースディレクトリ内に限定し、Symlinkによるディレクトリ外参照も拒否します。

CMPコマンドは既定の90秒制限より長い評価時間を必要とするため、Sidecarへ`ARGOCD_EXEC_TIMEOUT=10m`を設定し、Application ControllerとRepo Serverのtimeoutを10分より長く設定してください。上の例は10分のMCL Context deadlineに対して両方を660秒にしています。値を変更する場合は3つの制限を揃えてください。

## Lifecycle変換

`bootstrap`は通常リソースとして負のSync Waveを使い、`before.create`と`before.update`は`PreSync`、`after.create`と`after.update`は`PostSync`へ変換します。Argo CDはSync PhaseをSync Waveより先に実行するため、`bootstrap`と`PreSync` Hookを同じApplicationに出力するとエラーになります。CRDなどのBootstrap Resourceと、それに依存するHookは別Applicationへ分けてください。`before.delete`と`after.delete`は`PreDelete`と`PostDelete`へ変換します。RollbackイベントはArgo CDに対応するイベントがないためエラーになります。`verify`イベントはデフォルトで生成物に含めず、`include-verify: 'true'`を指定した場合は`PostSync` Hookに変換します。空出力はPrune事故防止のためエラーになり、意図的に空出力を使う場合だけ`allow-empty: 'true'`を指定します。HookやBootstrapの`wait`を`false`にするとArgo CDの待機動作と一致しないため、生成をエラーにします。

同じリソースをCreateとUpdateで異なる内容に評価した場合や、既存のArgo CD Lifecycle AnnotationがMCLの値と矛盾する場合はエラーになります。HookのCleanupは`BeforeHookCreation`、`HookSucceeded`、`HookFailed`へ変換します。

Argo CDはSelective SyncでSync Hookを実行しません。Prune、Diff、Health、Resource Tracking、Sync OptionsはArgo CD標準の動作に従います。

## 入力ファイル追跡

`mcl-argocd inputs`は`main.mcl`、指定した`parameter-files`、MCL評価で読み込んだファイルをセミコロン区切りで出力します。出力はApplicationのsource pathを基準にする`argocd.argoproj.io/manifest-generate-paths`の値として使えます。入力一覧はUIパラメータとしてAnnouncementしません。

この転送最適化を使う場合は、Argo CD Helm Valuesで次を有効にし、各Applicationへ`mcl-argocd inputs`の出力をAnnotationとして設定してください。

```yaml
configs:
  params:
    reposerver.plugin.use.manifest.generate.paths: "true"
```

MCLのimportや依存を追加した場合は、入力一覧を再生成してApplicationのAnnotationを更新します。古い一覧では新しい入力がCMPへ転送されず、Manifest生成に失敗することがあります。設定例は[設定](docs/configuration.md#入力ファイル追跡)を参照してください。

## セキュリティと制約

既定ではGit認証情報を共有せず、ファイル実行権限を保持しません。MCLのLockファイルに固定された依存だけを取得し、レンダリング中にLockを更新しません。評価には10分の上限を設けています。Git認証が必要な場合の有効化方法とアクセス範囲は[セキュリティ設定](docs/security.md#git認証)を参照してください。

イメージはMCLの依存コミットとArgo CD v3.6.1を固定してビルドします。リリースworkflowはlinux/amd64とlinux/arm64のマルチアーキテクチャイメージをGHCRへ公開し、SBOM、BuildKit Provenance、GitHub Artifact Attestationを生成します。

## OpenTelemetry

Argo CD v3.6.1のCMP ServerからOTLP Traceを送信する方法は[OpenTelemetry設定](docs/observability.md)を参照してください。

同じArgo CDで異なるMCL Toolchainを使うSidecar構成は[MCL SDKバージョン設定](docs/mcl-versions.md)を参照してください。
