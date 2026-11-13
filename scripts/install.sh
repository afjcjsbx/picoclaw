#!/usr/bin/env bash
# PicoClaw one-line installer.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/afjcjsbx/picoclaw/main/scripts/install.sh | bash
#   curl -fsSL https://raw.githubusercontent.com/afjcjsbx/picoclaw/main/scripts/install.sh | bash -s -- --non-interactive
#   bash scripts/install.sh [options]
#
# Requires Bash 3.2+ (macOS /bin/bash is fine). Windows is supported through
# Git Bash/MSYS2; WSL is detected as regular Linux. FreeBSD and NetBSD need
# bash and curl installed first.
#
# The installer intentionally uses only curl, tar/unzip and standard POSIX
# utilities. Provider API keys are shown while typing so the paste can be
# verified; other secrets are read with echo disabled. Secrets are written to
# a config file with mode 600. Release archives are verified against the
# published SHA-256 checksums when available.

set -eu
umask 077

if [ -z "${BASH_VERSION:-}" ]; then
    printf 'This installer requires bash.\n' >&2
    exit 1
fi

if [ -z "${HOME:-}" ]; then
    printf 'HOME is not set; cannot determine the install location.\n' >&2
    exit 1
fi

DEFAULT_REPO="afjcjsbx/picoclaw"
DEFAULT_INSTALL_DIR="$HOME/.local/bin"
DEFAULT_PICOCLAW_HOME="$HOME/.picoclaw"
NON_INTERACTIVE="${PICOCLAW_NON_INTERACTIVE:-0}"
MODIFY_PATH=1
FORCE_CONFIG=0
INSTALL_DIR="${PICOCLAW_INSTALL_DIR:-$DEFAULT_INSTALL_DIR}"
PICOCLAW_HOME="${PICOCLAW_HOME:-$DEFAULT_PICOCLAW_HOME}"
CONFIG_FILE="${PICOCLAW_CONFIG:-$PICOCLAW_HOME/config.json}"
WORKSPACE="${PICOCLAW_WORKSPACE:-$PICOCLAW_HOME/workspace}"
REPO="${PICOCLAW_REPO:-$DEFAULT_REPO}"
VERSION_PIN="${PICOCLAW_VERSION:-}"
LOCAL_ARCHIVE="${PICOCLAW_ARCHIVE:-}"
TMP_DIR=""
TMP_CONFIG=""
TTY_DEV=""
TUI_ACTIVE=0
TUI_STTY_STATE=""
CONFIG_ACTION="skipped"
CONFIG_TEMPLATE=""
LAUNCHER_INSTALLED=0
PATH_CONFIGURED=0
PICOCLAW_CMD=""
VERSION_LINE=""
OS=""
ARCH=""
ASSET=""
BIN_NAME=""
LAUNCHER_NAME=""
BIN_PATH=""
LAUNCHER_PATH=""
EXTRACT_DIR=""
ARCHIVE=""
TAG=""
CHOICE=""
BASE_MODE=""
KEY_MODE=""
DEFAULT_BASE=""
DEFAULT_MODEL=""
MODEL_LIST=""
KEY_URL=""
AUTH_METHOD=""
PROVIDER_NOTE=""
DEFAULT_TOOL_GROUPS="web_fetch vision media cron"
TOOLS_ENABLED=""

# ANSI colours only on a real terminal, and respect NO_COLOR.
if [ -t 1 ] && [ -z "${NO_COLOR:-}" ] && [ "${TERM:-}" != "dumb" ]; then
    C_RESET=$'\033[0m'
    C_BOLD=$'\033[1m'
    C_RED=$'\033[31m'
    C_GREEN=$'\033[32m'
    C_YELLOW=$'\033[33m'
    C_CYAN=$'\033[36m'
    C_MAGENTA=$'\033[35m'
else
    C_RESET=""
    C_BOLD=""
    C_RED=""
    C_GREEN=""
    C_YELLOW=""
    C_CYAN=""
    C_MAGENTA=""
fi

info() { printf '%s- %s%s\n' "$C_CYAN" "$*" "$C_RESET"; }
ok() { printf '%s+ %s%s\n' "$C_GREEN" "$*" "$C_RESET"; }
warn() { printf '%s! %s%s\n' "$C_YELLOW" "$*" "$C_RESET" >&2; }
err() { printf '%sx %s%s\n' "$C_RED" "$*" "$C_RESET" >&2; }
step() { printf '\n%s== %s ==%s\n' "$C_BOLD$C_CYAN" "$*" "$C_RESET"; }
die() { err "$*"; exit 1; }

usage() {
    cat <<'EOF'
PicoClaw installer

Usage:
  curl -fsSL https://raw.githubusercontent.com/afjcjsbx/picoclaw/main/scripts/install.sh | bash
  curl -fsSL https://raw.githubusercontent.com/afjcjsbx/picoclaw/main/scripts/install.sh | bash -s -- --non-interactive
  bash scripts/install.sh [options]

Options:
  --non-interactive, --yes, -y  Never prompt; use environment configuration.
  --dir PATH                    Install binaries into PATH (default: ~/.local/bin).
  --no-modify-path              Do not edit ~/.bashrc, ~/.zshrc or ~/.profile.
  --force-config                Back up and replace an existing config.json.
  -h, --help                    Show this help.

Environment:
  PICOCLAW_VERSION              Pin a release tag (for example v0.5.4).
  PICOCLAW_REPO                 GitHub owner/repo (default: afjcjsbx/picoclaw).
  PICOCLAW_INSTALL_DIR          Install directory.
  PICOCLAW_HOME                 Data home (default: ~/.picoclaw).
  PICOCLAW_CONFIG               Config path (default: $PICOCLAW_HOME/config.json).
  PICOCLAW_ARCHIVE              Offline install from a local release archive.
  PICOCLAW_CONFIG_TEMPLATE      Local config.example.json to adapt. Defaults to
                                config/config.example.json from the release.
  PICOCLAW_LOG_LEVEL            Log level written to the generated config
                                (default: warn).

Headless configuration (used with --non-interactive or --yes):
  PICOCLAW_PROVIDER             Any provider id from the interactive catalog
                                (43 chat providers). Use custom or
                                openai-compatible for a custom endpoint.
  PICOCLAW_API_KEY              Provider API key when the provider requires one.
  PICOCLAW_MODEL                Model id; defaults to the provider suggestion.
  PICOCLAW_API_BASE             Optional API base URL override.
  PICOCLAW_WORKSPACE            Workspace path.
  PICOCLAW_RESTRICT_WORKSPACE   1 (default) or 0.
  PICOCLAW_WEB_SEARCH           none, duckduckgo, brave or tavily.
  PICOCLAW_WEB_API_KEY          Brave/Tavily key when required.
  PICOCLAW_TOOLS                Optional tool groups: web_fetch, vision,
                                image_generation, media, voice, reaction,
                                cron, todo, mcp, hardware. Comma-separated,
                                all or none.
                                Core tools are always enabled.
  PICOCLAW_CHANNEL              none, telegram or discord.
  PICOCLAW_TELEGRAM_TOKEN       Telegram bot token.
  PICOCLAW_TELEGRAM_ALLOW_FROM  Comma-separated Telegram user IDs.
  PICOCLAW_DISCORD_TOKEN        Discord bot token.
  PICOCLAW_DISCORD_ALLOW_FROM   Comma-separated Discord user IDs (optional).

The configuration step adapts config/config.example.json with python3.
Provider API keys are shown while you type so you can verify the paste, and
the wizard queries the provider's model endpoint to offer a numbered list of
available models.

Windows: run inside Git Bash/MSYS2, or use WSL. This is a Bash installer and
does not modify the native Windows user PATH outside Git Bash.
FreeBSD/NetBSD: install bash and curl first (pkg install bash curl, or
pkgin install bash curl). python3 is needed for the guided configuration.
This is a Bash installer.
EOF
}

expand_path() {
    local p="$1"
    case "$p" in
        "~") p="$HOME" ;;
        "~/"*) p="$HOME/${p#~/}" ;;
    esac
    # Accept native Windows paths when running under Git Bash/MSYS2.
    case "$p" in
        [A-Za-z]:[\\/]*)
            if command -v cygpath >/dev/null 2>&1; then
                p="$(cygpath -u "$p" 2>/dev/null || printf '%s' "$p")"
            fi
            ;;
    esac
    case "$p" in
        /*|[A-Za-z]:[\\/]*) ;;
        *) p="${PWD:-.}/$p" ;;
    esac
    printf '%s' "$p"
}

to_native_path() {
    if [ "$OS" = "Windows" ] && command -v cygpath >/dev/null 2>&1; then
        cygpath -w "$1" 2>/dev/null || printf '%s' "$1"
    else
        printf '%s' "$1"
    fi
}

quote_cmd() {
    case "$1" in
        *[[:space:]]*) printf '"%s"' "$1" ;;
        *) printf '%s' "$1" ;;
    esac
}

print_banner() {
    printf '\n%s%s' "$C_MAGENTA" "$C_BOLD"
    printf '%s\n' '  +----------------------------------------------+'
    printf '%s\n' '  |              PicoClaw Installer              |'
    printf '%s\n' '  |       tiny - fast - deployable anywhere      |'
    printf '%s\n' '  +----------------------------------------------+'
    printf '%s\n' "$C_RESET"
}

cleanup() {
    if [ "${TUI_ACTIVE:-0}" = "1" ]; then
        end_key_input
    fi
    if [ -n "${TTY_DEV:-}" ] && command -v stty >/dev/null 2>&1; then
        stty echo <"$TTY_DEV" 2>/dev/null || true
    fi
    if [ -n "${TMP_CONFIG:-}" ]; then
        rm -f "$TMP_CONFIG" 2>/dev/null || true
    fi
    if [ -n "${TMP_DIR:-}" ]; then
        rm -rf "$TMP_DIR" 2>/dev/null || true
    fi
}

trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
trap 'exit 129' HUP

normalise_yes_no() {
    case "${1:-}" in
        1|true|TRUE|yes|YES|y|Y) return 0 ;;
    esac
    return 1
}

# ---------------------------------------------------------------------------
# Arguments
# ---------------------------------------------------------------------------

if normalise_yes_no "$NON_INTERACTIVE" || [ "${CI:-}" = "true" ] || [ "${CI:-}" = "1" ]; then
    NON_INTERACTIVE=1
else
    NON_INTERACTIVE=0
fi

while [ "$#" -gt 0 ]; do
    case "$1" in
        --non-interactive|--yes|-y)
            NON_INTERACTIVE=1
            ;;
        --dir|--install-dir)
            [ "$#" -ge 2 ] || die "$1 needs a path"
            INSTALL_DIR="$2"
            shift
            ;;
        --no-modify-path)
            MODIFY_PATH=0
            ;;
        --force-config)
            FORCE_CONFIG=1
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        --)
            shift
            [ "$#" -eq 0 ] || die "Unexpected argument: $1"
            break
            ;;
        -*)
            die "Unknown option: $1 (use --help)"
            ;;
        *)
            die "Unexpected argument: $1 (use --help)"
            ;;
    esac
    shift
done

# Validate the repository before it is put into URLs.
case "$REPO" in
    ""|*/*/*|/*|*/) die "PICOCLAW_REPO must be in owner/repo form" ;;
    *[!A-Za-z0-9._/-]*) die "PICOCLAW_REPO contains unsupported characters" ;;
esac

case "$VERSION_PIN" in
    ""|latest|LATEST) VERSION_PIN="" ;;
    v*) ;;
    *) VERSION_PIN="v$VERSION_PIN" ;;
esac
if [ -n "$VERSION_PIN" ]; then
    case "$VERSION_PIN" in
        *[!A-Za-z0-9._-]*) die "PICOCLAW_VERSION contains unsupported characters" ;;
    esac
fi

# ---------------------------------------------------------------------------
# Terminal / interactivity
# ---------------------------------------------------------------------------

if [ -r /dev/tty ]; then
    if { exec 9<>/dev/tty; } 2>/dev/null; then
        exec 9>&-
        TTY_DEV="/dev/tty"
    fi
elif [ -t 0 ]; then
    TTY_DEV="/dev/stdin"
fi

if [ "$NON_INTERACTIVE" = "0" ] && [ -z "$TTY_DEV" ]; then
    NON_INTERACTIVE=1
    warn "No terminal detected; continuing without the configuration wizard."
fi
if [ "$NON_INTERACTIVE" = "0" ] && [ ! -t 1 ]; then
    NON_INTERACTIVE=1
    warn "Output is not a terminal; continuing without the configuration wizard."
fi

read_line() {
    local __target="$1" __line=""
    if [ -n "$TTY_DEV" ]; then
        IFS= read -r __line <"$TTY_DEV" || return 1
    else
        IFS= read -r __line || return 1
    fi
    printf -v "$__target" '%s' "$__line"
    return 0
}

set_tty_echo() {
    [ -n "$TTY_DEV" ] || return 0
    command -v stty >/dev/null 2>&1 || return 0
    stty "$1" <"$TTY_DEV" 2>/dev/null || true
}

