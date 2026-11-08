#!/usr/bin/env bash
set -euo pipefail

: "${E2E_REPOSITORY:?E2E_REPOSITORY is required}"
: "${E2E_REVISION:?E2E_REVISION is required}"

namespace=argocd
application=mcl-plugin-e2e
crd=widgets.e2e.example.com
cluster_role=mcl-e2e-bootstrap-review
plugin_image=${MCL_PLUGIN_IMAGE:-mcl-argocd:e2e}

kubectl create namespace "$namespace" --dry-run=client -o yaml | kubectl apply -f -
kubectl apply --server-side --force-conflicts -n "$namespace" \
  -f https://raw.githubusercontent.com/argoproj/argo-cd/v3.6.1/manifests/install.yaml
kubectl wait -n "$namespace" --for=condition=Available deployment/argocd-repo-server --timeout=5m

patch_file="$(mktemp)"
trap 'rm -f "$patch_file"' EXIT
sed "s#ghcr.io/manifest-configuration-language/argocd-plugin:v1.0.0#$plugin_image#" \
  examples/install/kustomize/argocd-repo-server-patch.yaml > "$patch_file"
kubectl patch -n "$namespace" deployment/argocd-repo-server \
  --type=strategic --patch-file "$patch_file"
kubectl rollout status -n "$namespace" deployment/argocd-repo-server --timeout=5m

wait_for_sync_operation() {
  local previous_finished_at=$1
  local deadline=$((SECONDS + 300))
  local operation_state phase finished_at
  while (( SECONDS < deadline )); do
    operation_state="$(kubectl get -n "$namespace" "application/$application" -o json)"
    phase="$(jq -r '.status.operationState.phase // empty' <<<"$operation_state")"
    finished_at="$(jq -r '.status.operationState.finishedAt // empty' <<<"$operation_state")"
    if [[ -n "$finished_at" && "$finished_at" != "$previous_finished_at" ]]; then
      if [[ "$phase" != "Succeeded" ]]; then
        jq -r '.status.operationState.message // "sync operation failed"' <<<"$operation_state" >&2
        return 1
      fi
      return 0
    fi
    sleep 2
  done
  printf 'timed out waiting for the new sync operation to finish\n' >&2
  return 1
}

cat <<EOF | kubectl apply -f -
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: $application
  namespace: $namespace
  finalizers:
    - resources-finalizer.argocd.argoproj.io
spec:
  project: default
  source:
    repoURL: https://github.com/$E2E_REPOSITORY.git
    targetRevision: $E2E_REVISION
    path: examples/e2e/bootstrap
    plugin:
      name: mcl
  destination:
    server: https://kubernetes.default.svc
    namespace: default
  syncPolicy:
    automated:
      prune: true
EOF

kubectl wait -n "$namespace" --for='jsonpath={.status.sync.status}=Synced' \
  "application/$application" --timeout=5m
kubectl wait -n "$namespace" --for='jsonpath={.status.health.status}=Healthy' \
  "application/$application" --timeout=5m

role_tracking="$(kubectl get clusterrole "$cluster_role" \
  -o 'jsonpath={.metadata.annotations.argocd\.argoproj\.io/tracking-id}')"
if [[ "$role_tracking" != "$application:rbac.authorization.k8s.io/ClusterRole:default/$cluster_role" ]]; then
  printf 'unexpected ClusterRole tracking annotation: %s\n' "$role_tracking" >&2
  exit 1
fi

kubectl delete -n "$namespace" "application/$application" --wait=true --timeout=3m
if kubectl get clusterrole "$cluster_role" >/dev/null 2>&1; then
  printf 'Argo CD did not delete the tracked ClusterRole\n' >&2
  exit 1
fi
kubectl get crd "$crd" >/dev/null
remaining_widgets="$(kubectl get widgets.e2e.example.com --all-namespaces \
  -o 'jsonpath={.items[*].metadata.name}')"
