# インストール

Argo CD v3.6.1のrepo-serverへCMP Sidecarを追加します。イメージはリリースタグまたは検証済みのマルチアーキテクチャDigestで固定してください。リリースイメージはGHCRで公開し、署名、SBOM、Provenanceを添付します。

Helm Chartを使う場合は[README](../README.md)のValuesを`repoServer.extraContainers`へ設定します。Helmを使わない場合は、既存overlayの`kustomization.yaml`に次のPatchを追加し、[argocd-repo-server-patch.yaml](../examples/install/kustomize/argocd-repo-server-patch.yaml)を参照してください。

```yaml
patches:
  - path: argocd-repo-server-patch.yaml
```

Patchはrepo-server DeploymentにSidecarと専用`/tmp` volumeを追加します。SidecarはUID 999、Read-only Root Filesystem、Capabilitiesなし、`RuntimeDefault` seccompで動作します。CMP Serverはイメージ内の`/var/run/argocd/argocd-cmp-server`から起動し、設定ファイルはイメージ内の`/home/argocd/cmp-server/config/plugin.yaml`にあります。

## Application

ソースディレクトリに`main.mcl`を配置すればPlugin名の自動検出を使えます。エントリポイントを変更する場合は`entrypoint`パラメータを渡してください。`parameter-files`にはソースディレクトリ内のJSONファイル名を配列で指定し、`parameters`には型を保持したJSONオブジェクト文字列を指定します。

MCLの外部依存は`mcl.lock`に記録されたDigestを使って取得します。依存Lockを更新する変更は通常のソース変更としてレビューし、Argo CDのManifest生成中にLockを更新しません。