# Put the terminal in non-canonical, no-echo mode for single-key navigation.
# Restores the exact previous state in end_key_input and on exit.
begin_key_input() {
    [ -n "$TTY_DEV" ] || return 1
    command -v stty >/dev/null 2>&1 || return 1
    TUI_STTY_STATE="$(stty -g <"$TTY_DEV" 2>/dev/null)" || return 1
    [ -n "$TUI_STTY_STATE" ] || return 1
    stty -echo -icanon min 1 time 0 <"$TTY_DEV" 2>/dev/null || return 1
    TUI_ACTIVE=1
    return 0
}

end_key_input() {
    [ "${TUI_ACTIVE:-0}" = "1" ] || return 0
    if [ -n "${TUI_STTY_STATE:-}" ] && [ -n "$TTY_DEV" ]; then
        stty "$TUI_STTY_STATE" <"$TTY_DEV" 2>/dev/null || true
    fi
    TUI_ACTIVE=0
}

# Read one key press. Arrow keys arrive as ESC [ A/B and are returned whole.
read_key() {
    local __target="$1" __key="" __rest="" __esc=$'\033'
    IFS= read -r -n 1 __key <"$TTY_DEV" || return 1
    if [ "$__key" = "$__esc" ]; then
        IFS= read -r -n 2 __rest <"$TTY_DEV" || __rest=""
        __key="${__esc}${__rest}"
    fi
    printf -v "$__target" '%s' "$__key"
    return 0
}

ask_required() {
    local __prompt="$1" __var="$2" __answer=""
    while :; do
        printf '%s: ' "$__prompt"
        read_line __answer || die "Input closed."
        if [ -n "$__answer" ]; then
            printf -v "$__var" '%s' "$__answer"
            return 0
        fi
        warn "A value is required."
    done
}

ask_optional() {
    local __prompt="$1" __default="$2" __var="$3" __note="${4:-}" __answer=""
    if [ -n "$__default" ]; then
        if [ -n "$__note" ]; then
            printf '%s [%s] (%s): ' "$__prompt" "$__default" "$__note"
        else
            printf '%s [%s]: ' "$__prompt" "$__default"
        fi
    else
        if [ -n "$__note" ]; then
            printf '%s (%s): ' "$__prompt" "$__note"
        else
            printf '%s (optional): ' "$__prompt"
        fi
    fi
    read_line __answer || die "Input closed."
    if [ -z "$__answer" ]; then
        __answer="$__default"
    fi
    printf -v "$__var" '%s' "$__answer"
    return 0
}

ask_secret() {
    local __prompt="$1" __var="$2" __answer=""
    # Disable echo before printing the prompt so input queued immediately
    # after the prompt cannot be echoed by the terminal driver.
    set_tty_echo -echo
    printf '%s' "$__prompt"
    if [ -n "$TTY_DEV" ] && command -v stty >/dev/null 2>&1; then
        IFS= read -r __answer <"$TTY_DEV" || { set_tty_echo echo; die "Input closed."; }
    else
        IFS= read -r -s __answer || { set_tty_echo echo; die "Input closed."; }
    fi
    set_tty_echo echo
    printf '\n'
    printf -v "$__var" '%s' "$__answer"
    return 0
}

ask_visible() {
    local __prompt="$1" __var="$2" __answer=""
    set_tty_echo echo
    printf '%s' "$__prompt"
    read_line __answer || die "Input closed."
    printf -v "$__var" '%s' "$__answer"
    return 0
}

ask_yes_no() {
    local __prompt="$1" __default="$2" __answer=""
    while :; do
        printf '%s [%s]: ' "$__prompt" "$__default"
        read_line __answer || die "Input closed."
        [ -n "$__answer" ] || __answer="$__default"
        case "$__answer" in
            y|Y|yes|YES) return 0 ;;
            n|N|no|NO) return 1 ;;
            *) warn "Answer yes or no." ;;
        esac
    done
}

choose() {
    local __title="$1" __default="$2" __choice="" __opt="" __i=1
    shift 2
    local __count="$#"
    while :; do
        printf '\n%s%s%s\n' "$C_BOLD" "$__title" "$C_RESET"
        for __opt in "$@"; do
            printf '  %s%2d%s) %s\n' "$C_CYAN" "$__i" "$C_RESET" "$__opt"
            __i=$((__i + 1))
        done
        printf 'Select [%s]: ' "$__default"
        read_line __choice || die "Input closed."
        [ -n "$__choice" ] || __choice="$__default"
        case "$__choice" in
            *[!0-9]*) warn "Enter a number between 1 and $__count."; __i=1; continue ;;
        esac
        __choice=$((10#$__choice))
        if [ "$__choice" -ge 1 ] && [ "$__choice" -le "$__count" ]; then
            CHOICE="$__choice"
            return 0
        fi
        warn "Enter a number between 1 and $__count."
        __i=1
    done
}

# Optional tool groups. Core tools (files, shell, skills and delegation) are
# always enabled; these groups can be toggled at install time.
# Keep the ids in sync with pkg/config/config.go (ToolsConfig).
tool_catalog() {
    cat <<'EOF'
web_fetch|Web fetch & scraping|Fetch pages and URLs (web_fetch)
vision|Vision / image analysis|Inspect screenshots and photos (load_image)
image_generation|Image generation|Create images from prompts (image_generate)
media|File delivery|Send files to the chat (send_file)
voice|Voice synthesis (TTS)|Speak replies with a TTS provider (send_tts)
reaction|Message reactions|React to messages on supported channels (reaction)
cron|Automation & scheduling|Scheduled tasks and reminders (cron)
todo|Todo planning|Track multi-step plans (todo)
mcp|MCP servers|Model Context Protocol integrations (mcp)
hardware|Hardware I/O (Linux)|GPIO, I2C, SPI and serial tools
EOF
}

tool_group_selected() {
    case " ${TOOLS_ENABLED:-} " in
        *" $1 "*) return 0 ;;
    esac
    return 1
}

set_tool_group() {
    local id="$1" state="$2"
    TOOLS_ENABLED="$(printf ' %s ' "${TOOLS_ENABLED:-}" | sed "s/ ${id} / /g" | sed 's/^ *//; s/ *$//')"
    if [ "$state" = "1" ]; then
        TOOLS_ENABLED="${TOOLS_ENABLED:+$TOOLS_ENABLED }$id"
    fi
}

set_all_tool_groups() {
    TOOLS_ENABLED="$(tool_catalog | cut -d'|' -f1 | tr '\n' ' ' | sed 's/^ *//; s/ *$//')"
}

# Accepts comma/space separated group ids, "all" or "none".
set_tool_groups_from_list() {
    local raw="$1" token
    raw="$(printf '%s' "$raw" | tr ',' ' ')"
    TOOLS_ENABLED=""
    for token in $raw; do
        token="$(printf '%s' "$token" | tr '[:upper:]' '[:lower:]')"
        case "$token" in
            ""|none) TOOLS_ENABLED="" ;;
            all) set_all_tool_groups ;;
            *)
                tool_catalog | cut -d'|' -f1 | grep -qx "$token" || return 1
                set_tool_group "$token" 1
                ;;
        esac
    done
    return 0
}

tool_flag() {
    if tool_group_selected "$1"; then
        printf 'true'
    else
        printf 'false'
    fi
}

tools_summary() {
    local id label out=""
    if [ -z "${TOOLS_ENABLED:-}" ]; then
        printf 'core only'
        return 0
    fi
    while IFS='|' read -r id label _; do
        if tool_group_selected "$id"; then
            out="${out:+$out, }$label"
        fi
    done <<EOF
$(tool_catalog)
EOF
    printf '%s' "$out"
}

prompt_tools_tui() {
    local key cursor=1 count lines i id label note mark esc=$'\033'
    count="$(tool_catalog | awk 'END { print NR }')"
    lines=$((count + 1))

    printf '\n%sChoose optional tools%s\n' "$C_BOLD" "$C_RESET"
    printf '  Core tools are always on: files, shell, skills, delegation.\n'
    printf '  Up/Down move, Space toggles, Enter confirms, a=all, n=none.\n'
    while :; do
        i=1
        while IFS='|' read -r id label note; do
            mark=" "
            tool_group_selected "$id" && mark="x"
            if [ "$i" -eq "$cursor" ]; then
                printf '  %s>%s [%s] %2d) %-26s %s\n' \
                    "$C_CYAN$C_BOLD" "$C_RESET" "$mark" "$i" "$label" "$note"
            else
                printf '    [%s] %2d) %-26s %s\n' "$mark" "$i" "$label" "$note"
            fi
            i=$((i + 1))
        done <<EOF
$(tool_catalog)
EOF
        printf '  Up/Down move | Space toggle | Enter confirm | a all | n none\n'
        if ! read_key key; then
            end_key_input
            die "Input closed."
        fi
        case "$key" in
            "${esc}[A"|k|K) cursor=$((cursor - 1)); [ "$cursor" -ge 1 ] || cursor=$count ;;
            "${esc}[B"|j|J) cursor=$((cursor + 1)); [ "$cursor" -le "$count" ] || cursor=1 ;;
            " "|x|X)
                id="$(tool_catalog | sed -n "${cursor}p" | cut -d'|' -f1)"
                if [ -n "$id" ]; then
                    if tool_group_selected "$id"; then
                        set_tool_group "$id" 0
                    else
                        set_tool_group "$id" 1
                    fi
                fi
                ;;
            ""|$'\r'|$'\n') break ;;
            a|A) set_all_tool_groups ;;
            n|N) TOOLS_ENABLED="" ;;
        esac
        printf '\033[%dA\033[J' "$lines"
    done
    printf '\033[%dA\033[J' "$lines"
    end_key_input
    info "Optional tools: $(tools_summary)"
}

prompt_tools() {
    local input token id label note mark i count
    if [ -n "${PICOCLAW_TOOLS:-}" ]; then
        set_tool_groups_from_list "$PICOCLAW_TOOLS" || die "PICOCLAW_TOOLS contains an unknown tool group."
    elif [ -z "${TOOLS_ENABLED:-}" ]; then
        TOOLS_ENABLED="$DEFAULT_TOOL_GROUPS"
    fi

    if begin_key_input; then
        prompt_tools_tui
        return 0
    fi

    printf '\n%sChoose optional tools%s\n' "$C_BOLD" "$C_RESET"
    printf '  Core tools are always on: files, shell, skills, delegation.\n'
    printf '  Toggle by number; Enter keeps the current selection.\n'
    while :; do
        i=1
        while IFS='|' read -r id label note; do
            mark=" "
            tool_group_selected "$id" && mark="x"
            printf '  [%s] %2d) %-26s %s\n' "$mark" "$i" "$label" "$note"
            i=$((i + 1))
        done <<EOF
$(tool_catalog)
EOF
        count=$((i - 1))
        printf 'Toggle (numbers, comma-separated; a=all, n=none; Enter=done): '
        read_line input || die "Input closed."
        input="$(printf '%s' "$input" | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')"
        [ -n "$input" ] || break
        case "$input" in
            a|A|all|ALL) set_all_tool_groups; continue ;;
            n|N|none|NONE) TOOLS_ENABLED=""; continue ;;
        esac
        input="$(printf '%s' "$input" | tr ',' ' ')"
        for token in $input; do
            case "$token" in
                *[!0-9]*)
                    warn "Ignoring invalid selection: $token"
                    continue
                    ;;
            esac
            token=$((10#$token))
            if [ "$token" -lt 1 ] || [ "$token" -gt "$count" ]; then
                warn "Ignoring out-of-range selection: $token"
                continue
            fi
            id="$(tool_catalog | sed -n "${token}p" | cut -d'|' -f1)"
            if tool_group_selected "$id"; then
                set_tool_group "$id" 0
            else
                set_tool_group "$id" 1
            fi
        done
    done
    info "Optional tools: $(tools_summary)"
}

# ---------------------------------------------------------------------------
# Platform and paths
# ---------------------------------------------------------------------------

detect_platform() {
    local raw_os raw_arch
    raw_os="$(uname -s 2>/dev/null || true)"
    raw_arch="$(uname -m 2>/dev/null || true)"
    [ -n "$raw_os" ] || die "Cannot detect the operating system (uname failed)."
    [ -n "$raw_arch" ] || die "Cannot detect the CPU architecture (uname failed)."

    case "$raw_os" in
        Linux*) OS="Linux" ;;
        Darwin*) OS="Darwin" ;;
        FreeBSD*) OS="Freebsd" ;;
        NetBSD*) OS="Netbsd" ;;
        MINGW*|MSYS*|CYGWIN*) OS="Windows" ;;
        *) die "Unsupported operating system: $raw_os. Use Linux, macOS, FreeBSD, NetBSD, or Windows with Git Bash/WSL." ;;
    esac

    # FreeBSD reports 32-bit ARM as "arm" from uname -m; uname -p tells us
    # whether the release asset is armv6 or armv7.
    if [ "$OS" = "Freebsd" ] && [ "$raw_arch" = "arm" ]; then
        raw_arch="$(uname -p 2>/dev/null || true)"
    fi

    case "$raw_arch" in
        x86_64|amd64) ARCH="x86_64" ;;
        arm64|aarch64) ARCH="arm64" ;;
        armv7l|armv7|armv8l|armhf) ARCH="armv7" ;;
        armv6l|armv6) ARCH="armv6" ;;
        riscv64) ARCH="riscv64" ;;
        loongarch64|loong64) ARCH="loong64" ;;
        mipsle|mipsel) ARCH="mipsle" ;;
        s390x) ARCH="s390x" ;;
        *) die "Unsupported CPU architecture: $raw_arch." ;;
    esac

    case "$OS" in
        Linux)
            ASSET="picoclaw_Linux_${ARCH}.tar.gz"
            BIN_NAME="picoclaw"
            LAUNCHER_NAME="picoclaw-launcher"
            ;;
        Darwin)
            case "$ARCH" in
                x86_64|arm64) ;;
                *) die "macOS $ARCH is not published by PicoClaw." ;;
            esac
            ASSET="picoclaw_Darwin_${ARCH}.tar.gz"
            BIN_NAME="picoclaw"
            LAUNCHER_NAME="picoclaw-launcher"
            ;;
        Freebsd)
            case "$ARCH" in
                x86_64|arm64|armv6|armv7) ;;
                *) die "FreeBSD $ARCH is not published by PicoClaw." ;;
            esac
            ASSET="picoclaw_Freebsd_${ARCH}.tar.gz"
            BIN_NAME="picoclaw"
            LAUNCHER_NAME="picoclaw-launcher"
            ;;
        Netbsd)
            case "$ARCH" in
                x86_64|arm64) ;;
                *) die "NetBSD $ARCH is not published by PicoClaw." ;;
            esac
            ASSET="picoclaw_Netbsd_${ARCH}.tar.gz"
            BIN_NAME="picoclaw"
            LAUNCHER_NAME="picoclaw-launcher"
            ;;
        Windows)
            case "$ARCH" in
                x86_64|arm64) ;;
                *) die "Windows $ARCH is not published by PicoClaw." ;;
            esac
            ASSET="picoclaw_Windows_${ARCH}.zip"
            BIN_NAME="picoclaw.exe"
            LAUNCHER_NAME="picoclaw-launcher.exe"
            ;;
    esac

    info "Platform: $OS $ARCH"
}

