#!/usr/bin/env bash
# ats METHOD PATH [JSON_BODY]: one call to the ATS extension's API. The assistant's gate decides whether the
# call may go out (reads pass, queueing a task asks the user) and adds the credential.
set -euo pipefail
if [ $# -lt 2 ]; then
  echo "usage: ats METHOD PATH [JSON_BODY]" >&2
  exit 2
fi
method=$1
path=$2
shift 2
if [ $# -ge 1 ]; then
  exec curl -sS -X "$method" "${ATS_BASE:?}$path" -H 'Content-Type: application/json' --data-raw "$1"
fi
exec curl -sS -X "$method" "${ATS_BASE:?}$path"