if [[ -n "$remaining_widgets" ]]; then
  printf 'Argo CD left custom resources after deleting the Application: %s\n' "$remaining_widgets" >&2
  exit 1
fi
kubectl delete crd "$crd" --wait=true

cat <<EOF | kubectl apply -f -
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: $application
  namespace: $namespace
  finalizers:
    - resources-finalizer.argocd.argoproj.io
spec:
  project: default
  sources:
    - repoURL: https://github.com/$E2E_REPOSITORY.git
      targetRevision: $E2E_REVISION
      path: examples/multi-source
      plugin:
        name: mcl
    - repoURL: https://github.com/$E2E_REPOSITORY.git
      targetRevision: $E2E_REVISION
      path: examples/helm/chart
  destination:
    server: https://kubernetes.default.svc
    namespace: default
  syncPolicy:
    automated:
      prune: true
EOF

kubectl wait -n "$namespace" --for='jsonpath={.status.sync.status}=Synced' \
  "application/$application" --timeout=5m
kubectl wait -n default --for=create configmap/mcl-multi-source --timeout=5m
kubectl wait -n default --for=create deployment/mcl-plugin-e2e --timeout=5m
if ! kubectl get -n "$namespace" "application/$application" -o json |
  jq -e --arg hook_name "${application}-migration" \
    '.status.operationState.syncResult.resources[] | select(.kind == "Job" and .name == $hook_name) | .hookPhase == "Succeeded"' >/dev/null; then
  printf 'the Helm PreSync migration Hook did not succeed\n' >&2
  exit 1
fi
kubectl get -n default configmap mcl-multi-source >/dev/null
kubectl get -n default deployment "$application" >/dev/null
if kubectl get -n default job "${application}-migration" >/dev/null 2>&1; then
  printf 'the successful Helm migration Hook was not cleaned up\n' >&2
  exit 1
fi

kubectl delete -n "$namespace" "application/$application" --wait=true --timeout=3m
if kubectl get -n default configmap mcl-multi-source >/dev/null 2>&1 ||
  kubectl get -n default deployment "$application" >/dev/null 2>&1; then
  printf 'Application deletion left multi-source resources behind\n' >&2
  exit 1
fi

cat <<EOF | kubectl apply -f -
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: $application
  namespace: $namespace
  finalizers:
    - resources-finalizer.argocd.argoproj.io
spec:
  project: default
  source:
    repoURL: https://github.com/$E2E_REPOSITORY.git
    targetRevision: $E2E_REVISION
    path: examples/e2e/initial
    plugin:
      name: mcl
  destination:
    server: https://kubernetes.default.svc
    namespace: default
  syncPolicy:
    automated:
      prune: true
EOF

kubectl wait -n "$namespace" --for='jsonpath={.status.sync.status}=Synced' \
  "application/$application" --timeout=5m
kubectl wait -n default --for='jsonpath={.data.value}=before' \
  configmap/mcl-e2e-stable --timeout=5m
kubectl get -n default configmap mcl-e2e-stale >/dev/null
kubectl patch -n "$namespace" "application/$application" --type=merge \
  -p '{"spec":{"source":{"path":"examples/e2e/updated"}}}' >/dev/null
kubectl wait -n "$namespace" --for='jsonpath={.status.sync.status}=Synced' \
  "application/$application" --timeout=5m
kubectl wait -n default --for='jsonpath={.data.value}=after' \
  configmap/mcl-e2e-stable --timeout=5m
if kubectl get -n default configmap mcl-e2e-stale >/dev/null 2>&1; then
  printf 'automatic Prune left the stale resource after the source update\n' >&2
  exit 1
fi
kubectl delete -n "$namespace" "application/$application" --wait=true --timeout=3m
if kubectl get -n default configmap mcl-e2e-stable >/dev/null 2>&1; then
  printf 'Application deletion left the updated resource behind\n' >&2
  exit 1
fi

cat <<EOF | kubectl apply -f -
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: $application
  namespace: $namespace
  finalizers:
    - resources-finalizer.argocd.argoproj.io