resolve_paths() {
    [ -n "${INSTALL_DIR:-}" ] || die "Install directory is empty."
    [ -n "${PICOCLAW_HOME:-}" ] || die "PICOCLAW_HOME is empty."
    [ -n "${CONFIG_FILE:-}" ] || die "Configuration path is empty."
    [ -n "${WORKSPACE:-}" ] || die "Workspace path is empty."
    INSTALL_DIR="$(expand_path "$INSTALL_DIR")"
    PICOCLAW_HOME="$(expand_path "$PICOCLAW_HOME")"
    CONFIG_FILE="$(expand_path "$CONFIG_FILE")"
    WORKSPACE="$(expand_path "$WORKSPACE")"
    if [ -n "$LOCAL_ARCHIVE" ]; then
        LOCAL_ARCHIVE="$(expand_path "$LOCAL_ARCHIVE")"
        [ -f "$LOCAL_ARCHIVE" ] || die "PICOCLAW_ARCHIVE does not exist: $LOCAL_ARCHIVE"
    fi
    [ -n "$INSTALL_DIR" ] || die "Install directory is empty."
    [ -n "$CONFIG_FILE" ] || die "Configuration path is empty."
    info "Install directory: $INSTALL_DIR"
}

check_tools() {
    local tool
    for tool in uname mktemp mkdir chmod cp rm mv dirname basename find grep head sed awk tr date tar; do
        command -v "$tool" >/dev/null 2>&1 || die "Required command not found: $tool"
    done
    if [ -z "$LOCAL_ARCHIVE" ]; then
        command -v curl >/dev/null 2>&1 || die "curl is required. Install curl and retry."
    fi
    case "$ASSET" in
        *.zip)
            if ! command -v unzip >/dev/null 2>&1 && ! command -v python3 >/dev/null 2>&1 && ! command -v powershell.exe >/dev/null 2>&1; then
                warn "unzip/python3/powershell not found; trying tar for the ZIP archive."
            fi
            ;;
    esac
    if [ -z "$LOCAL_ARCHIVE" ]; then
        if ! command -v sha256sum >/dev/null 2>&1 &&
           ! command -v shasum >/dev/null 2>&1 &&
           ! command -v sha256 >/dev/null 2>&1 &&
           ! command -v openssl >/dev/null 2>&1 &&
           ! command -v certutil.exe >/dev/null 2>&1; then
            warn "No SHA-256 tool found; archive checksum verification will be skipped."
        fi
    fi
}

prepare_install_dir() {
    mkdir -p "$INSTALL_DIR" || die "Cannot create install directory: $INSTALL_DIR"
    [ -d "$INSTALL_DIR" ] || die "Install path is not a directory: $INSTALL_DIR"
    [ -w "$INSTALL_DIR" ] || die "Install directory is not writable: $INSTALL_DIR (use --dir or sudo manually)"
    BIN_PATH="$INSTALL_DIR/$BIN_NAME"
    LAUNCHER_PATH="$INSTALL_DIR/$LAUNCHER_NAME"
    if [ -e "$BIN_PATH" ]; then
        info "Existing PicoClaw binary will be replaced: $BIN_PATH"
    fi
    local existing
    existing="$(command -v picoclaw 2>/dev/null || true)"
    if [ -n "$existing" ] && [ "$existing" != "$BIN_PATH" ]; then
        warn "Another picoclaw is already on PATH: $existing"
    fi
}

make_tmp() {
    TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/picoclaw-install.XXXXXX" 2>/dev/null || true)"
    if [ -z "$TMP_DIR" ]; then
        TMP_DIR="${TMPDIR:-/tmp}/picoclaw-install.$$"
        mkdir -p "$TMP_DIR" || die "Cannot create temporary directory."
    fi
    [ -d "$TMP_DIR" ] || die "Cannot create temporary directory."
    chmod 700 "$TMP_DIR" 2>/dev/null || true
}

# ---------------------------------------------------------------------------
# Release download
# ---------------------------------------------------------------------------

valid_tag() {
    case "${1:-}" in
        ""|*[!A-Za-z0-9._-]*) return 1 ;;
    esac
    return 0
}

resolve_release() {
    if [ -n "$VERSION_PIN" ]; then
        TAG="$VERSION_PIN"
        valid_tag "$TAG" || die "Invalid release tag: $TAG"
        return 0
    fi

    local api_url api_json effective
    api_url="https://api.github.com/repos/$REPO/releases/latest"
    api_json="$(curl -fsSL --connect-timeout 10 --max-time 30 \
        --proto '=https' --tlsv1.2 \
        -H 'Accept: application/vnd.github+json' \
        -H 'User-Agent: picoclaw-installer' \
        "$api_url" 2>/dev/null || true)"
    TAG="$(printf '%s\n' "$api_json" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)"

    if ! valid_tag "$TAG"; then
        effective="$(curl -fsSIL --connect-timeout 10 --max-time 30 \
            --proto '=https' --tlsv1.2 \
            -H 'User-Agent: picoclaw-installer' \
            -o /dev/null -w '%{url_effective}' \
            "https://github.com/$REPO/releases/latest" 2>/dev/null || true)"
        TAG="$(printf '%s' "$effective" | sed -n 's#.*/releases/tag/\([^/?#]*\).*#\1#p' | head -n 1)"
    fi

    valid_tag "$TAG" || die "Could not determine the latest release. Set PICOCLAW_VERSION=v0.5.4 and retry."
    info "Latest release: $TAG"
}

download_file() {
    local url="$1" dest="$2" label="$3" dest_arg
    dest_arg="$(to_native_path "$dest")"
    info "$label"
    if [ -t 1 ]; then
        if ! curl -fL -S --progress-bar --retry 3 --retry-delay 2 --retry-connrefused \
            --connect-timeout 15 --max-time 600 --proto '=https' --tlsv1.2 \
            "$url" -o "$dest_arg"; then
            return 1
        fi
    else
        if ! curl -fsSL --retry 3 --retry-delay 2 --retry-connrefused \
            --connect-timeout 15 --max-time 600 --proto '=https' --tlsv1.2 \
            "$url" -o "$dest_arg"; then
            return 1
        fi
    fi
    return 0
}

sha256_file() {
    local file="$1" out=""
    if command -v sha256sum >/dev/null 2>&1; then
        out="$(sha256sum "$file" 2>/dev/null | awk '{print $1}')"
    elif command -v shasum >/dev/null 2>&1; then
        out="$(shasum -a 256 "$file" 2>/dev/null | awk '{print $1}')"
    elif command -v sha256 >/dev/null 2>&1; then
        # FreeBSD and NetBSD ship sha256(1) in the base system.
        out="$(sha256 -q "$file" 2>/dev/null || sha256 "$file" 2>/dev/null)"
        out="$(printf '%s\n' "$out" | awk 'NF { print $NF; exit }')"
    elif command -v openssl >/dev/null 2>&1; then
        out="$(openssl dgst -sha256 "$file" 2>/dev/null | sed 's/^.*= //')"
    elif command -v certutil.exe >/dev/null 2>&1; then
        out="$(certutil.exe -hashfile "$(to_native_path "$file")" SHA256 2>/dev/null | sed -n '2p' | tr -d ' \r')"
    else
        return 1
    fi
    out="$(printf '%s' "$out" | tr 'A-F' 'a-f' | tr -d '\r\n ')"
    [ -n "$out" ] || return 1
    printf '%s' "$out"
}

verify_checksum() {
    local checksum_url checksum_file expected actual
    checksum_file="$TMP_DIR/checksums.txt"
    checksum_url="https://github.com/$REPO/releases/download/$TAG/picoclaw_${TAG#v}_checksums.txt"

    if ! curl -fsSL --connect-timeout 10 --max-time 60 --proto '=https' --tlsv1.2 \
        "$checksum_url" -o "$(to_native_path "$checksum_file")" 2>/dev/null; then
        warn "Published checksums are unavailable; skipping archive verification."
        return 0
    fi

    expected="$(awk -v name="$ASSET" '$2 == name { print $1; exit }' "$checksum_file" 2>/dev/null | tr 'A-F' 'a-f' | tr -d '\r\n ')"
    if [ -z "$expected" ]; then
        warn "No checksum entry found for $ASSET; skipping archive verification."
        return 0
    fi

    if ! actual="$(sha256_file "$ARCHIVE")"; then
        warn "No SHA-256 verifier available; skipping archive verification."
        return 0
    fi

    if [ "$expected" != "$actual" ]; then
        die "Checksum mismatch for $ASSET! Expected $expected, got $actual. Aborting for safety."
    fi
    ok "SHA-256 checksum verified."
}

get_archive() {
    if [ -n "$LOCAL_ARCHIVE" ]; then
        ARCHIVE="$LOCAL_ARCHIVE"
        ASSET="$(basename "$ARCHIVE")"
        info "Using local archive: $ARCHIVE"
        info "Local archive supplied; remote checksum verification skipped."
        return 0
    fi

    resolve_release
    ASSET="picoclaw_${OS}_${ARCH}.tar.gz"
    case "$OS" in
        Windows) ASSET="picoclaw_Windows_${ARCH}.zip" ;;
        Linux) ASSET="picoclaw_Linux_${ARCH}.tar.gz" ;;
        Darwin) ASSET="picoclaw_Darwin_${ARCH}.tar.gz" ;;
    esac

    ARCHIVE="$TMP_DIR/$ASSET"
    local url="https://github.com/$REPO/releases/download/$TAG/$ASSET"
    download_file "$url" "$ARCHIVE" "Downloading $ASSET" || die "Download failed: $url"
    verify_checksum
    ok "Downloaded $ASSET."
}

# ---------------------------------------------------------------------------
# Extraction and installation
# ---------------------------------------------------------------------------

extract_archive() {
    EXTRACT_DIR="$TMP_DIR/extract"
    rm -rf "$EXTRACT_DIR"
    mkdir -p "$EXTRACT_DIR"
    case "$ARCHIVE" in
        *.tar.gz|*.tgz)
            tar -xzf "$ARCHIVE" -C "$EXTRACT_DIR" || die "Could not extract $ARCHIVE"
            ;;
        *.zip)
            if command -v unzip >/dev/null 2>&1; then
                unzip -q "$ARCHIVE" -d "$EXTRACT_DIR" || die "Could not extract $ARCHIVE with unzip"
            elif tar -xf "$ARCHIVE" -C "$EXTRACT_DIR" 2>/dev/null; then
                :
            elif command -v python3 >/dev/null 2>&1; then
                rm -rf "$EXTRACT_DIR"
                mkdir -p "$EXTRACT_DIR"
                python3 -m zipfile -e "$ARCHIVE" "$EXTRACT_DIR" || die "Could not extract $ARCHIVE with python3"
            elif command -v powershell.exe >/dev/null 2>&1; then
                rm -rf "$EXTRACT_DIR"
                mkdir -p "$EXTRACT_DIR"
                local arc_win dst_win
                arc_win="$(cygpath -w "$ARCHIVE" 2>/dev/null || printf '%s' "$ARCHIVE")"
                dst_win="$(cygpath -w "$EXTRACT_DIR" 2>/dev/null || printf '%s' "$EXTRACT_DIR")"
                powershell.exe -NoProfile -NonInteractive -Command \
                    "Expand-Archive -LiteralPath '$arc_win' -DestinationPath '$dst_win' -Force" \
                    || die "Could not extract $ARCHIVE with PowerShell"
            else
                die "No ZIP extractor available (unzip, python3 or PowerShell)."
            fi
            ;;
        *)
            die "Unsupported archive format: $ARCHIVE"
            ;;
    esac
}

