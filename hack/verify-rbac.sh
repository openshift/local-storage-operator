#!/bin/bash

tmpdir=$(mktemp -d -t config.XXXXXX)
trap "test -d $tmpdir && rm -rf $tmpdir" EXIT

echo "Backup config directory to $tmpdir first"
cp -r config $tmpdir/config

make rbac

diff -r --no-dereference $tmpdir/config config
