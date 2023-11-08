#!/bin/sh

# A hackish script to build bundle and index images for the given images.
# The output is available in opm-bundle directory.

set -o nounset
set -o pipefail

if [ "$#" -ne "4" ]; then
    echo "Usage: $0 <input_operator_image> <input_diskmaker_image> <output_bundle_image> <output_index_image>"
    exit 1
fi

DEFAULT_TOOL_BIN=$(which podman 2>/dev/null || which docker 2>/dev/null)
if [ "$?" -ne "0" ]; then
	echo "Error: No suitable container manipulation tool (podman, docker) found in \$PATH" 1>&2
	exit 1
fi
TOOL_BIN=${TOOL_BIN:-$DEFAULT_TOOL_BIN}

OPM_BIN=$(which opm 2>/dev/null)
if [ "$?" -ne "0" ]; then
	echo "Error: opm is not found in \$PATH" 1>&2
	exit 1
fi

set -o errexit

TOOL_NAME=$(basename $TOOL_BIN)
OPERATOR_IMAGE=$1
DISKMAKER_IMAGE=$2
BUNDLE_IMAGE=$3
INDEX_IMAGE=$4

# Prepare output dir
mkdir -p opm-bundle
pushd opm-bundle
cp -r -v ../config/* .

MANIFEST=manifests/stable/local-storage-operator.clusterserviceversion.yaml

# Replace images in the manifest - error prone, needs to be in sync with image-references.
sed -i.bak -e "s~quay.io/openshift/origin-local-storage-operator:latest~$OPERATOR_IMAGE~" \
	-e "s~quay.io/openshift/origin-local-storage-diskmaker:latest~$DISKMAKER_IMAGE~" \
	$MANIFEST
rm $MANIFEST.bak

# Build the bundle and push it
$TOOL_BIN build -t $BUNDLE_IMAGE -f bundle.Dockerfile .
$TOOL_BIN push $BUNDLE_IMAGE

# Build the index image and push it
$OPM_BIN index add --bundles $BUNDLE_IMAGE --tag $INDEX_IMAGE --container-tool $TOOL_NAME
$TOOL_BIN push $INDEX_IMAGE


echo
echo --------------------
echo "Index image created"
echo "Copy following snippet to apply it to your cluster"
echo

# Show oc apply -f - <<EOF to copy-paste into shell
cat <<REAL_EOF
oc apply -f - <<EOF
apiVersion: operators.coreos.com/v1alpha1
kind: CatalogSource
metadata:
  name: local-storage
  namespace: openshift-marketplace
spec:
  sourceType: grpc
  image: $INDEX_IMAGE
EOF
REAL_EOF

echo

popd