spec:
  project: default
  source:
    repoURL: https://github.com/$E2E_REPOSITORY.git
    targetRevision: $E2E_REVISION
    path: examples/e2e/selective-before
    plugin:
      name: mcl
  destination:
    server: https://kubernetes.default.svc
    namespace: default
EOF

previous_finished_at="$(kubectl get -n "$namespace" "application/$application" \
  -o 'jsonpath={.status.operationState.finishedAt}')"
kubectl patch -n "$namespace" "application/$application" --type=merge \
  -p "{\"operation\":{\"sync\":{\"revision\":\"$E2E_REVISION\"}}}" >/dev/null
wait_for_sync_operation "$previous_finished_at"
kubectl wait -n default --for=create configmap/mcl-e2e-selective --timeout=5m
if kubectl get -n default job mcl-e2e-selective-hook >/dev/null 2>&1; then
  printf 'the initial full sync did not clean up its successful Hook\n' >&2
  exit 1
fi

previous_finished_at="$(kubectl get -n "$namespace" "application/$application" \
  -o 'jsonpath={.status.operationState.finishedAt}')"
kubectl patch -n "$namespace" "application/$application" --type=merge \
  -p "{\"spec\":{\"source\":{\"path\":\"examples/e2e/selective-after\"}},\"operation\":{\"sync\":{\"revision\":\"$E2E_REVISION\",\"resources\":[{\"group\":\"\",\"kind\":\"ConfigMap\",\"name\":\"mcl-e2e-selective\",\"namespace\":\"default\"}]}}}" >/dev/null
wait_for_sync_operation "$previous_finished_at"
kubectl wait -n default --for='jsonpath={.data.value}=after' \
  configmap/mcl-e2e-selective --timeout=5m
if ! kubectl get -n "$namespace" "application/$application" -o json |
  jq -e '.status.operationState.syncResult.resources | any(.kind == "ConfigMap" and .name == "mcl-e2e-selective") and all(.kind != "Job")' >/dev/null; then
  printf 'Selective Sync did not limit the operation to the selected ConfigMap\n' >&2
  exit 1
fi
if kubectl get -n default job mcl-e2e-selective-hook >/dev/null 2>&1; then
  printf 'Selective Sync unexpectedly executed the Hook\n' >&2
  exit 1
fi
kubectl delete -n "$namespace" "application/$application" --wait=true --timeout=3m
if kubectl get -n default configmap mcl-e2e-selective >/dev/null 2>&1; then
  printf 'Application deletion left the selective-sync resource behind\n' >&2
  exit 1
fi

cat <<EOF | kubectl apply -f -
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: $application
  namespace: $namespace
  finalizers:
    - resources-finalizer.argocd.argoproj.io
spec:
  project: default
  source:
    repoURL: https://github.com/$E2E_REPOSITORY.git
    targetRevision: $E2E_REVISION
    path: examples/e2e/delete
    plugin:
      name: mcl
  destination:
    server: https://kubernetes.default.svc
    namespace: default
EOF

previous_finished_at="$(kubectl get -n "$namespace" "application/$application" \
  -o 'jsonpath={.status.operationState.finishedAt}')"
kubectl patch -n "$namespace" "application/$application" --type=merge \
  -p "{\"operation\":{\"sync\":{\"revision\":\"$E2E_REVISION\"}}}" >/dev/null
wait_for_sync_operation "$previous_finished_at"
kubectl wait -n default --for=create configmap/mcl-e2e-stable --timeout=5m
kubectl delete -n "$namespace" "application/$application" --wait=true --timeout=3m
for hook in mcl-e2e-before-delete mcl-e2e-after-delete; do
  kubectl wait -n default --for=condition=complete "job/$hook" --timeout=5m
done
if kubectl get -n default configmap mcl-e2e-stable >/dev/null 2>&1; then
  printf 'PreDelete/PostDelete sync left the managed resource behind\n' >&2
  exit 1
