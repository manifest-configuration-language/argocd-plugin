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

`mcl-argocd inputs`はMCL評価で読み込んだファイルと`parameter-files`をセミコロン区切りで出力します。このコマンドはUI Parameter Announcementではありません。出力は`argocd.argoproj.io/manifest-generate-paths`へ設定できます。Argo CDは相対パスをApplicationのsource pathから解決します。

```yaml
metadata:
  annotations:
    argocd.argoproj.io/manifest-generate-paths: main.mcl;../../cluster.mcl;../../mcl.toml;../../mcl.lock;params.json
```

`mcl-argocd inputs`は`main.mcl`、Parameter file、評価で実際に読み込んだimportやChartなどのファイルを列挙します。Applicationのsource path外にある入力は`../`を含む相対パスで表示されます。CMPへの転送もannotationのパスで絞られるため、入力依存が変わったときはApplication annotationを更新してください。

Argo CDの標準Sync Option、Diff、Resource Tracking、Health、Prune、Sync RetryはArgo CD側で設定します。プラグインはKubernetes APIを呼び出しません。
