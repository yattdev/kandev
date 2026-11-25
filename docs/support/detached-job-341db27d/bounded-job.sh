#!/bin/sh
# Root binds this exact script, fresh nonce, existing RW logs, and opaque payload.
# GNU coreutils timeout is required. No setup/import/reseed or retry is included.
set -u
if [ "$#" -lt 5 ]; then
    printf '%s\n' 'usage: NONCE EXISTING_LOG_DIR SECONDS KILL_GRACE COMMAND [ARGS...]' >&2
    exit 64
fi
nonce=$1
logs=$2
seconds=$3
grace=$4
shift 4
case "$nonce" in ''|*[!A-Za-z0-9_-]*) exit 64;; esac
[ "${#nonce}" -le 80 ] || exit 64
case "$seconds:$grace" in *[!0-9:]*|:*|*:) exit 64;; esac
case "$seconds:$grace" in 0*|*:0*) exit 64;; esac
[ "$seconds" -ge 1 ] && [ "$seconds" -le 1200 ] || exit 64
[ "$grace" -ge 1 ] && [ "$grace" -le 15 ] || exit 64
[ -d "$logs" ] && [ -w "$logs" ] || exit 73
umask 077
job_dir=$logs/$nonce.job
# Atomic nonce claim: a duplicate cannot replay the payload or overwrite receipts.
mkdir "$job_dir" || exit 73
started=$(date -u '+%Y-%m-%dT%H:%M:%SZ') || exit 74
printf '{"schema":"bounded-job/v1","nonce":"%s","seconds":%s,"kill_grace":%s,"started_at":"%s"}\n' \
    "$nonce" "$seconds" "$grace" "$started" > "$job_dir/.start.tmp" || exit 74
mv "$job_dir/.start.tmp" "$job_dir/start.json" || exit 74
cancel=0
child=
relay() {
    cancel=$1
    if [ -n "$child" ]; then
        kill -TERM "$child" 2>/dev/null || :
    fi
}
trap 'relay 143' TERM
trap 'relay 130' INT
trap 'relay 129' HUP
trap 'relay 131' QUIT
# Do not infer the affected image's tooling from Support's synthetic fixture.
# This inert check fails before the payload if the approved tool is absent.
/usr/bin/timeout --version > "$job_dir/timeout-version.txt" 2>&1
if ! grep -q 'GNU coreutils' "$job_dir/timeout-version.txt"; then
    payload_exit=125
elif [ "$cancel" -ne 0 ]; then
    payload_exit=$cancel
else
    /usr/bin/timeout --signal=TERM --kill-after="${grace}s" "${seconds}s" "$@" \
        > "$job_dir/stdout.log" 2> "$job_dir/stderr.log" &
    child=$!
    if [ "$cancel" -ne 0 ]; then kill -TERM "$child" 2>/dev/null || :; fi
    wait "$child"
    payload_exit=$?
    # A trapped stop may interrupt wait before timeout has reaped its child.
    if [ "$cancel" -ne 0 ] && kill -0 "$child" 2>/dev/null; then
        wait "$child"
        payload_exit=$?
    fi
fi
driver_exit=$payload_exit
cancelled=false
if [ "$cancel" -ne 0 ]; then driver_exit=$cancel; cancelled=true; fi
finished=$(date -u '+%Y-%m-%dT%H:%M:%SZ') || exit 74
printf '{"schema":"bounded-job/v1","nonce":"%s","complete":true,"payload_exit":%s,"driver_exit":%s,"cancelled":%s,"finished_at":"%s"}\n' \
    "$nonce" "$payload_exit" "$driver_exit" "$cancelled" "$finished" > "$job_dir/.exit.tmp" || exit 74
mv "$job_dir/.exit.tmp" "$job_dir/exit.json" || exit 74
exit "$driver_exit"