install_binary() {
    local src="$1" dst="$2" label="$3" tmp
    [ -f "$src" ] || { err "Binary not found in archive: $label"; return 1; }
    [ -d "$dst" ] && { err "Install target is a directory: $dst"; return 1; }
    tmp="${dst}.new.$$"
    rm -f "$tmp" 2>/dev/null || true
    if command -v install >/dev/null 2>&1; then
        install -m 0755 "$src" "$tmp" || { rm -f "$tmp"; err "Could not install $label to $dst"; return 1; }
    else
        cp "$src" "$tmp" || { rm -f "$tmp"; err "Could not copy $label to $dst"; return 1; }
        chmod 0755 "$tmp" || { rm -f "$tmp"; err "Could not make $label executable"; return 1; }
    fi
    if ! mv -f "$tmp" "$dst"; then
        rm -f "$tmp" 2>/dev/null || true
        err "Could not move $label into place: $dst"
        return 1
    fi
    if [ "$OS" = "Darwin" ] && command -v xattr >/dev/null 2>&1; then
        xattr -d com.apple.quarantine "$dst" 2>/dev/null || true
    fi
    return 0
}

install_binaries() {
    local src_bin src_launcher
    src_bin="$(find "$EXTRACT_DIR" -type f -name "$BIN_NAME" -print | head -n 1)"
    if [ -z "$src_bin" ] && [ "$OS" = "Windows" ]; then
        src_bin="$(find "$EXTRACT_DIR" -type f -name "picoclaw" -print | head -n 1)"
    fi
    [ -n "$src_bin" ] || die "The archive does not contain $BIN_NAME. Files: $(find "$EXTRACT_DIR" -maxdepth 2 -type f -print | tr '\n' ' ')"
    install_binary "$src_bin" "$BIN_PATH" "PicoClaw" || die "PicoClaw installation failed."
    ok "Installed PicoClaw: $BIN_PATH"

    src_launcher="$(find "$EXTRACT_DIR" -type f -name "$LAUNCHER_NAME" -print | head -n 1)"
    if [ -n "$src_launcher" ]; then
        if install_binary "$src_launcher" "$LAUNCHER_PATH" "PicoClaw Launcher"; then
            LAUNCHER_INSTALLED=1
            ok "Installed launcher: $LAUNCHER_PATH"
        else
            warn "Launcher installation failed; the core binary is still usable."
        fi
    else
        info "Launcher not present in this archive; core binary installed only."
    fi
}

# ---------------------------------------------------------------------------
# PATH
# ---------------------------------------------------------------------------

PATH_MARKER="# >>> picoclaw installer >>>"
PATH_MARKER_END="# <<< picoclaw installer <<<"

shell_dq_escape() {
    printf '%s' "$1" | sed 's/[\\"$`]/\\&/g'
}

add_path_block() {
    local profile="$1" escaped
    if [ -f "$profile" ] && grep -Fq "$PATH_MARKER" "$profile" 2>/dev/null; then
        if grep -Fq "$INSTALL_DIR" "$profile" 2>/dev/null; then
            info "PATH already configured in $profile"
            return 0
        fi
        warn "PATH block in $profile points to a different install location; leaving it unchanged."
        return 1
    fi
    if [ ! -e "$profile" ]; then
        : >"$profile" 2>/dev/null || { warn "Cannot create $profile"; return 1; }
        chmod 644 "$profile" 2>/dev/null || true
    fi
    [ -w "$profile" ] || { warn "Cannot write $profile"; return 1; }

    escaped="$(shell_dq_escape "$INSTALL_DIR")"
    {
        printf '\n%s\n' "$PATH_MARKER"
        printf 'case ":$PATH:" in\n'
        printf '  *":%s:"*) ;;\n' "$escaped"
        printf '  *) export PATH="%s:$PATH" ;;\n' "$escaped"
        printf 'esac\n'
        printf '%s\n' "$PATH_MARKER_END"
    } >>"$profile" 2>/dev/null || { warn "Cannot update $profile"; return 1; }
    ok "Added $INSTALL_DIR to PATH in $profile"
    return 0
}

configure_path() {
    local failed=0
    if [ "$MODIFY_PATH" = "0" ]; then
        info "Skipping shell profile updates (--no-modify-path)."
    else
        add_path_block "$HOME/.bashrc" || failed=1
        add_path_block "$HOME/.zshrc" || failed=1
        if [ -f "$HOME/.bash_profile" ]; then
            add_path_block "$HOME/.bash_profile" || failed=1
        fi
        if [ -f "$HOME/.profile" ]; then
            add_path_block "$HOME/.profile" || failed=1
        fi
        if [ "$failed" = "0" ]; then
            PATH_CONFIGURED=1
        else
            warn "Some shell profiles could not be updated; use the export command below."
        fi
    fi
    export PATH="$INSTALL_DIR:$PATH"
}

# ---------------------------------------------------------------------------
# Binary sanity check
# ---------------------------------------------------------------------------

sanity_check() {
    local version_out=""
    [ -x "$BIN_PATH" ] || die "Installed binary is not executable: $BIN_PATH"

    # Newer releases support --version. Older ones only expose the version
    # subcommand, so fall back instead of failing a correct install.
    if version_out="$("$BIN_PATH" --version 2>/dev/null)" && [ -n "$version_out" ]; then
        :
    elif version_out="$("$BIN_PATH" --no-color version 2>&1)"; then
        :
    else
        version_out="$("$BIN_PATH" version 2>&1)" || die "Installed binary failed to run: $BIN_PATH"
    fi
    VERSION_LINE="$(printf '%s\n' "$version_out" | sed -n 's/.*\(picoclaw[[:space:]][0-9][^[:space:]]*\).*/\1/p' | head -n 1)"
    if [ -z "$VERSION_LINE" ]; then
        VERSION_LINE="$(printf '%s\n' "$version_out" | sed -n '/[^[:space:]]/p' | head -n 1)"
    fi
    [ -n "$VERSION_LINE" ] || VERSION_LINE="PicoClaw"
    ok "Binary OK: $VERSION_LINE"
}

# ---------------------------------------------------------------------------
# Interactive configuration wizard
# ---------------------------------------------------------------------------

json_escape() {
    local s="$1" i ch esc
    s=${s//\\/\\\\}
    s=${s//\"/\\\"}
    s=${s//$'\n'/\\n}
    s=${s//$'\r'/\\r}
    s=${s//$'\t'/\\t}
    i=1
    while [ "$i" -le 31 ]; do
        case "$i" in
            9|10|13) i=$((i + 1)); continue ;;
        esac
        printf -v ch "\\$(printf '%03o' "$i")"
        printf -v esc '\\u%04x' "$i"
        s=${s//"$ch"/$esc}
        i=$((i + 1))
    done
    printf '%s' "$s"
}

valid_ids() {
    local raw="$1" id found=0
    raw="$(printf '%s' "$raw" | tr ',' ' ')"
    for id in $raw; do
        [ -n "$id" ] || continue
        if [[ ! "$id" =~ ^-?[0-9]+$ ]]; then
            return 1
        fi
        found=1
    done
    [ "$found" = "1" ]
}

ids_to_json() {
    local raw="$1" id out="" first=1
    raw="$(printf '%s' "$raw" | tr ',' ' ')"
    for id in $raw; do
        id="$(printf '%s' "$id" | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')"
        [ -n "$id" ] || continue
        if [ "$first" = "0" ]; then
            out="$out, "
        fi
        out="$out\"$(json_escape "$id")\""
        first=0
    done
    printf '[%s]' "$out"
}

# Keep this catalog in sync with pkg/providers/provider_metadata.go.
# Format:
#   id|label|api_base|base_mode|default_model|key_mode|key_url|auth_method|note
# base_mode: default, required, optional, none
# key_mode:  required, optional, none
# elevenlabs is intentionally absent: it is ASR-only and cannot be the main chat model.
provider_catalog() {
    cat <<'EOF'
openrouter|OpenRouter|https://openrouter.ai/api/v1|default|openai/gpt-5.4|required|https://openrouter.ai/keys||
anthropic|Anthropic|https://api.anthropic.com/v1|default|claude-sonnet-4-6|required|https://console.anthropic.com/settings/keys||
deepseek|DeepSeek|https://api.deepseek.com/v1|default|deepseek-v4-flash|required|https://platform.deepseek.com/api_keys||
openai|OpenAI|https://api.openai.com/v1|default|gpt-5.4|required|https://platform.openai.com/api-keys||
groq|Groq|https://api.groq.com/openai/v1|default|openai/gpt-oss-120b|required|https://console.groq.com/keys||
gemini|Google Gemini|https://generativelanguage.googleapis.com/v1beta|default|gemini-3-flash-preview|required|https://aistudio.google.com/app/apikey||
qwen-portal|Qwen|https://dashscope.aliyuncs.com/compatible-mode/v1|default|qwen3.6-plus|required|https://dashscope.console.aliyun.com/apiKey||
ollama|Ollama (local)|http://localhost:11434/v1|default|llama3.2|none|||Make sure Ollama is running and the model is pulled.
alibaba-coding|Alibaba Coding Plan|https://coding-intl.dashscope.aliyuncs.com/v1|default|qwen3.6-plus|required|https://bailian.console.aliyun.com||
alibaba-coding-anthropic|Alibaba Coding Plan (Anthropic)|https://coding-intl.dashscope.aliyuncs.com/apps/anthropic|default|qwen3.6-plus|required|https://bailian.console.aliyun.com||
anthropic-messages|Anthropic Messages|https://api.anthropic.com/v1|default|claude-sonnet-4-6|required|https://console.anthropic.com/settings/keys||
antigravity|Google Code Assist (OAuth)||none|gemini-3-flash|none||oauth|Run after install: picoclaw auth login --provider antigravity
avian|Avian|https://api.avian.io/v1|default|deepseek/deepseek-v3.2|required|https://avian.io||
azure|Azure OpenAI||required||required|https://portal.azure.com||Use the Azure deployment name as model and the resource endpoint as base URL.
bedrock|AWS Bedrock||optional||none|https://console.aws.amazon.com/bedrock||Requires AWS credentials and a bedrock-enabled build; api_base may be an AWS region.
byteplus|BytePlus|https://ark.ap-southeast.bytepluses.com/api/v3|default||required|https://console.byteplus.com/ark||
cerebras|Cerebras|https://api.cerebras.ai/v1|default|gpt-oss-120b|required|https://cloud.cerebras.ai||
claude-cli|Claude CLI||none|claude-sonnet-4-6|none|||Requires the Claude CLI installed and authenticated.
codex-cli|Codex CLI||none|gpt-5.4|none|||Requires the Codex CLI installed and authenticated.
github-copilot|GitHub Copilot|http://localhost:4321|default|gpt-5.4|none||oauth|Uses the local Copilot bridge; OAuth auth_method is written to config.
gpt4free|GPT4Free|http://localhost:1337/v1|default||optional|||
litellm|LiteLLM|http://localhost:4000/v1|default||optional|||
lmstudio|LM Studio|http://localhost:1234/v1|default||optional|||
longcat|LongCat|https://api.longcat.chat/openai|default|LongCat-Flash-Thinking|required|https://longcat.chat/platform/api_keys||
minimax|MiniMax|https://api.minimaxi.com/v1|default|MiniMax-M2.5|required|https://platform.minimaxi.com/user-center/basic-information/interface-key||
mistral|Mistral AI|https://api.mistral.ai/v1|default|mistral-large-latest|required|https://console.mistral.ai/api-keys||
mimo|Xiaomi MiMo|https://api.xiaomimimo.com/v1|default|mimo-v2.5|required|https://platform.xiaomimimo.com||
modelscope|ModelScope|https://api-inference.modelscope.cn/v1|default|Qwen/Qwen3-235B-A22B-Instruct-2507|required|https://modelscope.cn/my/myaccesstoken||
moonshot|Moonshot|https://api.moonshot.cn/v1|default|kimi-k2.5|required|https://platform.moonshot.cn/console/api-keys||
nearai|NEAR AI Cloud|https://cloud-api.near.ai/v1|default|zai-org/GLM-5.1-FP8|required|https://near.ai||
nvidia|NVIDIA|https://integrate.api.nvidia.com/v1|default|openai/gpt-oss-120b|required|https://build.nvidia.com||
novita|Novita AI|https://api.novita.ai/openai|default||required|https://novita.ai/settings/key-management||
opencode-go|OpenCode Go|https://opencode.ai/inference/go|default|kimi-k3|required|https://opencode.ai/auth||
qwen-intl|Qwen International|https://dashscope-intl.aliyuncs.com/compatible-mode/v1|default|qwen3.6-plus|required|https://dashscope-intl.console.aliyun.com||
qwen-us|Qwen US|https://dashscope-us.aliyuncs.com/compatible-mode/v1|default|qwen3.6-plus|required|https://dashscope-us.console.aliyun.com||
shengsuanyun|ShengsuanYun|https://router.shengsuanyun.com/api/v1|default|deepseek-v3|required|https://www.shengsuanyun.com||
siliconflow|SiliconFlow|https://api.siliconflow.cn/v1|default||required|https://cloud.siliconflow.cn/account/ak||
venice|Venice AI|https://api.venice.ai/api/v1|default|venice-uncensored|required|https://venice.ai/settings/api||
vivgrid|Vivgrid|https://api.vivgrid.com/v1|default||required|https://vivgrid.com||
vllm|VLLM (local)|http://localhost:8000/v1|default||optional|||
volcengine|Volcengine|https://ark.cn-beijing.volces.com/api/v3|default|doubao-seed-1-6-251015|required|https://console.volcengine.com/ark||
zai|Z.ai|https://api.z.ai/api/coding/paas/v4|default|glm-5|required|https://z.ai/manage-apikey/apikey-list||
zhipu|Zhipu AI|https://open.bigmodel.cn/api/paas/v4|default|glm-5|required|https://open.bigmodel.cn/usercenter/apikeys||
EOF
}

normalize_provider_id() {
    local p="$1"
    case "$p" in
        custom|openai-compatible) printf '__custom__' ;;
        qwen) printf 'qwen-portal' ;;
        qwen-international|dashscope-intl) printf 'qwen-intl' ;;
        dashscope-us) printf 'qwen-us' ;;
        gpt) printf 'openai' ;;
        claude) printf 'anthropic' ;;
        google) printf 'gemini' ;;
        glm) printf 'zhipu' ;;
        copilot) printf 'github-copilot' ;;
        google-antigravity) printf 'antigravity' ;;
        azure-openai) printf 'azure' ;;
        claudecli) printf 'claude-cli' ;;
        codexcli) printf 'codex-cli' ;;
        g4f) printf 'gpt4free' ;;
        near-ai|near-ai-cloud) printf 'nearai' ;;
        z.ai|z-ai) printf 'zai' ;;
        coding-plan|qwen-coding) printf 'alibaba-coding' ;;
        coding-plan-anthropic) printf 'alibaba-coding-anthropic' ;;
        *) printf '%s' "$p" ;;
    esac
}

