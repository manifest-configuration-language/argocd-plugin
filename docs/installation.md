# インストール

Argo CD v3.6.1のrepo-serverへCMP Sidecarを追加します。Helm Chartを使う場合はREADMEのValuesを`repoServer.extraContainers`へ設定します。Sidecarは`/var/run/argocd`と`/home/argocd/cmp-server/plugins`をrepo-serverと共有し、`/tmp`には専用emptyDirをマウントします。

コンテナはUID 999、Read-only Root Filesystem、Capabilitiesなし、`RuntimeDefault` seccompで動作します。CMP Serverはイメージ内の`/var/run/argocd/argocd-cmp-server`から起動し、設定ファイルはイメージ内の`/home/argocd/cmp-server/config/plugin.yaml`にあります。

## Application

ソースディレクトリに`main.mcl`を配置すればPlugin名の自動検出を使えます。エントリポイントを変更する場合は`entrypoint`パラメータを渡してください。`parameter-files`にはソースディレクトリ内のJSONファイル名を配列で指定し、`parameters`には型を保持したJSONオブジェクト文字列を指定します。

MCLの外部依存は`mcl.lock`に記録されたDigestを使って取得します。依存Lockを更新する変更は通常のソース変更としてレビューし、Argo CDのManifest生成中にLockを更新しません。
