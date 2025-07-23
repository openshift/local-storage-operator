#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

tmpdir=$(mktemp -d -t config.XXXXXX)
trap "test -d $tmpdir && rm -rf $tmpdir" EXIT

echo "Backup config directory to $tmpdir first"
cp -r config $tmpdir/config

make metadata

diff -r --no-dereference $tmpdir/config config