load_provider_record() {
    local id="$1" record
    record="$(provider_catalog | awk -F'|' -v id="$id" '$1 == id { print; exit }')"
    [ -n "$record" ] || return 1
    IFS='|' read -r PROVIDER PROVIDER_LABEL DEFAULT_BASE BASE_MODE DEFAULT_MODEL KEY_MODE KEY_URL AUTH_METHOD PROVIDER_NOTE <<< "$record"
    [ -n "$PROVIDER_LABEL" ] || PROVIDER_LABEL="$PROVIDER"
    return 0
}

set_custom_provider() {
    PROVIDER="openai"
    PROVIDER_LABEL="Custom OpenAI-compatible"
    DEFAULT_BASE=""
    BASE_MODE="required"
    DEFAULT_MODEL=""
    KEY_MODE="optional"
    KEY_URL=""
    AUTH_METHOD=""
    PROVIDER_NOTE="Point to an OpenAI-compatible /v1 endpoint."
}

choose_provider() {
    local count line id label rest choice i
    count="$(provider_catalog | awk 'END { print NR }')"
    [ "${count:-0}" -gt 0 ] 2>/dev/null || die "Provider catalog is empty."

    printf '\n%sChoose your main LLM provider%s\n' "$C_BOLD" "$C_RESET"
    printf '  %s chat providers available (voice-only ASR excluded); scroll if your terminal is short.\n' "$count"
    i=1
    while IFS='|' read -r id label rest; do
        printf '  %s%2d%s) %s [%s]\n' "$C_CYAN" "$i" "$C_RESET" "$label" "$id"
        i=$((i + 1))
    done <<EOF
$(provider_catalog)
EOF
    printf '  %s%2d%s) Custom OpenAI-compatible endpoint\n' "$C_CYAN" "$((count + 1))" "$C_RESET"

    while :; do
        printf 'Select [1]: '
        read_line choice || die "Input closed."
        [ -n "$choice" ] || choice="1"
        case "$choice" in
            *[!0-9]*) warn "Enter a number between 1 and $((count + 1))."; continue ;;
        esac
        choice=$((10#$choice))
        if [ "$choice" -ge 1 ] && [ "$choice" -le "$((count + 1))" ]; then
            CHOICE="$choice"
            break
        fi
        warn "Enter a number between 1 and $((count + 1))."
    done

    if [ "$CHOICE" -eq "$((count + 1))" ]; then
        set_custom_provider
        return 0
    fi
    line="$(provider_catalog | sed -n "${CHOICE}p")"
    [ -n "$line" ] || die "Could not read the selected provider."
    IFS='|' read -r PROVIDER PROVIDER_LABEL DEFAULT_BASE BASE_MODE DEFAULT_MODEL KEY_MODE KEY_URL AUTH_METHOD PROVIDER_NOTE <<< "$line"
}

prompt_provider() {
    choose_provider
    [ -n "$PROVIDER_NOTE" ] && info "$PROVIDER_NOTE"
    return 0
}

prompt_api_key() {
    local prompt="API key: "
    if [ -n "${PICOCLAW_API_KEY:-}" ]; then
        prompt="API key (blank = use PICOCLAW_API_KEY): "
    fi
    case "$KEY_MODE" in
        required)
            [ -n "$KEY_URL" ] && info "Get a key: $KEY_URL"
            info "The API key is shown while you type so you can verify the paste."
            while :; do
                ask_visible "$prompt" API_KEY
                [ -n "$API_KEY" ] || API_KEY="${PICOCLAW_API_KEY:-}"
                [ "$API_KEY" != "" ] && return 0
                warn "An API key is required for this provider."
            done
            ;;
        optional)
            info "The API key is shown while you type so you can verify the paste."
            ask_visible "API key (blank for an unauthenticated endpoint): " API_KEY
            [ -n "$API_KEY" ] || API_KEY="${PICOCLAW_API_KEY:-}"
            [ -n "$API_KEY" ] || warn "Continuing without an API key."
            ;;
        *)
            API_KEY=""
            info "No API key needed for $PROVIDER_LABEL."
            ;;
    esac
}

valid_api_base() {
    local value="$1"
    [ -n "$value" ] || return 0
    case "$value" in
        http://*|https://*) return 0 ;;
    esac
    # Bedrock accepts an AWS region (for example us-east-1) as api_base.
    [ "${PROVIDER:-}" = "bedrock" ] && return 0
    return 1
}

prompt_base() {
    local base_default="${PICOCLAW_API_BASE:-$DEFAULT_BASE}" base_hint="API base URL" base_note=""
    case "$PROVIDER" in
        azure) base_hint="Azure resource endpoint" ;;
        bedrock) base_hint="AWS region or endpoint" ;;
    esac
    if [ -n "$base_default" ]; then
        base_note="press Enter to keep the default"
    elif [ "$BASE_MODE" = "optional" ]; then
        base_note="optional, press Enter to skip"
    else
        base_note="a value is required"
    fi

    case "$BASE_MODE" in
        none)
            API_BASE=""
            return 0
            ;;
        required)
            while :; do
                if [ -n "$base_default" ]; then
                    ask_optional "$base_hint" "$base_default" API_BASE "$base_note"
                else
                    ask_required "$base_hint" API_BASE
                fi
                if [ -n "$API_BASE" ] && valid_api_base "$API_BASE"; then
                    return 0
                fi
                warn "Enter a valid http(s) URL."
            done
            ;;
        optional)
            while :; do
                if [ -n "$base_default" ]; then
                    ask_optional "$base_hint" "$base_default" API_BASE "$base_note"
                else
                    ask_optional "$base_hint" "" API_BASE "$base_note"
                fi
                if [ -z "$API_BASE" ] || valid_api_base "$API_BASE"; then
                    return 0
                fi
                warn "Enter a valid URL, or leave blank."
            done
            ;;
        *)
            while :; do
                if [ -n "$base_default" ]; then
                    ask_optional "$base_hint" "$base_default" API_BASE "$base_note"
                else
                    ask_optional "$base_hint" "" API_BASE "$base_note"
                fi
                if [ -n "$API_BASE" ] && valid_api_base "$API_BASE"; then
                    return 0
                fi
                warn "Enter a valid http(s) URL."
            done
            ;;
    esac
}

# ---------------------------------------------------------------------------
# Model discovery
# ---------------------------------------------------------------------------

# Providers with a non-standard model-list API are handled explicitly.
# Azure and Bedrock expose deployment/model catalogs that cannot be queried
# with a simple /models request, so they keep the manual model prompt.
model_list_urls() {
    local base="${API_BASE:-$DEFAULT_BASE}"
    [ -n "$base" ] || return 0
    base="${base%/}"
    case "$PROVIDER" in
        gemini)
            printf '%s\n' "${base}/models?pageSize=1000"
            printf '%s\n' "${base}/models"
            return 0
            ;;
        anthropic|anthropic-messages|alibaba-coding-anthropic)
            printf '%s\n' "${base}/models?limit=1000"
            printf '%s\n' "${base}/models"
            case "$base" in
                */v1) ;;
                *) printf '%s\n' "${base}/v1/models" ;;
            esac
            return 0
            ;;
        azure|bedrock|antigravity|claude-cli|codex-cli)
            return 0
            ;;
    esac
    printf '%s\n' "${base}/models"
    case "$base" in
        */v[0-9]|*/v[0-9][0-9]|*/v[0-9]beta|*/v[0-9][0-9]beta)
            ;;
        *)
            printf '%s\n' "${base}/v1/models"
            ;;
    esac
}

curl_config_escape() {
    printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g'
}

write_model_curl_config() {
    local file="$1" key="${API_KEY:-}"
    {
        printf 'header = "Accept: application/json"\n'
        case "$PROVIDER" in
            gemini)
                [ -n "$key" ] && printf 'header = "x-goog-api-key: %s"\n' "$(curl_config_escape "$key")"
                ;;
            anthropic|anthropic-messages|alibaba-coding-anthropic)
                [ -n "$key" ] && printf 'header = "x-api-key: %s"\n' "$(curl_config_escape "$key")"
                printf 'header = "anthropic-version: 2023-06-01"\n'
                ;;
            *)
                [ -n "$key" ] && printf 'header = "Authorization: Bearer %s"\n' "$(curl_config_escape "$key")"
                ;;
        esac
    } >"$file" || return 1
    chmod 600 "$file" 2>/dev/null || true
    return 0
}

parse_model_list() {
    python3 -c '
import json
import sys

try:
    payload = json.load(sys.stdin)
except Exception:
    sys.exit(1)

def items(obj):
    if isinstance(obj, list):
        return obj
    if isinstance(obj, dict):
        for key in ("data", "models", "result", "results", "model_list"):
            value = obj.get(key)
            if isinstance(value, list):
                return value
            if isinstance(value, dict):
                for nested in ("data", "models", "result"):
                    if isinstance(value.get(nested), list):
                        return value[nested]
    return []

def model_id(item):
    if isinstance(item, str):
        return item
    if isinstance(item, dict):
        for key in ("id", "name", "model", "model_id"):
            value = item.get(key)
            if isinstance(value, str) and value.strip():
                return value
    return ""

ids = []
seen = set()
for item in items(payload):
    mid = model_id(item).strip()
    if mid.startswith("models/"):
        mid = mid[len("models/"):]
    if mid and mid not in seen:
        seen.add(mid)
        ids.append(mid)
print("\n".join(ids))
'
}

fetch_model_list() {
    local base urls last_code code tmp auth_conf native_tmp native_auth
    MODEL_LIST=""
    [ -n "${API_BASE:-}" ] || API_BASE="$DEFAULT_BASE"
    base="${API_BASE:-}"
    [ -n "$base" ] || return 1
    command -v curl >/dev/null 2>&1 || return 1
    if ! command -v python3 >/dev/null 2>&1; then
        warn "python3 is required to read the model list; enter the model id manually."
        return 1
    fi
    urls="$(model_list_urls)"
    [ -n "$urls" ] || return 1
    tmp="$TMP_DIR/models.json"
    auth_conf="$TMP_DIR/models.headers"
    write_model_curl_config "$auth_conf" || return 1
    native_tmp="$(to_native_path "$tmp")"
    native_auth="$(to_native_path "$auth_conf")"

    info "Fetching available models from $PROVIDER_LABEL..."
    last_code=""
    while IFS= read -r url; do
        [ -n "$url" ] || continue
        code="$(curl -sS -K "$native_auth" -o "$native_tmp" -w '%{http_code}' \
            --connect-timeout 5 --max-time 15 "$url" 2>/dev/null || true)"
        last_code="${code:-000}"
        [ "$last_code" = "200" ] || continue
        MODEL_LIST="$(parse_model_list <"$tmp" 2>/dev/null || true)"
        if [ -n "$MODEL_LIST" ]; then
            return 0
        fi
    done <<EOF
$urls
EOF

    case "$last_code" in
        401|403) warn "The endpoint rejected the API key (HTTP $last_code); enter the model id manually." ;;
        404) warn "The endpoint does not expose a model list (HTTP 404); enter the model id manually." ;;
        200) warn "The endpoint returned no models; enter the model id manually." ;;
        000|"") warn "Could not reach $PROVIDER_LABEL to list models; enter the model id manually." ;;
        *) warn "Could not fetch models from $PROVIDER_LABEL (HTTP $last_code); enter the model id manually." ;;
    esac
    return 1
}

