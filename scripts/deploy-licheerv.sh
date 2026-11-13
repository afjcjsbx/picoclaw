#!/usr/bin/env bash
#
# Build PicoClaw for a supported board and deploy it over SSH, then restart
# the gateway.
#
# Targets:
#   lichee   LicheeRV Nano (SG2002, riscv64)  - init.d, SSH as root
#   rpi2     Raspberry Pi 2 Model B (armv7)   - systemd, SSH + sudo
#
# The script stops every picoclaw process on the board, installs the new
# binaries atomically (keeping .prev backups), starts the launcher (which
# auto-starts the gateway) and waits until the gateway reports "ready".
#
# Usage:
#   scripts/deploy-licheerv.sh [options]
#
# Options:
#   -t, --target NAME     deployment target: lichee (default) or rpi2
#   -H, --host HOST       SSH host (default: per target, or PICOCLAW_DEPLOY_HOST)
#   -u, --user USER       SSH user (default: per target, or PICOCLAW_DEPLOY_USER)
#   -p, --password PASS   SSH/sudo password (default: per target, env
#                         PICOCLAW_DEPLOY_PASSWORD, or ~/.config/picoclaw/deploy.env)
#       --gomaxprocs N    GOMAXPROCS for launcher/gateway (default: 4 or
#                         PICOCLAW_DEPLOY_GOMAXPROCS)
#       --no-build        reuse existing build/picoclaw-linux-<arch> artifacts
#       --no-launcher     do not build/install picoclaw-launcher
#       --rollback        restore *.prev binaries on the board and restart
#       --check           run preflight checks on the board and exit
#       --timeout SECS    gateway ready timeout (default: 300)
#   -h, --help            show this help
#
# SSH auth: an existing SSH key is used when available, otherwise the
# password is supplied through SSH_ASKPASS (fallback: sshpass). On systemd
# targets the same password is used for remote sudo.
#
# Per-target defaults can be overridden in ~/.config/picoclaw/deploy.env:
#   PICOCLAW_DEPLOY_RPI2_HOST=board.example
#   PICOCLAW_DEPLOY_RPI2_USER=pi
#   PICOCLAW_DEPLOY_RPI2_PASSWORD=...
# Keep that file chmod 600 (it is outside the repository on purpose).

set -euo pipefail

TARGET_NAME=""
DEF_HOST=""
DEF_USER=""
DEF_PASSWORD=""
EXPECTED_ARCH=""
BUILD_GOARCH=""
BUILD_GOARM=""
SERVICE_MODE=""
SERVICE_NAME="picoclaw-launcher"

HOST=""
USER=""
PASSWORD=""
HOST_SET=0
USER_SET=0
PASSWORD_SET=0
GOMAXPROCS_VALUE=4
GOMAXPROCS_SET=0
DO_BUILD=1
DO_LAUNCHER=1
ROLLBACK=0
CHECK_ONLY=0
TIMEOUT=300

REMOTE_DIR=/opt/picoclaw
PICO_HOME=/root/.picoclaw
GW_PORT=18791

die() { echo "error: $*" >&2; exit 1; }
usage() {
	cat <<'EOF'
Build PicoClaw for a supported board and deploy it over SSH, then restart the gateway.

Targets:
  lichee   LicheeRV Nano (SG2002, riscv64)  - init.d, SSH as root
  rpi2     Raspberry Pi 2 Model B (armv7)   - systemd, SSH + sudo

Usage:
  scripts/deploy-licheerv.sh [options]

Options:
  -t, --target NAME     deployment target: lichee (default) or rpi2
  -H, --host HOST       SSH host (default: per target, or PICOCLAW_DEPLOY_HOST)
  -u, --user USER       SSH user (default: per target, or PICOCLAW_DEPLOY_USER)
  -p, --password PASS   SSH/sudo password (default: per target, env
                        PICOCLAW_DEPLOY_PASSWORD, or ~/.config/picoclaw/deploy.env)
      --gomaxprocs N    GOMAXPROCS for launcher/gateway (default: 4 or
                        PICOCLAW_DEPLOY_GOMAXPROCS)
      --no-build        reuse existing build/picoclaw-linux-<arch> artifacts
      --no-launcher     do not build/install picoclaw-launcher
      --rollback        restore *.prev binaries on the board and restart
      --check           run preflight checks on the board and exit
      --timeout SECS    gateway ready timeout (default: 300)
  -h, --help            show this help

SSH auth: an existing SSH key is used when available, otherwise the password is
supplied through SSH_ASKPASS (fallback: sshpass). On systemd targets the same
password is used for remote sudo.
EOF
}

