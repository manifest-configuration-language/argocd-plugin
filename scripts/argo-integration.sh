#!/usr/bin/env bash
set -euo pipefail

: "${E2E_REPOSITORY:?E2E_REPOSITORY is required}"
: "${E2E_REVISION:?E2E_REVISION is required}"

namespace=argocd
application=mcl-plugin-e2e
crd=widgets.e2e.example.com
cluster_role=mcl-e2e-bootstrap-review

kubectl create namespace "$namespace" --dry-run=client -o yaml | kubectl apply -f -
kubectl apply --server-side --force-conflicts -n "$namespace" \
  -f https://raw.githubusercontent.com/argoproj/argo-cd/v3.6.1/manifests/install.yaml
kubectl wait -n "$namespace" --for=condition=Available deployment/argocd-repo-server --timeout=5m

patch_file="$(mktemp)"
trap 'rm -f "$patch_file"' EXIT
sed 's#ghcr.io/manifest-configuration-language/argocd-plugin:v1.0.0#mcl-argocd:e2e#' \
  examples/install/kustomize/argocd-repo-server-patch.yaml > "$patch_file"
kubectl patch -n "$namespace" deployment/argocd-repo-server \
  --type=strategic --patch-file "$patch_file"
kubectl rollout status -n "$namespace" deployment/argocd-repo-server --timeout=5m

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
