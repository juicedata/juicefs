#!/bin/bash -e
# META1=redis META2=mysql sudo -E bash .github/scripts/command/changelog_replay.sh --case basic
# Explicit META_URL1/META_URL2 must refer to empty, disposable namespaces.
set -e -o pipefail
source .github/scripts/start_meta_engine.sh

META1=${META1:-redis}
META2=${META2:-mysql}
if [[ -z ${META_URL1:-} && -z ${META_URL2:-} ]]; then
    start_meta_engine "$META1"
    [[ $META1 == "$META2" ]] || start_meta_engine "$META2"
    META_URL1=$(get_meta_url "$META1")
    META_URL2=$(get_meta_url2 "$META2")
    create_database "$META_URL1"
    create_database "$META_URL2"
fi
[[ -n ${META_URL1:-} && -n ${META_URL2:-} ]] || { echo 'pass both META_URL1 and META_URL2'; exit 1; }
options=(--src-meta "$META_URL1" --dst-meta "$META_URL2" --juicefs "${JUICEFS:-./juicefs}")
[[ -z ${LOG_DIR:-} ]] || options+=(--log-dir "$LOG_DIR")
exec python3 .github/scripts/command/changelog_replay/run.py "${options[@]}" "$@"