CONFIG_FILE="${PICOCLAW_DEPLOY_CONFIG:-$HOME/.config/picoclaw/deploy.env}"
if [[ -f "$CONFIG_FILE" ]]; then
	cfg_mode=$(stat -f '%Lp' "$CONFIG_FILE" 2>/dev/null || stat -c '%a' "$CONFIG_FILE" 2>/dev/null || true)
	if [[ -n "$cfg_mode" && "$cfg_mode" != "600" && "$cfg_mode" != "400" ]]; then
		echo "warning: $CONFIG_FILE is accessible by other users (mode $cfg_mode); run: chmod 600 $CONFIG_FILE" >&2
	fi
	# shellcheck disable=SC1090
	. "$CONFIG_FILE"
fi

while [[ $# -gt 0 ]]; do
	case "$1" in
	-t | --target) TARGET_NAME="$2"; shift 2 ;;
	-H | --host) HOST="$2"; HOST_SET=1; shift 2 ;;
	-u | --user) USER="$2"; USER_SET=1; shift 2 ;;
	-p | --password) PASSWORD="$2"; PASSWORD_SET=1; shift 2 ;;
	--gomaxprocs) GOMAXPROCS_VALUE="$2"; GOMAXPROCS_SET=1; shift 2 ;;
	--no-build) DO_BUILD=0; shift ;;
	--no-launcher) DO_LAUNCHER=0; shift ;;
	--rollback) ROLLBACK=1; shift ;;
	--check) CHECK_ONLY=1; shift ;;
	--timeout) TIMEOUT="$2"; shift 2 ;;
	-h | --help) usage; exit 0 ;;
	*) die "unknown option: $1 (try --help)" ;;
	esac
done

TARGET_NAME="${TARGET_NAME:-${PICOCLAW_DEPLOY_TARGET:-lichee}}"

case "$TARGET_NAME" in
lichee | lichee-rv | licheerv | licheerv-nano)
	TARGET_NAME=lichee
	DEF_HOST=""
	DEF_USER=root
	DEF_PASSWORD=""
	EXPECTED_ARCH=riscv64
	BUILD_GOARCH=riscv64
	BUILD_GOARM=""
	SERVICE_MODE=initd
	;;
rpi | rpi2 | pi | pi2 | raspberry | raspberrypi | raspberry-pi)
	TARGET_NAME=rpi2
	DEF_HOST=""
	DEF_USER=""
	DEF_PASSWORD=""
	EXPECTED_ARCH=armv7l
	BUILD_GOARCH=arm
	BUILD_GOARM=7
	SERVICE_MODE=systemd
	;;
*) die "unknown target: $TARGET_NAME (choose lichee or rpi2)" ;;
esac

TARGET_UPPER=$(printf '%s' "$TARGET_NAME" | tr '[:lower:]' '[:upper:]')
target_var() { printf 'PICOCLAW_DEPLOY_%s_%s' "$TARGET_UPPER" "$1"; }

if [[ $HOST_SET -eq 0 ]]; then
	host_var=$(target_var HOST)
	if [[ -n "${PICOCLAW_DEPLOY_HOST:-}" ]]; then
		HOST="$PICOCLAW_DEPLOY_HOST"
	elif [[ -n "${!host_var:-}" ]]; then
		HOST="${!host_var}"
	else
		HOST="$DEF_HOST"
	fi
fi

if [[ $USER_SET -eq 0 ]]; then
	user_var=$(target_var USER)
	if [[ -n "${PICOCLAW_DEPLOY_USER:-}" ]]; then
		USER="$PICOCLAW_DEPLOY_USER"
	elif [[ -n "${!user_var:-}" ]]; then
		USER="${!user_var}"
	else
		USER="$DEF_USER"
	fi
fi

if [[ $PASSWORD_SET -eq 0 ]]; then
	pw_var=$(target_var PASSWORD)
	if [[ -n "${PICOCLAW_DEPLOY_PASSWORD:-}" ]]; then
		PASSWORD="$PICOCLAW_DEPLOY_PASSWORD"
	elif [[ -n "${!pw_var:-}" ]]; then
		PASSWORD="${!pw_var}"
	else
		PASSWORD="$DEF_PASSWORD"
	fi
