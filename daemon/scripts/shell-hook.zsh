# aetherd zsh integration — appended by `aetherctl init`
# Sends the last command to the daemon after each prompt via a background job.
# Zero latency impact: the socat write is fully backgrounded and disowned.

_aetherd_precmd() {
    local _exit=$?

    # Resolve socket path — honour override, fall back to XDG default.
    local _sock="${AETHERD_SOCK:-${XDG_RUNTIME_DIR:-/run/user/$(id -u)}/aetherd.sock}"

    # Skip silently if the daemon isn't running.
    [[ ! -S "$_sock" ]] && return

    # Capture the last command.  fc -ln -1 includes a leading tab on some
    # systems; strip leading whitespace.
    local _raw
    _raw=$(fc -ln -1 2>/dev/null)
    _raw="${_raw#"${_raw%%[! $'\t']*}"}"  # ltrim spaces and tabs
    [[ -z "$_raw" ]] && return

    # JSON-escape the command and cwd: backslashes first, then double-quotes.
    local _cmd="${_raw//\\/\\\\}"
    _cmd="${_cmd//\"/\\\"}"
    local _cwd="${PWD//\\/\\\\}"
    _cwd="${_cwd//\"/\\\"}"

    # EPOCHSECONDS is a zsh built-in (no subprocess).  Fall back to date(1).
    local _ts="${EPOCHSECONDS:-$(date +%s)}"

    # Fire and forget — &! disowns the job so no "done" message appears.
    {
        printf '{"method":"ingest","payload":{"cmd":"%s","exit_code":%d,"cwd":"%s","ts":%d}}\n' \
            "$_cmd" "$_exit" "$_cwd" "$_ts" \
            | socat - "UNIX-CLIENT:$_sock" >/dev/null 2>&1
    } &!
}

# Register only once — guard against sourcing the file multiple times.
if (( ! ${precmd_functions[(Ie)_aetherd_precmd]} )); then
    precmd_functions+=(_aetherd_precmd)
fi
