# インストール

Argo CD v3.6.1のrepo-serverへCMP Sidecarを追加します。イメージはリリースタグまたは検証済みのマルチアーキテクチャDigestで固定してください。リリースイメージはGHCRで公開し、署名、SBOM、Provenanceを添付します。

現時点では公開ReleaseとGHCRイメージがありません。READMEとKustomize Patchに記載された`v1.0.0`は仮値なので、公開後に実際のRelease tagまたは検証済みDigestへ置き換えてください。

Helm Chartを使う場合は[README](../README.md)のValuesを`repoServer.extraContainers`へ設定します。Helmを使わない場合は、既存overlayの`kustomization.yaml`に次のPatchを追加し、[argocd-repo-server-patch.yaml](../examples/install/kustomize/argocd-repo-server-patch.yaml)を参照してください。

```yaml
patches:
  - path: argocd-repo-server-patch.yaml
```

Patchはrepo-server DeploymentにSidecarとサイズ上限付きの専用`/tmp` volumeを追加します。SidecarはUID 999、Read-only Root Filesystem、Capabilitiesなし、`RuntimeDefault` seccompで動作します。Argo CD Helm Chartの`copyutil` Init Containerが`var-files` volumeへ配置する`/var/run/argocd/argocd-cmp-server`からCMP Serverを起動します。インストール時は`copyutil` Init Containerを維持してください。プラグイン設定はイメージ内の`/home/argocd/cmp-server/config/plugin.yaml`にあります。

Sidecarでは`ARGOCD_EXEC_TIMEOUT=10m`を設定し、`MCL_CACHE_DIRECTORY=/tmp/mcl-cache`を依存Cacheに使います。専用`/tmp` volumeの`sizeLimit`はCacheと一時ファイルの合計上限です。実際の依存量に合わせてサイズを調整してください。

CMPのコマンドタイムアウトは既定90秒、Application ControllerとRepo ServerのRPC timeoutは既定60秒です。MCLの評価上限10分を使うには、Helm Valuesの`configs.params`で`server.repo.server.timeout.seconds`と`controller.repo.server.timeout.seconds`を10分より長い秒数に設定してください。例として660秒を使います。Sidecar、Repo Server、Application Controllerの設定を揃えないと、MCLのContext deadline前にArgo CDが実行を終了します。

入力ファイル追跡によるCMPへの転送最適化を使う場合は、Helm Valuesの`configs.params`へ次を追加します。この設定はArgo CD全体に適用されるため、各CMP Applicationに入力ファイル一覧のAnnotationも設定してください。

```yaml
configs:
  params:
    reposerver.plugin.use.manifest.generate.paths: "true"
```

Applicationのソースディレクトリで`mcl-argocd inputs`を実行し、出力を`argocd.argoproj.io/manifest-generate-paths`へ設定します。MCLのimportや依存を追加した場合は、コマンドを再実行してAnnotationを更新してください。古い一覧に新しい入力が含まれないと、repo-serverからCMPへファイルが転送されず、Manifest生成に失敗することがあります。詳しい例は[設定](configuration.md#入力ファイル追跡)を参照してください。

## Application

ソースディレクトリに`main.mcl`を配置すればPlugin名の自動検出を使えます。プラグインは常に`main.mcl`を読み込みます。`parameter-files`にはソースディレクトリ内のJSONファイル名を配列で指定し、`parameters`には型を保持したJSONオブジェクト文字列を指定します。

MCLの外部依存は`mcl.lock`に記録されたDigestを使って取得します。依存Lockを更新する変更は通常のソース変更としてレビューし、Argo CDのManifest生成中にLockを更新しません。