fi

[[ -n "$HOST" ]] || die "missing deployment host (use --host or $CONFIG_FILE)"
[[ -n "$USER" ]] || die "missing deployment user (use --user or $CONFIG_FILE)"

if [[ $GOMAXPROCS_SET -eq 0 && -n "${PICOCLAW_DEPLOY_GOMAXPROCS:-}" ]]; then
	GOMAXPROCS_VALUE="$PICOCLAW_DEPLOY_GOMAXPROCS"
fi

[[ "$GOMAXPROCS_VALUE" =~ ^[0-9]+$ ]] && [[ "$GOMAXPROCS_VALUE" -ge 1 ]] ||
	die "invalid --gomaxprocs: $GOMAXPROCS_VALUE"
[[ "$TIMEOUT" =~ ^[0-9]+$ ]] && [[ "$TIMEOUT" -ge 1 ]] ||
	die "invalid --timeout: $TIMEOUT"

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CORE_BIN="$ROOT/build/picoclaw-linux-$BUILD_GOARCH"
LAUNCHER_BIN="$ROOT/build/picoclaw-launcher-linux-$BUILD_GOARCH"

REMOTE_STAGE=""
REMOTE_SCRIPT=""

TMP="$(mktemp -d)"

SSH_OPTS=(-o StrictHostKeyChecking=accept-new -o LogLevel=ERROR -o ConnectTimeout=10)
AUTH_MODE=""

detect_auth() {
	if ssh "${SSH_OPTS[@]}" -o BatchMode=yes -o ConnectTimeout=8 "$USER@$HOST" true >/dev/null 2>&1; then
		AUTH_MODE=key
		echo "auth: ssh key"
		return
	fi

	if [[ -z "$PASSWORD" ]]; then
		if [[ -t 0 ]]; then
			read -rsp "SSH password for $USER@$HOST: " PASSWORD
			echo >&2
		else
			die "cannot authenticate to $USER@$HOST: no working key and no password (use -p or $CONFIG_FILE)"
		fi
	fi

	cat >"$TMP/askpass.sh" <<'ASKPASS_EOF'
#!/bin/sh
printf '%s\n' "$PICOCLAW_ASKPASS_PW"
ASKPASS_EOF
	chmod +x "$TMP/askpass.sh"
	export SSH_ASKPASS="$TMP/askpass.sh"
	export SSH_ASKPASS_REQUIRE=force
	export DISPLAY=:0
	export PICOCLAW_ASKPASS_PW="$PASSWORD"

	if ssh "${SSH_OPTS[@]}" -o BatchMode=no -o NumberOfPasswordPrompts=1 "$USER@$HOST" true </dev/null >/dev/null 2>&1; then
		AUTH_MODE=askpass
		echo "auth: ssh password (askpass)"
		return
	fi

	if command -v sshpass >/dev/null 2>&1; then
		AUTH_MODE=sshpass
		echo "auth: ssh password (sshpass)"
		return
	fi

	die "cannot authenticate to $USER@$HOST (no working key, askpass or sshpass)"
}

run_ssh() {
	case "$AUTH_MODE" in
	key) ssh "${SSH_OPTS[@]}" -o BatchMode=yes "$USER@$HOST" "$1" ;;
	askpass) ssh "${SSH_OPTS[@]}" -o BatchMode=no -o NumberOfPasswordPrompts=1 "$USER@$HOST" "$1" </dev/null ;;
	sshpass) sshpass -p "$PASSWORD" ssh "${SSH_OPTS[@]}" "$USER@$HOST" "$1" ;;
	*) die "internal error: auth not initialized" ;;
	esac
}

run_ssh_stdin() {
	local input="$1"
	local cmd="$2"
	case "$AUTH_MODE" in
	key) printf '%s\n' "$input" | ssh "${SSH_OPTS[@]}" -o BatchMode=yes "$USER@$HOST" "$cmd" ;;
	askpass) printf '%s\n' "$input" | ssh "${SSH_OPTS[@]}" -o BatchMode=no -o NumberOfPasswordPrompts=1 "$USER@$HOST" "$cmd" ;;
	sshpass) printf '%s\n' "$input" | sshpass -p "$PASSWORD" ssh "${SSH_OPTS[@]}" "$USER@$HOST" "$cmd" ;;
	*) die "internal error: auth not initialized" ;;
	esac
}

