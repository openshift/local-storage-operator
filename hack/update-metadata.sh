#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

# Usage:
#   ./hack/update-metadata.sh [OCP_VERSION]
#
#   OCP_VERSION is an optional argument. If no argument is provided, it defaults
#   to the version found in .channels[0].currentCSV in PACKAGE_MANIFEST.
#   This means you can run `./hack/update-metadata.sh` to update the manifests
#   using the current package version, or you can for example run
#   `./hack/update-metadata.sh 4.20` to set the package version to 4.20.
#   Both PACKAGE_MANIFEST and CSV_MANIFEST will be updated by this script.

PACKAGE_MANIFEST=config/manifests/local-storage-operator.package.yaml
CHANNEL=$(yq '.channels[0].name' ${PACKAGE_MANIFEST})
CURRENT_CSV=$(yq '.channels[0].currentCSV' ${PACKAGE_MANIFEST})
PACKAGE_NAME=$(echo ${CURRENT_CSV} | sed 's/\.v.*$//')
PACKAGE_VERSION=$(echo ${CURRENT_CSV} | sed 's/^.*\.v//')

if [ -z "${CHANNEL}" ] ||
   [ -z "${PACKAGE_NAME}" ] ||
   [ -z "${PACKAGE_VERSION}" ]; then
	echo "Failed to parse ${PACKAGE_MANIFEST}"
	exit 1
fi

CSV_MANIFEST=config/manifests/${CHANNEL}/${PACKAGE_NAME}.clusterserviceversion.yaml
METADATA_NAME=$(yq ' "" + .metadata.name' ${CSV_MANIFEST})
SKIP_RANGE=$(yq ' "" + .metadata.annotations["olm.skipRange"]' ${CSV_MANIFEST})
OLM_PROPERTIES=$(yq ' "" + .metadata.annotations["olm.properties"]' ${CSV_MANIFEST}) # sets olm.maxOpenShiftVersion
SPEC_VERSION=$(yq ' "" + .spec.version' ${CSV_MANIFEST})
ALM_STATUS_DESC=$(yq ' "" + .spec.labels.alm-status-descriptors' ${CSV_MANIFEST})
MUST_GATHER_IMAGE=$(yq ' "" + .metadata.annotations["operators.openshift.io/must-gather-image"]' ${CSV_MANIFEST})

if [ -z "${METADATA_NAME}" ] ||
   [ -z "${SKIP_RANGE}" ] ||
   [ -z "${OLM_PROPERTIES}" ] ||
   [ -z "${SPEC_VERSION}" ] ||
   [ -z "${ALM_STATUS_DESC}" ] ||
   [ -z "${MUST_GATHER_IMAGE}" ]; then
	echo "Failed to parse ${CSV_MANIFEST}"
	exit 1
fi

OCP_VERSION=${1:-${PACKAGE_VERSION}}
IFS='.' read -r MAJOR_VERSION MINOR_VERSION PATCH_VERSION <<< "${OCP_VERSION}"
PATCH_VERSION=${PATCH_VERSION:-0}
if [ "${OCP_VERSION}" != "${PACKAGE_VERSION}" ]; then
	PACKAGE_VERSION="${MAJOR_VERSION}.${MINOR_VERSION}.${PATCH_VERSION}"
fi

export NEW_CURRENT_CSV="${PACKAGE_NAME}.v${PACKAGE_VERSION}"
export NEW_METADATA_NAME="${PACKAGE_NAME}.v${PACKAGE_VERSION}"
export NEW_SKIP_RANGE=$(echo ${SKIP_RANGE} | sed "s/ <.*$/ <${PACKAGE_VERSION}/")
export NEW_OLM_PROPERTIES=$(echo "${OLM_PROPERTIES}" | jq -c 'map(if .type=="olm.maxOpenShiftVersion" then .value="'${MAJOR_VERSION}.$((MINOR_VERSION + 1))'" else . end)')
export NEW_SPEC_VERSION="${PACKAGE_VERSION}"
export NEW_ALM_STATUS_DESC="${PACKAGE_NAME}.v${PACKAGE_VERSION}"
export NEW_MUST_GATHER_IMAGE=$(echo ${MUST_GATHER_IMAGE} | sed "s/:v[0-9]*\.[0-9]*\.[0-9]*$/:v${PACKAGE_VERSION}/")

if [ -z "${NEW_METADATA_NAME}" ] ||
   [ -z "${NEW_SKIP_RANGE}" ] ||
   [ -z "${NEW_OLM_PROPERTIES}" ] ||
   [ -z "${NEW_SPEC_VERSION}" ] ||
   [ -z "${NEW_ALM_STATUS_DESC}" ] ||
   [ -z "${NEW_MUST_GATHER_IMAGE}" ]; then
	echo "Failed to generate new values for ${CSV_MANIFEST}"
	exit 1
fi

echo "Updating package manifest to ${PACKAGE_VERSION}"
yq -i '.channels[0].currentCSV = strenv(NEW_CURRENT_CSV)' ${PACKAGE_MANIFEST}

echo "Updating OLM metadata to ${PACKAGE_VERSION}"
yq -i '
  .metadata.name = strenv(NEW_METADATA_NAME) |
  .metadata.annotations["olm.skipRange"] = strenv(NEW_SKIP_RANGE) |
  .metadata.annotations["olm.properties"] = strenv(NEW_OLM_PROPERTIES) |
  .metadata.annotations["operators.openshift.io/must-gather-image"] = strenv(NEW_MUST_GATHER_IMAGE) |
  .spec.version = strenv(NEW_SPEC_VERSION) |
  .spec.labels.alm-status-descriptors = strenv(NEW_ALM_STATUS_DESC)
' ${CSV_MANIFEST}

