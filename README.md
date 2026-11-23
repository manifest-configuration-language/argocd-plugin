# MCL Argo CD Plugin

MCL Argo CD PluginはArgo CD Config Management Plugin SidecarとしてMCLプロジェクトを評価します。MCLのLockに固定された依存を取得し、MCLのLifecycleイベントをArgo CD Hook、Sync Wave、Hook Cleanup Policyに変換します。

## インストール

Argo CD Helm Chartの`repoServer.extraContainers`へSidecarを追加し、共有ソケットと独立した一時領域をマウントしてください。SidecarのUIDは999で実行します。

```yaml
repoServer:
  volumes:
    - name: mcl-tmp
      emptyDir:
        sizeLimit: 4Gi
  extraContainers:
    - name: mcl
      image: ghcr.io/manifest-configuration-language/argocd-plugin:v0.1.1
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

`mcl-argocd inputs`は`main.mcl`、指定した`parameter-files`、MCL評価で読み込んだファイルをセミコロン区切りで出力します。出力はApplicationのsource pathを基準にする`argocd.argoproj.io/manifest-generate-paths`の値として使えます。このAnnotationは変更された入力に応じたApplicationの再調整に使われます。入力一覧はUIパラメータとしてAnnouncementしません。

Argo CD v3.6.1でCMPへ送るファイルの範囲を調整する場合は、Argo CD Helm Valuesで次を有効にし、各Applicationへ`mcl-argocd inputs`の出力をAnnotationとして設定してください。

```yaml
configs:
  params:
    reposerver.plugin.use.manifest.generate.paths: "true"
```

Argo CD v3.6.1はAnnotationに記載された個々のパスだけをCMPへ送らず、パスから決まる共通ディレクトリ以下を送ります。この設定はCMPへの転送量をAnnotationのファイル一覧だけに限定しません。Annotationに記載する入力が不足すると変更時にApplicationの再調整が行われず、古いManifestが残ることがあります。MCLのimportや依存を追加した場合は、入力一覧を再生成してApplicationのAnnotationを更新してください。設定例は[設定](docs/configuration.md#入力ファイル追跡)を参照してください。

## セキュリティと制約

既定ではGit認証情報を共有せず、ファイル実行権限を保持しません。MCLのLockファイルに固定された依存だけを取得し、レンダリング中にLockを更新しません。評価には10分の上限を設けています。Git認証が必要な場合の有効化方法とアクセス範囲は[セキュリティ設定](docs/security.md#git認証)を参照してください。

イメージには`go.mod`で固定したMCL SDKを組み込みます。対応するLock形式はMCL SDKの実装が決定し、Lock生成時のMCL・Go・Helm・Kustomizeバージョンの完全一致は要求しません。Argo CD v3.6.1との統合テストを実施します。リリースworkflowはlinux/amd64とlinux/arm64のマルチアーキテクチャイメージをGHCRへ公開し、SBOM、BuildKit Provenance、GitHub Artifact Attestationを生成します。

公開済みGitHub Releaseとコンテナイメージは不変の成果物として扱います。通常のRelease Workflowは既存Releaseの再作成をスキップします。GHCR上の成果物を誤って削除した場合は、同じバージョンを自動再発行せず、元のDigestと署名を確認して手動で復旧するか、新しいバージョンをリリースしてください。

## OpenTelemetry

Argo CD v3.6.1のCMP ServerからOTLP Traceを送信する方法は[OpenTelemetry設定](docs/observability.md)を参照してください。

MCL CLIとPluginのバージョンに関する考え方は[SDKとLockの互換性](docs/mcl-versions.md)を参照してください。