choose_model_from_list() {
    local default="$1" count i id choice
    count="$(printf '%s\n' "$MODEL_LIST" | awk 'NF { n++ } END { print n+0 }')"
    [ "${count:-0}" -gt 0 ] 2>/dev/null || return 1

    printf '\n%sAvailable models from %s%s\n' "$C_BOLD" "$PROVIDER_LABEL" "$C_RESET"
    i=1
    while IFS= read -r id; do
        [ -n "$id" ] || continue
        if [ "$id" = "$default" ]; then
            printf '  %s*%s %3d) %s\n' "$C_GREEN" "$C_RESET" "$i" "$id"
        else
            printf '    %3d) %s\n' "$i" "$id"
        fi
        i=$((i + 1))
    done <<EOF
$MODEL_LIST
EOF
    printf '  Type a number, an exact model id, or "m" for manual entry.\n'

    while :; do
        if [ -n "$default" ]; then
            printf 'Model [%s]: ' "$default"
        else
            printf 'Model: '
        fi
        read_line choice || die "Input closed."
        if [ -z "$choice" ]; then
            if [ -n "$default" ]; then
                MODEL="$default"
                return 0
            fi
            warn "Select a model or type its id."
            continue
        fi
        case "$choice" in
            m|M|manual) return 1 ;;
        esac
        if printf '%s\n' "$MODEL_LIST" | grep -Fqx -e "$choice"; then
            MODEL="$choice"
            return 0
        fi
        case "$choice" in
            -*) warn "Enter a number between 1 and $count, or a model id."; continue ;;
        esac
        case "$choice" in
            *[!0-9]*)
                case "$choice" in
                    *[[:space:]]*) warn "The model id cannot contain whitespace." ;;
                    *) MODEL="$choice"; return 0 ;;
                esac
                ;;
            *)
                choice=$((10#$choice))
                if [ "$choice" -ge 1 ] && [ "$choice" -le "$count" ]; then
                    MODEL="$(printf '%s\n' "$MODEL_LIST" | sed -n "${choice}p")"
                    return 0
                fi
                warn "Enter a number between 1 and $count."
                ;;
        esac
    done
}

prompt_model() {
    local model_default="${PICOCLAW_MODEL:-$DEFAULT_MODEL}"
    if fetch_model_list; then
        if choose_model_from_list "$model_default"; then
            return 0
        fi
    fi

    while :; do
        if [ -n "$model_default" ]; then
            ask_optional "Default model" "$model_default" MODEL
        else
            ask_required "Default model" MODEL
        fi
        case "$MODEL" in
            *[[:space:]]*) warn "The model id cannot contain whitespace." ;;
            "") warn "A model id is required." ;;
            *) return 0 ;;
        esac
    done
}

prompt_web_search() {
    WEB_SEARCH="none"
    WEB_API_KEY=""
    choose "Web search (optional)" "1" \
        "DuckDuckGo (zero-config)" \
        "Brave Search (API key)" \
        "Tavily (API key)" \
        "None"
    case "$CHOICE" in
        1) WEB_SEARCH="duckduckgo" ;;
        2) WEB_SEARCH="brave" ;;
        3) WEB_SEARCH="tavily" ;;
        4) WEB_SEARCH="none" ;;
    esac
    case "$WEB_SEARCH" in
        brave|tavily)
            local prompt="Brave API key: "
            [ "$WEB_SEARCH" = "tavily" ] && prompt="Tavily API key: "
            [ "$WEB_SEARCH" = "brave" ] && info "Get a key: https://api.search.brave.com/app/keys"
            [ "$WEB_SEARCH" = "tavily" ] && info "Get a key: https://app.tavily.com/home"
            if [ -n "${PICOCLAW_WEB_API_KEY:-}" ]; then
                prompt="$prompt (blank = use PICOCLAW_WEB_API_KEY): "
            fi
            while :; do
                ask_secret "$prompt" WEB_API_KEY
                [ -n "$WEB_API_KEY" ] || WEB_API_KEY="${PICOCLAW_WEB_API_KEY:-}"
                [ -n "$WEB_API_KEY" ] && break
                warn "An API key is required for this search provider."
            done
            ;;
    esac
}

prompt_channel() {
    CHANNEL_TYPE="none"
    CHANNEL_TOKEN=""
    CHANNEL_ALLOW_RAW=""
    choose "Messaging gateway (optional)" "3" \
        "Telegram bot" \
        "Discord bot" \
        "None"
    case "$CHOICE" in
        1) CHANNEL_TYPE="telegram" ;;
        2) CHANNEL_TYPE="discord" ;;
        3) CHANNEL_TYPE="none" ;;
    esac

    case "$CHANNEL_TYPE" in
        telegram)
            local token_prompt="Telegram bot token: "
            if [ -n "${PICOCLAW_TELEGRAM_TOKEN:-}" ]; then
                token_prompt="Telegram bot token (blank = use env): "
            fi
            info "Create a bot with @BotFather on Telegram."
            while :; do
                ask_secret "$token_prompt" CHANNEL_TOKEN
                [ -n "$CHANNEL_TOKEN" ] || CHANNEL_TOKEN="${PICOCLAW_TELEGRAM_TOKEN:-}"
                [ -n "$CHANNEL_TOKEN" ] && break
                warn "A Telegram bot token is required."
            done
            local allow_default="${PICOCLAW_TELEGRAM_ALLOW_FROM:-}"
            while :; do
                if [ -n "$allow_default" ]; then
                    ask_optional "Allowed Telegram user IDs (comma-separated)" "$allow_default" CHANNEL_ALLOW_RAW
                else
                    ask_required "Allowed Telegram user IDs (comma-separated)" CHANNEL_ALLOW_RAW
                fi
                if valid_ids "$CHANNEL_ALLOW_RAW"; then
                    break
                fi
                warn "Use numeric Telegram user IDs, separated by commas."
            done
            info "This allowlist prevents other Telegram users from using your bot."
            ;;
        discord)
            local token_prompt="Discord bot token: "
            if [ -n "${PICOCLAW_DISCORD_TOKEN:-}" ]; then
                token_prompt="Discord bot token (blank = use env): "
            fi
            info "Create a bot in the Discord Developer Portal."
            while :; do
                ask_secret "$token_prompt" CHANNEL_TOKEN
                [ -n "$CHANNEL_TOKEN" ] || CHANNEL_TOKEN="${PICOCLAW_DISCORD_TOKEN:-}"
                [ -n "$CHANNEL_TOKEN" ] && break
                warn "A Discord bot token is required."
            done
            ask_optional "Allowed Discord user IDs (comma-separated, optional)" "${PICOCLAW_DISCORD_ALLOW_FROM:-}" CHANNEL_ALLOW_RAW
            if [ -n "$CHANNEL_ALLOW_RAW" ]; then
                while ! valid_ids "$CHANNEL_ALLOW_RAW"; do
                    warn "Use numeric Discord user IDs, separated by commas."
                    ask_required "Allowed Discord user IDs (comma-separated)" CHANNEL_ALLOW_RAW
                done
            else
                warn "No Discord allowlist set; anyone who can invite the bot may use it."
                if ! ask_yes_no "Continue without a Discord allowlist?" "N"; then
                    ask_required "Allowed Discord user IDs (comma-separated)" CHANNEL_ALLOW_RAW
                    while ! valid_ids "$CHANNEL_ALLOW_RAW"; do
                        warn "Use numeric Discord user IDs, separated by commas."
                        ask_required "Allowed Discord user IDs (comma-separated)" CHANNEL_ALLOW_RAW
                    done
                fi
            fi
            ;;
    esac
}

run_wizard() {
    step "First-time configuration"
    info "Press Ctrl-C at any time to abort. Provider API keys are shown so you can verify the paste; other secrets stay hidden."
    prompt_provider
    prompt_api_key
    prompt_base
    prompt_model
    prompt_web_search
    prompt_tools
    prompt_channel

    local ws_default="${PICOCLAW_WORKSPACE:-$PICOCLAW_HOME/workspace}"
    while :; do
        ask_optional "Workspace directory" "$ws_default" WORKSPACE
        WORKSPACE="$(expand_path "$WORKSPACE")"
        [ -n "$WORKSPACE" ] && break
        warn "A workspace path is required."
    done
    if [ "$WORKSPACE" = "/" ]; then
        warn "Using / as the workspace makes the sandbox boundary almost meaningless."
    fi

    if ask_yes_no "Restrict file and command access to the workspace? (recommended)" "Y"; then
        RESTRICT=1
    else
        RESTRICT=0
        warn "Sandbox restriction disabled. Only do this on a trusted machine."
    fi

    printf '\n%sConfiguration summary%s\n' "$C_BOLD" "$C_RESET"
    printf '  Provider:    %s\n' "$PROVIDER_LABEL"
    printf '  Model:       %s\n' "$MODEL"
    if [ -n "$API_BASE" ]; then
        printf '  API base:    %s\n' "$API_BASE"
    else
        printf '  API base:    (provider default)\n'
    fi
    if [ -n "$AUTH_METHOD" ]; then
        printf '  Auth:        %s\n' "$AUTH_METHOD"
    fi
    printf '  Web search:  %s\n' "$WEB_SEARCH"
    printf '  Tools:       %s\n' "$(tools_summary)"
    printf '  Gateway:     %s\n' "$CHANNEL_TYPE"
    printf '  Workspace:   %s\n' "$WORKSPACE"
    printf '  Sandbox:     %s\n' "$([ "$RESTRICT" = "1" ] && printf enabled || printf disabled)"
    printf '  Config:      %s\n' "$CONFIG_FILE"

    if ! ask_yes_no "Write this configuration?" "Y"; then
        return 1
    fi
    return 0
}