upload() {
	case "$AUTH_MODE" in
	key) scp "${SSH_OPTS[@]}" -q "$1" "$USER@$HOST:$2" ;;
	askpass) scp "${SSH_OPTS[@]}" -q "$1" "$USER@$HOST:$2" </dev/null ;;
	sshpass) sshpass -p "$PASSWORD" scp "${SSH_OPTS[@]}" -q "$1" "$USER@$HOST:$2" ;;
	esac
}

cleanup_remote() {
	if [[ -n "$AUTH_MODE" && -n "$REMOTE_STAGE" ]]; then
		if [[ "$REMOTE_STAGE" == "$REMOTE_DIR" ]]; then
			run_ssh "rm -f '$REMOTE_SCRIPT'" >/dev/null 2>&1 || true
		else
			run_ssh "rm -rf '$REMOTE_STAGE'" >/dev/null 2>&1 || true
		fi
	fi
	rm -rf "$TMP"
}
trap cleanup_remote EXIT

ensure_sudo() {
	if run_ssh "sudo -n true" >/dev/null 2>&1; then
		echo "sudo: passwordless"
		return
	fi
	if [[ -z "$PASSWORD" ]]; then
		if [[ -t 0 ]]; then
			read -rsp "sudo password for $USER@$HOST: " PASSWORD
			echo >&2
		else
			die "remote sudo needs a password (use -p or $CONFIG_FILE)"
		fi
	fi
	run_ssh_stdin "$PASSWORD" "sudo -H -S -p '' true" >/dev/null ||
		die "sudo authentication failed on $HOST"
	echo "sudo: password accepted"
}

sha256_local() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{print $1}'
	else
		shasum -a 256 "$1" | awk '{print $1}'
	fi
}

cat >"$TMP/remote.sh" <<'REMOTE_EOF'
#!/bin/sh
set -u

MODE=${MODE:-deploy}
VER=${VER:-}
INSTALL_LAUNCHER=${INSTALL_LAUNCHER:-1}
CORE_SHA=${CORE_SHA:-}
LAUNCHER_SHA=${LAUNCHER_SHA:-}
READY_TIMEOUT=${READY_TIMEOUT:-300}
GOMAXPROCS=${GOMAXPROCS:-4}
BIN_DIR=${BIN_DIR:-/opt/picoclaw}
PICO_HOME=${PICO_HOME:-/root/.picoclaw}
GW_PORT=${GW_PORT:-18791}
SERVICE_MODE=${SERVICE_MODE:-initd}
SERVICE_NAME=${SERVICE_NAME:-picoclaw-launcher}
STAGE=${STAGE:-$BIN_DIR}

note() { echo "[remote] $*"; }
die() { echo "[remote] ERROR: $*" >&2; exit 1; }

if command -v wget >/dev/null 2>&1; then
	http_get() { wget -qO- -T 8 "$1" 2>/dev/null; }
elif command -v curl >/dev/null 2>&1; then
	http_get() { curl -fsS -m 8 "$1" 2>/dev/null; }
else
	die "wget or curl required on the board"
fi

