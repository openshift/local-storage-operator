#!/bin/bash -x
set -e

KUBECONFIG=${KUBECONFIG:-$HOME/.kube/config}

export TEST_WATCH_NAMESPACE=${TEST_OPERATOR_NAMESPACE:-openshift-local-storage}
export TEST_OPERATOR_NAMESPACE=${TEST_OPERATOR_NAMESPACE:-openshift-local-storage}
export TEST_LOCAL_DISK=${TEST_LOCAL_DISK:-""}

usage() {
  cat <<'EOF'
Usage: hack/test-e2e.sh [--suite <name>] [go test args...]

Run all e2e suites (default), or select one suite:
  --suite LocalVolumeDiscovery
  --suite LocalVolumeSet
  --suite LocalVolume

Examples:
  hack/test-e2e.sh --suite LocalVolumeSet
  hack/test-e2e.sh --suite localvolumeset -count=1
  hack/test-e2e.sh -run 'TestLocalStorage/LocalVolumeSet$'
EOF
}

suite=""
extra_args=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    --suite)
      if [[ -z "${2:-}" ]]; then
        echo "error: --suite requires a value" >&2
        usage
        exit 2
      fi
      suite="$2"
      shift 2
      ;;
    --suite=*)
      suite="${1#*=}"
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      extra_args+=("$1")
      shift
      ;;
  esac
done

suite_run_regex=""
if [[ -n "$suite" ]]; then
  suite_lower=$(echo "$suite" | tr '[:upper:]' '[:lower:]')
  case "$suite_lower" in
    localvolumediscovery)
      suite_name="LocalVolumeDiscovery"
      ;;
    localvolumeset)
      suite_name="LocalVolumeSet"
      ;;
    localvolume)
      suite_name="LocalVolume"
      ;;
    *)
      echo "error: unsupported suite '$suite'" >&2
      usage
      exit 2
      ;;
  esac
  suite_run_regex="TestLocalStorage/${suite_name}$"
fi

go test -timeout 0 ./test/e2e/... \
  -root="$(pwd)" \
  -kubeconfig="${KUBECONFIG}" \
  -v \
  -parallel=1 \
  ${suite_run_regex:+-run "$suite_run_regex"} \
  "${extra_args[@]}"
