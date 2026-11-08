# MCL SDKバージョン

Pluginイメージは、ルートの`go.mod`に固定したMCL SDKを使用します。MCL SDKの更新は通常の依存更新として行い、公開するPluginイメージは常に単一です。

MCLプロジェクトの`mcl.lock`は、評価に使うMCL SDKとToolchainのバージョンをPluginのSDKに合わせてください。Pluginはレンダリング中にLockを更新しません。不一致がある場合は、MCL側でLockを更新してからApplicationを同期してください。
