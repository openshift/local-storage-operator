#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

tmpdir=$(mktemp -d -t api.XXXXXX)
trap "test -d $tmpdir && rm -rf $tmpdir" EXIT

echo "Backup api directory to $tmpdir first"
cp -r api $tmpdir/api

make generate

diff -r --no-dereference $tmpdir/api api
