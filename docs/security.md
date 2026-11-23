# セキュリティ

CMPは信頼されたRepositoryコードとして評価されます。SidecarはNon-root、Read-only Root Filesystem、Capabilitiesなしで実行します。既定ではGit認証情報の共有とGit内の実行モード維持を無効にします。

Plugin ParameterのパスをShellへ渡しません。`main.mcl`とJSONファイルはSymlink解決後もソースディレクトリ内にあることを確認します。パラメータ、Secrets、環境変数の内容はログへ出力しません。Argo CDの`spec.source.plugin.env`はMCL parameterとして参照できるため、Secretを渡す場合は信頼できるRepositoryだけに限定し、生成ManifestにSecret値を含めないでください。

MCL評価には10分のContext deadlineを設定し、ロック済み外部依存だけを取得します。Manifest生成のために依存Lockを更新しません。評価失敗、Lifecycle競合、空出力の既定状態ではManifestを返さないため、失敗した生成結果によるPruneを防ぎます。意図的な空出力は`allow-empty: 'true'`で明示します。

依存取得先のプライベートIPは既定で拒否されます。信頼できるRepositoryからプライベート依存を取得する場合は、CMP Sidecarの`MCL_DANGEROUS_ALLOWED_PRIVATE_DESTINATIONS`へ宛先を含む最小のRFC 1918 CIDRとPortを`CIDR:port`形式で設定し、社内CAが必要な場合は`MCL_EXTERNAL_CA_FILE`にPEM証明書ファイルを指定します。許可先とCAファイルはApplicationから変更できない運用者管理の設定にしてください。設定の詳細は[MCLの依存取得ネットワーク制限](https://github.com/manifest-configuration-language/mcl/blob/main/docs/dependency-network-security.md)を参照してください。

## Git認証

認証が必要なKustomize Git依存を使う場合は、CMP設定の`spec.provideGitCreds`を`true`にし、repo-serverとSidecarの両方へ同じ専用Unix socket volumeをマウントしてください。`ARGOCD_ASK_PASS_SOCK`も両コンテナで同じsocket pathに設定します。CMP設定はConfigMap等からマウントして、既定イメージ内の`plugin.yaml`を書き換えずに管理します。

```yaml
spec:
  provideGitCreds: true
```

Kubernetes Podには、例えば`emptyDir`を追加し、`/var/run/mcl-askpass`へrepo-serverとSidecarの両方からマウントします。両コンテナへ`ARGOCD_ASK_PASS_SOCK=/var/run/mcl-askpass/reposerver.sock`を設定します。Argo CDの[Git認証連携手順](https://argo-cd.readthedocs.io/en/release-3.6/operator-manual/config-management-plugins/#provide-git-credentials)では、socket用の専用volumeを使うよう推奨しています。

Argo CDはApplicationのソースRepository認証情報をCMPへ問い合わせ可能にします。このプラグインは`ARGOCD_APP_SOURCE_REPO_URL`と一致するHTTPS Git URLにだけ認証情報を返し、異なるhostまたはrepository pathへの認証情報要求は拒否します。Git認証情報は評価される信頼済みRepositoryコードから利用可能になるため、利用Repositoryを信頼できる場合に限って有効にしてください。共有socketはArgo CD公式手順に従いrepo-serverとSidecar専用にします。
