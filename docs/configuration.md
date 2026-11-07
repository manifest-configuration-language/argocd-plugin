# 設定

CMP Plugin名は`mcl`です。Applicationのsourceには`plugin: {}`を設定するか、`plugin.name: mcl`を指定します。`main.mcl`を検出条件に使い、再帰検索は行いません。

以下のPlugin Parametersを指定できます。

| 名前 | 形式 | 既定値 | 意味 |
| --- | --- | --- | --- |
| `entrypoint` | string | `main.mcl` | 評価するMCLファイル |
| `parameter-files` | array | 空 | 読み込むJSONオブジェクトファイル |
| `parameters` | string | `{}` | MCLへ渡すJSONオブジェクト |
| `allow-empty` | string | `false` | 空Manifestを許可 |
| `include-verify` | string | `false` | `verify`イベントをPostSync Hookに含める |

Parameter Announcementの既定値はArgo CDから実行時パラメータとして渡らないため、プラグインが既定値を適用します。ファイルパスは相対パスに限定し、Symlinkを解決した後にソースディレクトリ内であることを検証します。重複キー、未知のPlugin Parameter、無効なJSONはエラーです。

Argo CDの標準Sync Option、Diff、Resource Tracking、Health、Prune、Sync RetryはArgo CD側で設定します。プラグインはKubernetes APIを呼び出しません。
