#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

vendor/k8s.io/code-generator/generate-groups.sh \
deepcopy \
github.com/openshift/local-storage-operator/pkg/generated \
github.com/openshift/local-storage-operator/pkg/apis \
local:v1 \
--go-header-file "./tmp/codegen/boilerplate.go.txt"