proc_list() {
	for e in /proc/[0-9]*/exe; do
		t=$(readlink "$e" 2>/dev/null) || continue
		case "$t" in
		"$BIN_DIR"/*)
			p=${e#/proc/}
			p=${p%/exe}
			echo "$p $t"
			;;
		esac
	done
}

stop_all() {
	note "stopping all picoclaw instances ($SERVICE_MODE)"
	case "$SERVICE_MODE" in
	systemd)
		systemctl stop "$SERVICE_NAME" >/dev/null 2>&1 || true
		;;
	initd)
		if [ -x /etc/init.d/S98picoclaw_launcher ]; then
			/etc/init.d/S98picoclaw_launcher stop >/dev/null 2>&1 || true
		fi
		if [ -x /etc/init.d/S99picoclaw-gateway ]; then
			/etc/init.d/S99picoclaw-gateway stop >/dev/null 2>&1 || true
		fi
		;;
	*)
		die "unknown service mode: $SERVICE_MODE"
		;;
	esac
	sleep 1
	for e in /proc/[0-9]*/exe; do
		t=$(readlink "$e" 2>/dev/null) || continue
		case "$t" in
		"$BIN_DIR"/*)
			p=${e#/proc/}
			p=${p%/exe}
			kill "$p" 2>/dev/null || true
			;;
		esac
	done
	sleep 2
	for e in /proc/[0-9]*/exe; do
		t=$(readlink "$e" 2>/dev/null) || continue
		case "$t" in
		"$BIN_DIR"/*)
			p=${e#/proc/}
			p=${p%/exe}
			kill -9 "$p" 2>/dev/null || true
			;;
		esac
	done
	sleep 1
	left=$(proc_list)
	if [ -n "$left" ]; then
		echo "$left"
		die "instances still running after stop"
	fi
	note "no picoclaw process running"
}

install_bin() {
	src=$1
	dst=$2
	want=$3
	[ -f "$src" ] || die "missing uploaded file: $src"
	got=$(sha256sum "$src" | awk '{print $1}')
	[ "$got" = "$want" ] || die "sha256 mismatch for $dst: got $got want $want"
	if [ "$(dirname "$src")" = "$(dirname "$dst")" ]; then
		tmp=$src
	else
		tmp="$dst.new.$$"
		rm -f "$dst".new.* 2>/dev/null || true
		cp -f "$src" "$tmp" || die "failed to stage $dst"
		got=$(sha256sum "$tmp" | awk '{print $1}')
		if [ "$got" != "$want" ]; then
			rm -f "$tmp"
			die "sha256 mismatch after copy for $dst: got $got want $want"
		fi
	fi
	chmod +x "$tmp"
	if [ -f "$dst" ]; then
		cp -p "$dst" "$dst.prev" || die "failed to backup $dst"
	fi
	mv -f "$tmp" "$dst" || die "failed to install $dst"
	chmod 755 "$dst"
	note "installed $dst sha256=$got"
}

start_all() {
	export GOMAXPROCS
	case "$SERVICE_MODE" in
	systemd)
		unit_dir="/etc/systemd/system/$SERVICE_NAME.service.d"
		mkdir -p "$unit_dir" || die "failed to create $unit_dir"
		cat >"$unit_dir/gomaxprocs.conf" <<EOF
[Service]
Environment=GOMAXPROCS=$GOMAXPROCS
EOF
		systemctl daemon-reload || die "systemctl daemon-reload failed"
		note "starting $SERVICE_NAME with GOMAXPROCS=$GOMAXPROCS"
		systemctl start "$SERVICE_NAME" >/dev/null 2>&1 || die "failed to start $SERVICE_NAME"
		;;
	initd)
		note "starting launcher (gateway is auto-started) with GOMAXPROCS=$GOMAXPROCS"
		/etc/init.d/S98picoclaw_launcher start >/dev/null 2>&1 || die "failed to start launcher"
		;;
	*)
		die "unknown service mode: $SERVICE_MODE"
		;;
	esac
}

wait_ready() {
	i=0
	while [ "$i" -lt "$READY_TIMEOUT" ]; do
		resp=$(http_get "http://127.0.0.1:$GW_PORT/ready")
		case "$resp" in
		*'"ready"'*)
			note "gateway ready after ${i}s"
			return 0
			;;
		esac
		sleep 5
		i=$((i + 5))
		note "waiting for gateway... ${i}s"
	done
	return 1
}

verify_running() {
	procs=$(proc_list)
	note "running instances:"
	echo "$procs" | sed 's/^/[remote]   /'
	gw=$(echo "$procs" | grep -c "$BIN_DIR/picoclaw\$")
	lc=$(echo "$procs" | grep -c "$BIN_DIR/picoclaw-launcher\$")
	[ "$gw" = "1" ] || die "expected exactly 1 gateway, found $gw"
	[ "$lc" = "1" ] || die "expected exactly 1 launcher, found $lc"
}

verify_version() {
	out=$("$BIN_DIR/picoclaw" version 2>/dev/null)
	echo "$out" | grep -q -F "$VER" || die "running version does not match $VER"
	pidver=$(grep -o '"version": "[^"]*"' "$PICO_HOME/.picoclaw.pid" 2>/dev/null | cut -d'"' -f4)
	[ "$pidver" = "$VER" ] || die "pid file version '$pidver' != '$VER'"
	note "version verified: $VER"
}

tail_logs() {
	note "last gateway log lines:"
	tail -n 25 "$PICO_HOME/logs/gateway.log" 2>/dev/null || true
	note "last launcher log lines:"
	tail -n 15 "$PICO_HOME/logs/launcher.log" 2>/dev/null || true
}

cleanup_uploads() {
	[ "$STAGE" = "$BIN_DIR" ] && return 0
	rm -f "$STAGE/picoclaw.new" "$STAGE/picoclaw-launcher.new" 2>/dev/null || true
}

case "$MODE" in
deploy)
	stop_all
	install_bin "$STAGE/picoclaw.new" "$BIN_DIR/picoclaw" "$CORE_SHA"
	if [ "$INSTALL_LAUNCHER" = "1" ]; then
		install_bin "$STAGE/picoclaw-launcher.new" "$BIN_DIR/picoclaw-launcher" "$LAUNCHER_SHA"
	fi
	start_all
	if ! wait_ready; then
		tail_logs
		die "gateway did not become ready within ${READY_TIMEOUT}s"
	fi
	verify_running
	verify_version
	cleanup_uploads
	;;
rollback)
	[ -f "$BIN_DIR/picoclaw.prev" ] || die "no picoclaw.prev to roll back to"
	stop_all
	cp -p "$BIN_DIR/picoclaw.prev" "$BIN_DIR/picoclaw" || die "rollback copy failed"
	if [ -f "$BIN_DIR/picoclaw-launcher.prev" ]; then
		cp -p "$BIN_DIR/picoclaw-launcher.prev" "$BIN_DIR/picoclaw-launcher"
	fi
	start_all
	if ! wait_ready; then
		tail_logs
		die "gateway did not become ready after rollback"
	fi
	verify_running
	"$BIN_DIR/picoclaw" version 2>/dev/null | grep -E 'picoclaw v|Build' || true
	note "rollback complete"
	;;
check)
	note "host: $(uname -n) ($(uname -m))"
	note "user: $(id -un)"
	note "service mode: $SERVICE_MODE ($SERVICE_NAME)"
	if [ "$SERVICE_MODE" = "systemd" ]; then
		note "service enabled: $(systemctl is-enabled "$SERVICE_NAME" 2>/dev/null || echo unknown)"
		note "service active: $(systemctl is-active "$SERVICE_NAME" 2>/dev/null || echo unknown)"
	fi
	[ -d "$BIN_DIR" ] || die "$BIN_DIR not found"
	ls -l "$BIN_DIR" | sed 's/^/[remote]   /'
	note "installed version: $("$BIN_DIR/picoclaw" version 2>/dev/null | grep -E 'picoclaw v' | head -n 1 | sed 's/^ *//')"
	note "configured version: $(grep -o '"version": "[^"]*"' "$PICO_HOME/.picoclaw.pid" 2>/dev/null | cut -d'"' -f4)"
	note "running instances:"
	proc_list | sed 's/^/[remote]   /'
	;;
*)
	die "unknown mode: $MODE"
	;;
esac
note "done"
REMOTE_EOF

sh -n "$TMP/remote.sh" || die "generated remote script has syntax errors"

detect_auth
arch=$(run_ssh "uname -m" | tail -n 1)
if [[ "$arch" != "$EXPECTED_ARCH" ]]; then
	die "remote arch is '$arch', expected $EXPECTED_ARCH for target $TARGET_NAME"
fi
run_ssh "test -d '$REMOTE_DIR'" || die "$REMOTE_DIR not found on the board"
if [[ "$SERVICE_MODE" == "systemd" ]]; then
	ensure_sudo
	remote_home=$(run_ssh 'printf %s "$HOME"')
	[[ -n "$remote_home" ]] || die "cannot determine remote HOME for $USER@$HOST"
	REMOTE_STAGE="$remote_home/.picoclaw-deploy-$$"
else
	REMOTE_STAGE="$REMOTE_DIR"
fi
REMOTE_SCRIPT="$REMOTE_STAGE/deploy-$$.sh"

echo "target: $TARGET_NAME ($USER@$HOST, $arch)"

run_ssh "mkdir -p '$REMOTE_STAGE'" >/dev/null || die "cannot create staging dir $REMOTE_STAGE"
upload "$TMP/remote.sh" "$REMOTE_SCRIPT" || die "failed to upload remote script"

remote_env() {
	local mode="$1"
	printf "MODE='%s' VER='%s' INSTALL_LAUNCHER='%s' CORE_SHA='%s' LAUNCHER_SHA='%s' READY_TIMEOUT='%s' GOMAXPROCS='%s' SERVICE_MODE='%s' SERVICE_NAME='%s' BIN_DIR='%s' PICO_HOME='%s' GW_PORT='%s' STAGE='%s'" \
		"$mode" "$VERSION" "$DO_LAUNCHER" "$CORE_SHA" "$LAUNCHER_SHA" "$TIMEOUT" "$GOMAXPROCS_VALUE" \
		"$SERVICE_MODE" "$SERVICE_NAME" "$REMOTE_DIR" "$PICO_HOME" "$GW_PORT" "$REMOTE_STAGE"
}

run_remote() {
	local mode="$1"
	local envs
	envs=$(remote_env "$mode")
	if [[ "$SERVICE_MODE" == "systemd" ]]; then
		run_ssh_stdin "$PASSWORD" "sudo -H -S -p '' env $envs sh '$REMOTE_SCRIPT'"
	else
		run_ssh "env $envs sh '$REMOTE_SCRIPT'"
	fi
}

if [[ $CHECK_ONLY -eq 1 ]]; then
	VERSION=""
	CORE_SHA=""
	LAUNCHER_SHA=""
	run_remote check || die "preflight failed on $HOST"
	exit 0
fi

cd "$ROOT"
VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo dev)