load_noninteractive_settings() {
    local raw_provider
    raw_provider="${PICOCLAW_PROVIDER:-}"
    [ -n "$raw_provider" ] || die "PICOCLAW_PROVIDER is required for non-interactive configuration."
    raw_provider="$(printf '%s' "$raw_provider" | tr '[:upper:]' '[:lower:]')"
    case "$raw_provider" in
        *[!A-Za-z0-9._-]*) die "Invalid PICOCLAW_PROVIDER: $raw_provider" ;;
    esac
    PROVIDER="$(normalize_provider_id "$raw_provider")"
    if [ "$PROVIDER" = "elevenlabs" ]; then
        die "ElevenLabs is ASR-only and cannot be the main chat model; configure it under voice/ASR instead."
    fi
    if [ "$PROVIDER" = "__custom__" ]; then
        set_custom_provider
    elif ! load_provider_record "$PROVIDER"; then
        PROVIDER="$raw_provider"
        PROVIDER_LABEL="$PROVIDER"
        DEFAULT_BASE=""
        BASE_MODE="optional"
        DEFAULT_MODEL=""
        KEY_MODE="optional"
        KEY_URL=""
        AUTH_METHOD=""
        PROVIDER_NOTE=""
        warn "Unknown provider id; verify model, base URL and credentials."
    fi
    [ -n "$PROVIDER_NOTE" ] && info "$PROVIDER_NOTE"

    MODEL="${PICOCLAW_MODEL:-$DEFAULT_MODEL}"
    [ -n "$MODEL" ] || die "PICOCLAW_MODEL is required for provider $PROVIDER."
    case "$MODEL" in
        *[[:space:]]*) die "PICOCLAW_MODEL cannot contain whitespace." ;;
    esac

    API_BASE="${PICOCLAW_API_BASE:-$DEFAULT_BASE}"
    if [ "$BASE_MODE" = "required" ] && [ -z "$API_BASE" ]; then
        die "PICOCLAW_API_BASE is required for provider $PROVIDER."
    fi
    if [ -n "$API_BASE" ] && ! valid_api_base "$API_BASE"; then
        die "PICOCLAW_API_BASE must be a valid http(s) URL (AWS region allowed for bedrock)."
    fi

    if [ "$KEY_MODE" = "none" ]; then
        API_KEY=""
    else
        API_KEY="${PICOCLAW_API_KEY:-}"
    fi
    if [ "$KEY_MODE" = "required" ] && [ -z "$API_KEY" ]; then
        die "PICOCLAW_API_KEY is required for provider $PROVIDER."
    fi

    WEB_SEARCH="${PICOCLAW_WEB_SEARCH:-none}"
    WEB_SEARCH="$(printf '%s' "$WEB_SEARCH" | tr '[:upper:]' '[:lower:]')"
    case "$WEB_SEARCH" in
        none|duckduckgo) WEB_API_KEY="" ;;
        brave|tavily)
            WEB_API_KEY="${PICOCLAW_WEB_API_KEY:-}"
            [ -n "$WEB_API_KEY" ] || die "PICOCLAW_WEB_API_KEY is required for $WEB_SEARCH."
            ;;
        *) die "PICOCLAW_WEB_SEARCH must be none, duckduckgo, brave or tavily." ;;
    esac

    TOOLS_ENABLED=""
    if [ -n "${PICOCLAW_TOOLS:-}" ]; then
        set_tool_groups_from_list "$PICOCLAW_TOOLS" ||
            die "PICOCLAW_TOOLS must list optional tool groups (comma-separated), all or none."
    else
        TOOLS_ENABLED="$DEFAULT_TOOL_GROUPS"
    fi

    CHANNEL_TYPE="${PICOCLAW_CHANNEL:-none}"
    CHANNEL_TYPE="$(printf '%s' "$CHANNEL_TYPE" | tr '[:upper:]' '[:lower:]')"
    CHANNEL_TOKEN=""
    CHANNEL_ALLOW_RAW=""
    case "$CHANNEL_TYPE" in
        none) ;;
        telegram)
            CHANNEL_TOKEN="${PICOCLAW_TELEGRAM_TOKEN:-}"
            CHANNEL_ALLOW_RAW="${PICOCLAW_TELEGRAM_ALLOW_FROM:-}"
            [ -n "$CHANNEL_TOKEN" ] || die "PICOCLAW_TELEGRAM_TOKEN is required for the Telegram channel."
            [ -n "$CHANNEL_ALLOW_RAW" ] || die "PICOCLAW_TELEGRAM_ALLOW_FROM is required for the Telegram channel."
            valid_ids "$CHANNEL_ALLOW_RAW" || die "PICOCLAW_TELEGRAM_ALLOW_FROM must contain numeric user IDs."
            ;;
        discord)
            CHANNEL_TOKEN="${PICOCLAW_DISCORD_TOKEN:-}"
            CHANNEL_ALLOW_RAW="${PICOCLAW_DISCORD_ALLOW_FROM:-}"
            [ -n "$CHANNEL_TOKEN" ] || die "PICOCLAW_DISCORD_TOKEN is required for the Discord channel."
            if [ -n "$CHANNEL_ALLOW_RAW" ]; then
                valid_ids "$CHANNEL_ALLOW_RAW" || die "PICOCLAW_DISCORD_ALLOW_FROM must contain numeric user IDs."
            else
                warn "Discord allowlist is empty; anyone who can invite the bot may use it."
            fi
            ;;
        *) die "PICOCLAW_CHANNEL must be none, telegram or discord." ;;
    esac

    WORKSPACE="${PICOCLAW_WORKSPACE:-$PICOCLAW_HOME/workspace}"
    WORKSPACE="$(expand_path "$WORKSPACE")"
    RESTRICT=1
    case "${PICOCLAW_RESTRICT_WORKSPACE:-1}" in
        0|false|FALSE|no|NO) RESTRICT=0; warn "Sandbox restriction disabled via environment." ;;
    esac

    info "Provider: $PROVIDER_LABEL ($MODEL)"
    [ -n "$API_BASE" ] && info "API base: $API_BASE"
    [ -n "$AUTH_METHOD" ] && info "Auth method: $AUTH_METHOD"
    info "Web search: $WEB_SEARCH"
    info "Tools: $(tools_summary)"
    info "Gateway: $CHANNEL_TYPE"
}

# Resolve the canonical config template. Order: explicit override, local
# checkout for local archives, release tag from GitHub, local checkout
# fallback, factory defaults from the installed binary. The template is
# adapted, never copied verbatim.
prepare_config_template() {
    local candidate script_dir local_repo="" ref url dest
    CONFIG_TEMPLATE=""

    if [ -n "${PICOCLAW_CONFIG_TEMPLATE:-}" ]; then
        candidate="$(expand_path "$PICOCLAW_CONFIG_TEMPLATE")"
        [ -f "$candidate" ] || die "PICOCLAW_CONFIG_TEMPLATE is not a file: $candidate"
        CONFIG_TEMPLATE="$candidate"
        info "Config template: $CONFIG_TEMPLATE"
        return 0
    fi

    # Prefer a local checkout when the installer itself came from that checkout
    # (offline/local archive builds); otherwise use the template from the
    # release being installed so it matches the binary schema exactly.
    if [ -f "$PWD/config/config.example.json" ] && [ -f "$PWD/scripts/install.sh" ]; then
        local_repo="$PWD/config/config.example.json"
    elif [ -n "${BASH_SOURCE[0]:-}" ] && [ -f "${BASH_SOURCE[0]}" ]; then
        script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." 2>/dev/null && pwd || true)"
        candidate="$script_dir/config/config.example.json"
        if [ -f "$candidate" ]; then
            local_repo="$candidate"
        fi
    fi

    if [ -n "$LOCAL_ARCHIVE" ] && [ -n "$local_repo" ]; then
        CONFIG_TEMPLATE="$local_repo"
        info "Config template: $CONFIG_TEMPLATE"
        return 0
    fi

    if command -v curl >/dev/null 2>&1; then
        ref="${TAG:-main}"
        dest="$TMP_DIR/config.example.json"
        url="https://raw.githubusercontent.com/$REPO/$ref/config/config.example.json"
        if curl -fsSL --connect-timeout 10 --max-time 30 \
            --proto '=https' --tlsv1.2 \
            -H 'User-Agent: picoclaw-installer' \
            "$url" -o "$(to_native_path "$dest")" 2>/dev/null && [ -s "$dest" ]; then
            CONFIG_TEMPLATE="$dest"
            info "Config template: $url"
            return 0
        fi
        if [ "$ref" != "main" ]; then
            url="https://raw.githubusercontent.com/$REPO/main/config/config.example.json"
            if curl -fsSL --connect-timeout 10 --max-time 30 \
                --proto '=https' --tlsv1.2 \
                -H 'User-Agent: picoclaw-installer' \
                "$url" -o "$(to_native_path "$dest")" 2>/dev/null && [ -s "$dest" ]; then
                CONFIG_TEMPLATE="$dest"
                info "Config template: $url"
                return 0
            fi
        fi
        warn "Could not download config/config.example.json for $ref."
    fi

    if [ -n "$local_repo" ]; then
        CONFIG_TEMPLATE="$local_repo"
        info "Config template: $CONFIG_TEMPLATE"
        return 0
    fi

    # Last resort: ask the installed binary for its factory configuration.
    dest="$TMP_DIR/config.default.json"
    if PICOCLAW_HOME="$PICOCLAW_HOME" PICOCLAW_CONFIG="$dest" \
        "$BIN_PATH" --no-color config reset --force >/dev/null 2>&1 && [ -s "$dest" ]; then
        CONFIG_TEMPLATE="$dest"
        info "Config template: factory defaults from $BIN_PATH"
        return 0
    fi

    die "Could not load config/config.example.json. Set PICOCLAW_CONFIG_TEMPLATE to a local copy."
}

adapt_config_template() {
    local template="$1" output="$2" python_cmd="$3"
    PICOCLAW_CFG_TEMPLATE="$template" \
    PICOCLAW_CFG_OUTPUT="$output" \
    PICOCLAW_CFG_WORKSPACE="$(to_native_path "$WORKSPACE")" \
    PICOCLAW_CFG_RESTRICT="$RESTRICT" \
    PICOCLAW_CFG_PROVIDER="$PROVIDER" \
    PICOCLAW_CFG_MODEL="$MODEL" \
    PICOCLAW_CFG_API_KEY="$API_KEY" \
    PICOCLAW_CFG_API_BASE="$API_BASE" \
    PICOCLAW_CFG_AUTH_METHOD="$AUTH_METHOD" \
    PICOCLAW_CFG_WEB_SEARCH="$WEB_SEARCH" \
    PICOCLAW_CFG_WEB_KEY="$WEB_API_KEY" \
    PICOCLAW_CFG_CHANNEL="$CHANNEL_TYPE" \
    PICOCLAW_CFG_CHANNEL_TOKEN="$CHANNEL_TOKEN" \
    PICOCLAW_CFG_CHANNEL_ALLOW="$CHANNEL_ALLOW_RAW" \
    PICOCLAW_CFG_TOOLS="$TOOLS_ENABLED" \
    PICOCLAW_CFG_LOG_LEVEL="${PICOCLAW_LOG_LEVEL:-warn}" \
        "$python_cmd" "$TMP_DIR/adapt_config.py"
}