fi
kubectl delete -n default job mcl-e2e-before-delete mcl-e2e-after-delete --wait=true

cat <<EOF | kubectl apply -f -
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: $application
  namespace: $namespace
spec:
  project: default
  source:
    repoURL: https://github.com/$E2E_REPOSITORY.git
    targetRevision: $E2E_REVISION
    path: examples/e2e/generated-name-hook
    plugin:
      name: mcl
  destination:
    server: https://kubernetes.default.svc
    namespace: default
EOF

previous_finished_at="$(kubectl get -n "$namespace" "application/$application" \
  -o 'jsonpath={.status.operationState.finishedAt}')"
kubectl patch -n "$namespace" "application/$application" --type=merge \
  -p "{\"operation\":{\"sync\":{\"revision\":\"$E2E_REVISION\"}}}" >/dev/null
wait_for_sync_operation "$previous_finished_at"
first_generated_job="$(kubectl get -n "$namespace" "application/$application" -o json |
  jq -r '[.status.operationState.syncResult.resources[] | select(.kind == "Job" and .hookPhase == "Succeeded" and (.name | startswith("mcl-e2e-generated-hook-"))) | .name] | if length == 1 then .[0] else empty end')"
if [[ -z "$first_generated_job" ]]; then
  printf 'the first generateName Hook did not produce exactly one successful Job\n' >&2
  exit 1
fi
if kubectl get -n default job "$first_generated_job" >/dev/null 2>&1 ||
  [[ "$(kubectl get -n default jobs -l e2e=generated-name-hook -o json | jq '.items | length')" != 0 ]]; then
  printf 'HookSucceeded did not clean up the first generated-name Job\n' >&2
  exit 1
fi

previous_finished_at="$(kubectl get -n "$namespace" "application/$application" \
  -o 'jsonpath={.status.operationState.finishedAt}')"
kubectl patch -n "$namespace" "application/$application" --type=merge \
  -p "{\"operation\":{\"sync\":{\"revision\":\"$E2E_REVISION\"}}}" >/dev/null
wait_for_sync_operation "$previous_finished_at"
second_generated_job="$(kubectl get -n "$namespace" "application/$application" -o json |
  jq -r '[.status.operationState.syncResult.resources[] | select(.kind == "Job" and .hookPhase == "Succeeded" and (.name | startswith("mcl-e2e-generated-hook-"))) | .name] | if length == 1 then .[0] else empty end')"
if [[ -z "$second_generated_job" || "$second_generated_job" == "$first_generated_job" ]]; then
  printf 'a repeated sync did not create a distinct generateName Hook Job\n' >&2
  exit 1
fi
if kubectl get -n default job "$second_generated_job" >/dev/null 2>&1 ||
  [[ "$(kubectl get -n default jobs -l e2e=generated-name-hook -o json | jq '.items | length')" != 0 ]]; then
  printf 'HookSucceeded did not clean up the repeated generated-name Job\n' >&2
  exit 1
fi
kubectl delete -n "$namespace" "application/$application" --wait=true --timeout=3m

cat <<EOF | kubectl apply -f -
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: $application
  namespace: $namespace
  finalizers:
    - resources-finalizer.argocd.argoproj.io
spec:
  project: default
  source:
    repoURL: https://github.com/$E2E_REPOSITORY.git
    targetRevision: $E2E_REVISION
    path: examples/e2e/failure-hook
    plugin:
      name: mcl
  destination:
    server: https://kubernetes.default.svc
    namespace: default
operation:
  sync:
    revision: $E2E_REVISION
EOF

kubectl wait -n "$namespace" --for='jsonpath={.status.operationState.phase}=Failed' \
  "application/$application" --timeout=5m
if kubectl get -n default job mcl-failure-hook >/dev/null 2>&1; then
  printf 'the failed MCL Hook was not cleaned up\n' >&2
  exit 1
fi
kubectl delete -n "$namespace" "application/$application" --wait=true --timeout=3m
