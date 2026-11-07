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
