# Lifecycle

Lifecycle付きArtifactをCreate、Update、Delete操作としてMCLにレンダリングし、各document referenceを使ってLifecycleとManifestを対応付けます。CreateとUpdateの同一Resourceが異なる内容を持つ場合は同期を失敗させます。

| MCL Lifecycle | Argo CD出力 |
| --- | --- |
| `bootstrap` | 通常Resource、mainより前のSync Wave |
| `before.create`、`before.update` | `PreSync` |
| `after.create`、`after.update` | `PostSync` |
| `before.delete` | `PreDelete` |
| `after.delete` | `PostDelete` |
| `verify` | 既定では出力しない。`include-verify: 'true'`の場合は`PostSync` |
| `before.rollback`、`after.rollback` | 対応先がないためエラー |

`before-next-run`、`on-success`、`on-failure`を`BeforeHookCreation`、`HookSucceeded`、`HookFailed`へ変換します。MCL Hook Priorityは`argocd.argoproj.io/sync-wave`へ設定します。HookのAnnotationに既存値がある場合、MCLが出す値と一致しなければエラーです。

Hook Resourceでは`metadata.name`の代わりに`metadata.generateName`を指定できます。MCLはHook Lifecycleが付いたResourceに限って`generateName`を許可し、通常Resourceでは安定したIdentityとして`metadata.name`を要求します。Argo CDがSyncごとにJob名を生成するため、同じ`generateName` prefixのHookを繰り返し実行でき、`HookSucceeded`などのCleanup Policyで完了したJobを削除できます。

Bootstrap PriorityはBootstrap同士の順序付けに使います。プラグインは各PriorityをArgo CD Waveへそのまま加算せず、すべての通常ResourceのWaveより前になる連続したWaveへ割り当てます。通常Resourceの最小WaveがArgo CDで扱える最小値の場合など、前のWaveを確保できない入力は明確なエラーになります。

Argo CDはSync PhaseをSync Waveより先に実行します。そのため、通常Resourceとして適用する`bootstrap`は、どのWaveを指定しても`PreSync` Hookより先には実行できません。同じApplicationにBootstrap ResourceとPreSync Hookがある場合は、実行順序を保証できないため生成を失敗させます。CRDなどのBootstrap Resourceと、それに依存するHookは別Applicationに配置してください。CRDをHookにすると通常ResourceのTrackingやPruneの動作が変わるため、プラグインはCRDをHookへ変換しません。

Argo CDはHookの実行完了とSync Wave間でResource Healthを待ちます。MCL Lifecycleで`wait: false`を指定してこの待機を無効化する意味をArgo CDへ変換できないため、`bootstrap`またはHookイベントに`wait: false`が設定されている場合は生成を失敗させます。`main` Resourceの待機とSync状態はArgo CDの標準動作に従います。

Argo CDはSelective SyncでSync Hookを実行しません。`PreDelete`と`PostDelete`はApplication削除時のHookであり、通常のPruneでは実行されません。Hook Cleanupの実際の削除タイミングはArgo CDのHook動作に従います。
