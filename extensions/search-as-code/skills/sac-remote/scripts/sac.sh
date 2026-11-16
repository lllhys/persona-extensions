#!/usr/bin/env bash
# sac METHOD PATH: one call to the search-as-code extension's API at the hub. The assistant's gate decides whether
# the call may go out (listing passes; reading session history asks the user the first time per computer, project
# and scope; restoring always asks) and adds the credential.
set -euo pipefail
if [ $# -lt 2 ]; then
  echo "usage: sac METHOD PATH" >&2
  exit 2
fi
method=$1
path=$2
exec curl -sS -X "$method" "${SAC_BASE:?}$path"