if [[ $ROLLBACK -eq 1 ]]; then
	CORE_SHA=""
	LAUNCHER_SHA=""
	echo "rolling back binaries on $TARGET_NAME ($USER@$HOST)"
	if ! run_remote rollback; then
		die "rollback failed on the board"
	fi
	echo "rollback complete on $HOST"
	exit 0
fi

if [[ $DO_BUILD -eq 1 ]]; then
	for c in make git go; do
		command -v "$c" >/dev/null 2>&1 || die "missing dependency: $c"
	done
	if [[ $DO_LAUNCHER -eq 1 ]]; then
		command -v pnpm >/dev/null 2>&1 || die "missing dependency: pnpm (needed for launcher build; use --no-launcher to skip)"
	fi

	BUILD_ENV=(GOTOOLCHAIN=auto)
	[[ -n "$BUILD_GOARM" ]] && BUILD_ENV+=("GOARM=$BUILD_GOARM")
	echo "building $VERSION for linux/$BUILD_GOARCH${BUILD_GOARM:+/$BUILD_GOARM} ($TARGET_NAME)"
	env "${BUILD_ENV[@]}" make build GOOS=linux GOARCH="$BUILD_GOARCH"
	if [[ $DO_LAUNCHER -eq 1 ]]; then
		# WEB_GO=go avoids the macOS launcher build forcing CGO_ENABLED=1
		env "${BUILD_ENV[@]}" make build-launcher GOOS=linux GOARCH="$BUILD_GOARCH" WEB_GO=go
	fi
fi

[[ -f "$CORE_BIN" ]] || die "missing $CORE_BIN"
if [[ $DO_LAUNCHER -eq 1 ]]; then
	[[ -f "$LAUNCHER_BIN" ]] || die "missing $LAUNCHER_BIN"
fi

CORE_SHA=$(sha256_local "$CORE_BIN")
LAUNCHER_SHA=""
if [[ $DO_LAUNCHER -eq 1 ]]; then
	LAUNCHER_SHA=$(sha256_local "$LAUNCHER_BIN")
fi

echo "uploading binaries to $USER@$HOST:$REMOTE_STAGE"
upload "$CORE_BIN" "$REMOTE_STAGE/picoclaw.new" || die "failed to upload $CORE_BIN"
if [[ $DO_LAUNCHER -eq 1 ]]; then
	upload "$LAUNCHER_BIN" "$REMOTE_STAGE/picoclaw-launcher.new" || die "failed to upload $LAUNCHER_BIN"
fi

echo "deploying $VERSION to $TARGET_NAME ($HOST)"
if ! run_remote deploy; then
	die "deploy failed on the board (old binaries kept in *.prev)"
fi

echo "deploy complete: $VERSION on $TARGET_NAME ($USER@$HOST)"
