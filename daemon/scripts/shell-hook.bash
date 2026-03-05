# aetherd bash integration — appended by `aetherctl init`
# Sends the last command to the daemon via PROMPT_COMMAND.
#
# Note: bash does not flush history until the session ends by default.
# We run `history -a` before reading so the entry is present.

_aetherd_precmd() {
    local _exit=$?

    local _sock="${AETHERD_SOCK:-${XDG_RUNTIME_DIR:-/run/user/$(id -u)}/aetherd.sock}"
    [[ ! -S "$_sock" ]] && return

    # Flush and read the last history entry.
    history -a 2>/dev/null
    local _raw
    _raw=$(HISTTIMEFORMAT='' history 1 2>/dev/null | sed 's/^[[:space:]]*[0-9]*[[:space:]]*//')
    [[ -z "$_raw" ]] && return

    local _cmd="${_raw//\\/\\\\}"
    _cmd="${_cmd//\"/\\\"}"
    local _cwd="${PWD//\\/\\\\}"
    _cwd="${_cwd//\"/\\\"}"
    local _ts
    _ts=$(date +%s)

    {
        printf '{"method":"ingest","payload":{"cmd":"%s","exit_code":%d,"cwd":"%s","ts":%d}}\n' \
            "$_cmd" "$_exit" "$_cwd" "$_ts" \
            | socat - "UNIX-CLIENT:$_sock" >/dev/null 2>&1
    } &

    # Discard the background job notification.
    disown 2>/dev/null || true
}

# Prepend to PROMPT_COMMAND, guarding against duplicate registration.
if [[ "$PROMPT_COMMAND" != *"_aetherd_precmd"* ]]; then
    if [[ -z "$PROMPT_COMMAND" ]]; then
        PROMPT_COMMAND="_aetherd_precmd"
    else
        PROMPT_COMMAND="_aetherd_precmd; ${PROMPT_COMMAND}"
    fi
fi
