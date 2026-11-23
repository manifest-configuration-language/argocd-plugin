# 設定

CMP Plugin名は`mcl`です。Applicationのsourceには`plugin: {}`を設定するか、`plugin.name: mcl`を指定します。`main.mcl`を検出条件に使い、再帰検索は行いません。

以下のPlugin Parametersを指定できます。

| 名前 | 形式 | 既定値 | 意味 |
| --- | --- | --- | --- |
| `parameter-files` | array | 空 | 読み込むJSONオブジェクトファイル |
| `parameters` | string | `{}` | MCLへ渡すJSONオブジェクト |
| `allow-empty` | string | `false` | 空Manifestを許可 |
| `include-verify` | string | `false` | `verify`イベントをPostSync Hookに含める |
| `kubernetes-version` | string | MCL既定値 | MCLの静的Kubernetes Schema検証に使うversion |

Parameter Announcementの既定値はArgo CDから実行時パラメータとして渡らないため、プラグインが既定値を適用します。ファイルパスは相対パスに限定し、Symlinkを解決した後にソースディレクトリ内であることを検証します。重複キー、未知のPlugin Parameter、無効なJSONはエラーです。

## Application Environment

Applicationの`spec.source.plugin.env`で設定した環境変数は、Argo CDが`ARGOCD_ENV_`を付けてCMPへ渡します。プラグインは接頭辞を除いた名前をMCLのparameter名として扱い、値をStringとして評価します。例えば、`name: region`と`value: west`はMCLの`param region: String`へ渡されます。環境変数と`parameters`または`parameter-files`で同じparameterを設定するとエラーになります。

ApplicationSetでは、Application templateからgeneratorの値を環境変数へ渡せます。

```yaml
spec:
  source:
    plugin:
      env:
        - name: application
          value: '{{path.basename}}'
```

MCLのparameter名は環境変数名と一致させてください。環境変数の値はJSON値として解釈せずStringとして渡します。環境変数はログに出力しませんが、MCLコードから参照できるため、信頼できるRepositoryだけで使ってください。

## 入力ファイル追跡

`mcl-argocd inputs`はMCL評価で読み込んだファイルと`parameter-files`をセミコロン区切りで出力します。このコマンドはUI Parameter Announcementではありません。出力は`argocd.argoproj.io/manifest-generate-paths`へ設定できます。Argo CDは相対パスをApplicationのsource pathから解決し、変更された入力に応じてApplicationの再調整を判断します。

Argo CDのAnnotationは`;`を区切り文字に使うため、入力ファイルのpathに`;`が含まれる場合、`mcl-argocd inputs`は曖昧なpath一覧を出力せずエラーになります。その場合はファイル名を変更してください。

Argo CD v3.6.1でCMPへ送るファイルの範囲をAnnotationに基づいて調整するには、`reposerver.plugin.use.manifest.generate.paths`を有効にします。ただし、v3.6.1のrepo-serverはAnnotationに記載された個々のファイルだけを送らず、Annotationのパスから決まる共通ディレクトリ以下をCMPへ送ります。そのため、CMPへ届くファイルはAnnotationに列挙したファイルだけには限定されません。

```yaml
configs:
  params:
    reposerver.plugin.use.manifest.generate.paths: "true"
```

各Applicationには`mcl-argocd inputs`の出力を設定します。例えば、Applicationのソースディレクトリで同じPlugin Parametersを使って実行した場合、出力例は次のとおりです。

```text
main.mcl;../../cluster.mcl;../../mcl.toml;../../mcl.lock;params.json
```

この出力をApplicationのmetadataへ設定します。

```yaml
metadata:
  annotations:
    argocd.argoproj.io/manifest-generate-paths: main.mcl;../../cluster.mcl;../../mcl.toml;../../mcl.lock;params.json
```

`mcl-argocd inputs`は`main.mcl`、Parameter file、評価で実際に読み込んだimportやChartなどのファイルを列挙します。Applicationのsource path外にある入力は`../`を含む相対パスで表示されます。新しいimportや依存を追加したときは、変更後のソースを使ってコマンドを再実行し、Application Annotationを更新してください。古いAnnotationに新しい入力が含まれないと、その入力の変更時に再調整が行われず、古いManifestが残ることがあります。

Argo CDの標準Sync Option、Diff、Resource Tracking、Health、Prune、Sync RetryはArgo CD側で設定します。プラグインはKubernetes APIを呼び出しません。
