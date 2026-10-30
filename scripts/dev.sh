#!/bin/sh
set -eu
project_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$project_root"
if [ -f "$project_root/work/bin/.aividcut-native-worker-built" ] && [ -x "$project_root/work/bin/aividcut-mlt-worker" ] && [ -d "$project_root/work/mlt-selected-repository" ] && [ -d "$project_root/work/mlt-profiles" ]; then
  export AIVIDCUT_MLT_WORKER="${AIVIDCUT_MLT_WORKER:-$project_root/work/bin/aividcut-mlt-worker}"
  export AIVIDCUT_MLT_REPOSITORY="${AIVIDCUT_MLT_REPOSITORY:-$project_root/work/mlt-selected-repository}"
  export AIVIDCUT_MLT_PROFILES="${AIVIDCUT_MLT_PROFILES:-$project_root/work/mlt-profiles}"
fi
mkdir -p work/bin
go build -o work/bin/aividcut-devserver ./cmd/devserver
work/bin/aividcut-devserver -listen 127.0.0.1:43110 -data work/dev-data &
aividcut_backend_pid=$!
trap 'kill "$aividcut_backend_pid" 2>/dev/null || true' EXIT INT TERM
sleep 1
if ! kill -0 "$aividcut_backend_pid" 2>/dev/null; then
  printf '后端未能启动，请检查端口43110是否已被占用。\n' >&2
  exit 1
fi
pnpm --dir frontend run dev
