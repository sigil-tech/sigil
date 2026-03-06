#!/usr/bin/env zsh
# aether shell hook — source this from ~/.zshrc
# Added automatically by: aetherd init
#
# Sends each executed command to aetherd via Unix socket (non-blocking).
# Adds < 1ms latency to every prompt redraw.

_aetherd_precmd() {
    local _aetherd_exit=$?
    local _aetherd_cmd
    _aetherd_cmd="$(fc -ln -1 2>/dev/null)"
    _aetherd_cmd="${_aetherd_cmd##[[:space:]]}"
    [[ -z "$_aetherd_cmd" ]] && return 0

    local _aetherd_sock="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}/aetherd.sock"
    [[ ! -S "$_aetherd_sock" ]] && return 0

    # Escape cmd and cwd for embedding in JSON (replace \ then ")
    local _aetherd_cmd_json="${_aetherd_cmd//\\/\\\\}"
    _aetherd_cmd_json="${_aetherd_cmd_json//\"/\\\"}"
    local _aetherd_cwd_json="${PWD//\\/\\\\}"
    _aetherd_cwd_json="${_aetherd_cwd_json//\"/\\\"}"

    printf '{"method":"ingest","payload":{"cmd":"%s","exit_code":%d,"cwd":"%s","ts":%d}}\n' \
        "$_aetherd_cmd_json" \
        "$_aetherd_exit" \
        "$_aetherd_cwd_json" \
        "$(date +%s)" \
        | nc -U -w0 "$_aetherd_sock" 2>/dev/null &
}

# Guard against double-registration
if (( ${precmd_functions[(I)_aetherd_precmd]} == 0 )); then
    precmd_functions+=(_aetherd_precmd)
fi