write_config() {
    local config_dir native_home status_out config_err python_cmd factory_template

    [ -n "$WORKSPACE" ] || die "Workspace path is empty."
    config_dir="$(dirname "$CONFIG_FILE")"
    mkdir -p "$WORKSPACE" || die "Cannot create workspace: $WORKSPACE"
    mkdir -p "$PICOCLAW_HOME" || die "Cannot create PICOCLAW_HOME: $PICOCLAW_HOME"
    mkdir -p "$config_dir" || die "Cannot create config directory: $config_dir"
    # Newly created directories already honor the installer's umask 077. Do
    # not chmod arbitrary existing parents such as /tmp, /etc or ~/.config.

    if [ -f "$config_dir/.security.yml" ]; then
        warn "A .security.yml file exists; its secrets override values in config.json."
    fi

    # Defensive defaults for variables that may not have been set.
    [ -n "${WEB_SEARCH:-}" ] || WEB_SEARCH="none"
    [ -n "${WEB_API_KEY:-}" ] || WEB_API_KEY=""
    [ -n "${CHANNEL_TYPE:-}" ] || CHANNEL_TYPE="none"
    [ -n "${CHANNEL_TOKEN:-}" ] || CHANNEL_TOKEN=""
    [ -n "${CHANNEL_ALLOW_RAW:-}" ] || CHANNEL_ALLOW_RAW=""
    [ -n "${RESTRICT:-}" ] || RESTRICT=1
    [ -n "${API_KEY:-}" ] || API_KEY=""
    [ -n "${API_BASE:-}" ] || API_BASE=""
    [ -n "${AUTH_METHOD:-}" ] || AUTH_METHOD=""

    python_cmd="$(command -v python3 2>/dev/null || true)"
    if [ -z "$python_cmd" ]; then
        warn "python3 is required to adapt config/config.example.json to your choices."
        warn "Install python3 and rerun the installer, or create $CONFIG_FILE manually."
        return 1
    fi

    prepare_config_template

    native_home="$(to_native_path "$PICOCLAW_HOME")"
    TMP_CONFIG="$(mktemp "${CONFIG_FILE}.tmp.XXXXXX" 2>/dev/null || true)"
    [ -n "$TMP_CONFIG" ] || die "Cannot create a temporary config file next to $CONFIG_FILE."

    cat >"$TMP_DIR/adapt_config.py" <<'PY'
import json
import os
import sys


def env(name, default=""):
    value = os.environ.get(name)
    return default if value is None else value


def is_placeholder(value):
    text = value.strip()
    if not text:
        return False
    lowered = text.lower()
    return (
        text.startswith("YOUR_")
        or text.startswith("sk-")
        or "your" in lowered
        or text in ("pplx-xxx", "ctx7sk-xx")
    )


def clean(value):
    if isinstance(value, dict):
        return {
            key: clean(item)
            for key, item in value.items()
            if not key.startswith("_comment")
        }
    if isinstance(value, list):
        return [clean(item) for item in value if not (isinstance(item, str) and is_placeholder(item))]
    if isinstance(value, str) and is_placeholder(value):
        return ""
    return value


def main():
    source = env("PICOCLAW_CFG_TEMPLATE")
    destination = env("PICOCLAW_CFG_OUTPUT")
    if not source or not destination:
        raise SystemExit("missing template or destination path")

    with open(source, "r", encoding="utf-8") as handle:
        config = clean(json.load(handle))

    defaults = config.setdefault("agents", {}).setdefault("defaults", {})
    defaults["workspace"] = env("PICOCLAW_CFG_WORKSPACE")
    defaults["restrict_to_workspace"] = env("PICOCLAW_CFG_RESTRICT", "1") == "1"
    defaults["model_name"] = "default"

    model = {
        "model_name": "default",
        "provider": env("PICOCLAW_CFG_PROVIDER"),
        "model": env("PICOCLAW_CFG_MODEL"),
        "enabled": True,
    }
    api_key = env("PICOCLAW_CFG_API_KEY")
    api_base = env("PICOCLAW_CFG_API_BASE")
    auth_method = env("PICOCLAW_CFG_AUTH_METHOD")
    if api_key:
        model["api_keys"] = [api_key]
    if api_base:
        model["api_base"] = api_base
    if auth_method:
        model["auth_method"] = auth_method
    config["model_list"] = [model]

    channels = config.setdefault("channel_list", {})
    for channel in channels.values():
        if isinstance(channel, dict):
            channel["enabled"] = False

    channel_type = env("PICOCLAW_CFG_CHANNEL", "none").lower()
    if channel_type not in ("", "none"):
        channel = channels.setdefault(channel_type, {})
        channel["enabled"] = True
        channel["type"] = channel_type
        channel["allow_from"] = [
            item for item in env("PICOCLAW_CFG_CHANNEL_ALLOW").replace(",", " ").split() if item
        ]
        settings = channel.get("settings")
        if not isinstance(settings, dict):
            settings = {}
            channel["settings"] = settings
        settings["token"] = env("PICOCLAW_CFG_CHANNEL_TOKEN")

    tools = config.setdefault("tools", {})
    selected = set(env("PICOCLAW_CFG_TOOLS").split())
    groups = {
        "web_fetch": ("web_fetch",),
        "vision": ("load_image",),
        "image_generation": ("image_generate",),
        "media": ("send_file",),
        "voice": ("send_tts",),
        "reaction": ("reaction",),
        "cron": ("cron",),
        "todo": ("todo",),
        "mcp": ("mcp",),
        "hardware": ("i2c", "serial", "spi"),
    }
    for group, names in groups.items():
        for name in names:
            node = tools.setdefault(name, {})
            if isinstance(node, dict):
                node["enabled"] = group in selected

    for name in (
        "read_file",
        "write_file",
        "append_file",
        "edit_file",
        "list_dir",
        "search_files",
        "skills",
        "find_skills",
        "install_skill",
        "subagent",
        "spawn",
        "spawn_status",
        "message",
    ):
        node = tools.setdefault(name, {})
        if isinstance(node, dict):
            node["enabled"] = True

    web = tools.setdefault("web", {})
    search = env("PICOCLAW_CFG_WEB_SEARCH", "none").lower()
    web["enabled"] = search != "none"
    web["provider"] = "auto" if search == "none" else search
    # Honor the provider selected in the wizard instead of silently preferring
    # a model's native search implementation.
    web["prefer_native"] = False
    web_key = env("PICOCLAW_CFG_WEB_KEY")
    for name, node in list(web.items()):
        if isinstance(node, dict):
            # Legacy single-key field, rejected by strict config loading.
            node.pop("api_key", None)
    for name in ("brave", "tavily", "duckduckgo"):
        node = web.setdefault(name, {})
        if not isinstance(node, dict):
            node = {}
            web[name] = node
        node["enabled"] = name == search
        if name == search and name in ("brave", "tavily") and web_key:
            node["api_keys"] = [web_key]
        else:
            node.pop("api_keys", None)

    gateway = config.setdefault("gateway", {})
    gateway.pop("_comment", None)
    level = env("PICOCLAW_CFG_LOG_LEVEL", "warn").lower()
    if level == "warning":
        level = "warn"
    if level not in ("debug", "info", "warn", "error", "fatal"):
        level = "warn"
    gateway["log_level"] = level

    with open(destination, "w", encoding="utf-8") as handle:
        json.dump(config, handle, ensure_ascii=False, indent=2)
        handle.write("\n")


if __name__ == "__main__":
    try:
        main()
    except Exception as exc:  # noqa: BLE001 - surface the error to the installer
        sys.stderr.write("config adaptation failed: %s\n" % exc)
        sys.exit(1)
PY

    adapt_config_template "$CONFIG_TEMPLATE" "$TMP_CONFIG" "$python_cmd" ||
        die "Could not adapt the config template: $CONFIG_TEMPLATE"

    chmod 600 "$TMP_CONFIG" 2>/dev/null || true
    validate_json_file "$TMP_CONFIG"

    # The repository example can be newer than the installed release. If the
    # binary rejects unknown fields, retry with the binary's factory defaults
    # instead of writing a config that PicoClaw cannot load.
    status_out="$(PICOCLAW_HOME="$native_home" PICOCLAW_CONFIG="$TMP_CONFIG" \
        "$BIN_PATH" --no-color status 2>&1 || true)"
    config_err="$(printf '%s\n' "$status_out" | grep -m1 -i 'error loading config' || true)"
    if [ -n "$config_err" ]; then
        warn "The config template does not match this PicoClaw build; using factory defaults."
        factory_template="$TMP_DIR/config.default.json"
        if ! PICOCLAW_HOME="$native_home" PICOCLAW_CONFIG="$factory_template" \
            "$BIN_PATH" --no-color config reset --force >/dev/null 2>&1 || [ ! -s "$factory_template" ]; then
            die "PicoClaw rejected the generated config and factory defaults are unavailable: $config_err"
        fi
        CONFIG_TEMPLATE="$factory_template"
        adapt_config_template "$CONFIG_TEMPLATE" "$TMP_CONFIG" "$python_cmd" ||
            die "Could not adapt the factory config defaults."
        chmod 600 "$TMP_CONFIG" 2>/dev/null || true
        validate_json_file "$TMP_CONFIG"
        status_out="$(PICOCLAW_HOME="$native_home" PICOCLAW_CONFIG="$TMP_CONFIG" \
            "$BIN_PATH" --no-color status 2>&1 || true)"
        config_err="$(printf '%s\n' "$status_out" | grep -m1 -i 'error loading config' || true)"
        if [ -n "$config_err" ]; then
            die "PicoClaw rejected the generated config: $config_err"
        fi
        info "Config template: factory defaults from $BIN_PATH"
    fi

    mv -f "$TMP_CONFIG" "$CONFIG_FILE" || die "Cannot move config into place: $CONFIG_FILE"
    TMP_CONFIG=""
    chmod 600 "$CONFIG_FILE" 2>/dev/null || true
    ok "Configuration written: $CONFIG_FILE"
}

validate_json_file() {
    local file="$1"
    if command -v python3 >/dev/null 2>&1; then
        python3 -m json.tool "$file" >/dev/null 2>&1 || die "Generated config is not valid JSON."
    elif command -v jq >/dev/null 2>&1; then
        jq -e . "$file" >/dev/null 2>&1 || die "Generated config is not valid JSON."
    elif command -v node >/dev/null 2>&1; then
        node -e 'JSON.parse(require("fs").readFileSync(process.argv[1], "utf8"))' "$file" >/dev/null 2>&1 ||
            die "Generated config is not valid JSON."
    elif command -v plutil >/dev/null 2>&1; then
        plutil -lint -s "$file" >/dev/null 2>&1 || die "Generated config is not valid JSON."
    fi
}

backup_config() {
    local backup
    [ -f "$CONFIG_FILE" ] || return 0
    backup="${CONFIG_FILE}.bak.$(date +%Y%m%d-%H%M%S).$$"
    cp -p "$CONFIG_FILE" "$backup" || die "Cannot back up existing config: $CONFIG_FILE"
    chmod 600 "$backup" 2>/dev/null || true
    ok "Backup created: $backup"
}

maybe_write_config() {
    local replace=0
    if [ -e "$CONFIG_FILE" ]; then
        if [ "$FORCE_CONFIG" = "1" ]; then
            replace=1
        elif [ "$NON_INTERACTIVE" = "1" ]; then
            info "Existing config kept: $CONFIG_FILE"
            CONFIG_ACTION="kept"
            return 0
        else
            warn "Existing config found: $CONFIG_FILE"
            if ask_yes_no "Back up and replace it?" "N"; then
                replace=1
            else
                info "Keeping existing config."
                CONFIG_ACTION="kept"
                return 0
            fi
        fi
    fi

    if [ "$NON_INTERACTIVE" = "1" ]; then
        if [ -z "${PICOCLAW_PROVIDER:-}" ]; then
            if [ "$FORCE_CONFIG" = "1" ]; then
                die "PICOCLAW_PROVIDER is required with --force-config in non-interactive mode."
            fi
            warn "No PICOCLAW_PROVIDER set; skipping config generation in non-interactive mode."
            CONFIG_ACTION="skipped"
            return 0
        fi
        [ "$replace" = "1" ] && backup_config
        load_noninteractive_settings
        if write_config; then
            CONFIG_ACTION="written"
        else
            CONFIG_ACTION="skipped"
        fi
        return 0
    fi

    [ "$replace" = "1" ] && backup_config
    if run_wizard; then
        if write_config; then
            CONFIG_ACTION="written"
        else
            CONFIG_ACTION="skipped"
        fi
    else
        warn "Configuration not written."
        if [ -e "$CONFIG_FILE" ]; then
            CONFIG_ACTION="kept"
        else
            CONFIG_ACTION="skipped"
        fi
    fi
}

# ---------------------------------------------------------------------------
# Final report
# ---------------------------------------------------------------------------

print_success() {
    local profile_hint cmd_q launcher_q
    case "${SHELL:-}" in
        */zsh) profile_hint="$HOME/.zshrc" ;;
        */bash) profile_hint="$HOME/.bashrc" ;;
        *) if [ -f "$HOME/.profile" ]; then
               profile_hint="$HOME/.profile"
           else
               profile_hint="$HOME/.bashrc"
           fi
           ;;
    esac
    if [ "$MODIFY_PATH" = "0" ] || [ "$PATH_CONFIGURED" = "0" ]; then
        PICOCLAW_CMD="$BIN_PATH"
    else
        case ":$PATH:" in
            *":$INSTALL_DIR:"*) PICOCLAW_CMD="picoclaw" ;;
            *) PICOCLAW_CMD="$BIN_PATH" ;;
        esac
    fi
    cmd_q="$(quote_cmd "$PICOCLAW_CMD")"
    launcher_q="$(quote_cmd "$INSTALL_DIR/$LAUNCHER_NAME")"

    printf '\n%s+ PicoClaw installed%s\n\n' "$C_GREEN$C_BOLD" "$C_RESET"
    printf '  Version    %s\n' "${VERSION_LINE:-PicoClaw}"
    if [ "$CONFIG_ACTION" = "written" ]; then
        printf '  Provider   %s\n' "$PROVIDER_LABEL"
        printf '  Model      %s\n' "$MODEL"
        if [ -n "$AUTH_METHOD" ]; then
            printf '  Auth       %s\n' "$AUTH_METHOD"
        fi
        printf '  Tools      %s\n' "$(tools_summary)"
    fi
    printf '  Binary     %s\n' "$BIN_PATH"
    case "$CONFIG_ACTION" in
        written) printf '  Config     %s\n' "$CONFIG_FILE" ;;
        kept) printf '  Config     existing %s (kept)\n' "$CONFIG_FILE" ;;
        *) printf '  Config     not created\n' ;;
    esac
    if [ "$CONFIG_ACTION" = "written" ]; then
        printf '  Workspace  %s\n' "$WORKSPACE"
        printf '  Sandbox    %s\n' "$([ "${RESTRICT:-1}" = "1" ] && printf enabled || printf disabled)"
    fi
    if [ "$LAUNCHER_INSTALLED" = "1" ]; then
        printf '  Web UI     %s\n' "$INSTALL_DIR/$LAUNCHER_NAME"
    fi

    printf '\n%sQuick start%s\n' "$C_BOLD" "$C_RESET"
    printf '  %s agent      # interactive CLI chat\n' "$cmd_q"
    printf '  %s gateway    # messaging gateway (if configured)\n' "$cmd_q"
    if [ "$LAUNCHER_INSTALLED" = "1" ]; then
        printf '  %s  # browser UI on http://localhost:18800\n' "$launcher_q"
    fi

    if [ "$MODIFY_PATH" = "1" ] && [ "$PATH_CONFIGURED" = "1" ]; then
        printf '\nRestart your shell or run:\n  source "%s"\n' "$profile_hint"
    else
        printf '\nAdd PicoClaw to PATH:\n  export PATH="%s:$PATH"\n' "$INSTALL_DIR"
    fi

    if [ "$CONFIG_ACTION" = "skipped" ]; then
        printf '\n'
        warn "No config was written. Re-run in a terminal, or set PICOCLAW_PROVIDER/PICOCLAW_API_KEY and use --non-interactive."
    fi
    if [ "$PICOCLAW_HOME" != "$DEFAULT_PICOCLAW_HOME" ]; then
        printf '\n'
        info "Custom home. Export PICOCLAW_HOME=\"$PICOCLAW_HOME\" before running PicoClaw."
    fi
    if [ "$CONFIG_FILE" != "$PICOCLAW_HOME/config.json" ]; then
        printf '\n'
        info "Custom config path. Export PICOCLAW_CONFIG=\"$CONFIG_FILE\" before running PicoClaw."
    fi
    if [ "$OS" = "Windows" ]; then
        printf '\n'
        info "Windows: run PicoClaw from Git Bash/MSYS2, or use WSL. Native PowerShell PATH was not modified."
        info "Windows: config secrecy relies on your user-profile ACLs (chmod is best-effort)."
    fi
    if [ "$CONFIG_ACTION" = "written" ]; then
        if [ -n "$PROVIDER_NOTE" ]; then
            printf '\n'
            info "$PROVIDER_NOTE"
        fi
        printf '\n'
        info "Secrets are stored in $CONFIG_FILE with mode 600 where supported."
    fi
    printf '\n'
}

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

main() {
    print_banner
    step "Environment"
    resolve_paths
    detect_platform
    check_tools
    prepare_install_dir
    make_tmp

    step "Release"
    get_archive

    step "Install"
    extract_archive
    install_binaries

    step "PATH"
    configure_path

    step "Verify"
    sanity_check

    step "Configuration"
    maybe_write_config

    print_success
}

main
