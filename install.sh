#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-or-later
# Copyright (C) 2026 Piekasz (GitHub: Piekario). Author attribution must be kept: see NOTICE.
# install.sh - installs the WowMcp addon and the wow-mcp server on macOS and Linux (the counterpart of install.ps1).
#
# What it does:
#   1. finds the game directory (--wow-dir, $WOW_DIR, macOS default, common Wine / Lutris / Proton / Bottles prefixes on Linux)
#   2. copies the addon to <wow>/<client>/Interface/AddOns/WowMcp, after a timestamped backup of the previous copy,
#      and removes files that no longer exist in the new version
#   3. installs the server binary to the install directory (the previous one is kept as wow-mcp-<version>.old)
#   4. runs `wow-mcp --check` and prints the MCP client configuration
#
# It writes ONLY the addon folder inside the game directory. It never touches WTF/ or SavedVariables.
# Backups, the server and its history live in the install directory (default ~/.local/share/wow-mcp).
#
# Run `./install.sh --help` for the options.
# shellcheck disable=SC2012  # ls is used only on names this script creates (addon-*, wow-mcp-*.old)
set -eu

ADDON_NAME=WowMcp
HERE=$(cd "$(dirname "$0")" && pwd)

WOW_DIR=${WOW_DIR:-}
CLIENT=_forever_
INSTALL_DIR=${WOW_MCP_HOME:-$HOME/.local/share/wow-mcp}
KEEP=2
DRY=0
ROLLBACK=0
SKIP_CHECK=0
BUILD=0
SLOTS=0
BINARY=
ADDON_SRC=
ADDONS_DIR=
ADDON_TARGET=
BACKUP_DIR=

usage() {
	cat <<USAGE
Usage: ./install.sh [options]

  --wow-dir DIR      game directory (the folder that contains _forever_ / _retail_ ...). Default: \$WOW_DIR, then auto-detection.
  --client NAME      client folder inside the game directory (default: _forever_)
  --install-dir DIR  where the server, its history and the backups go (default: ~/.local/share/wow-mcp, or \$WOW_MCP_HOME)
  --binary FILE      server binary to install (default: ./wow-mcp from the release archive, or ./bin/wow-mcp)
  --build            build the server from server/ with Go (source checkout only) instead of using a ready binary
  --keep N           how many addon backups and old binaries to keep (default: 2)
  --slots [N]        also install N request-slot addons (WowMcp_S0001..) in the AddOns folder
                     for the server's inbound channel (waypoints, map focus); N defaults to 600.
                     Slot folders above N are removed. Without --slots no slots are touched.
  --skip-check       do not run wow-mcp --check at the end
  --dry-run          print what would be done and change nothing
  --rollback         go back to the previous addon backup and the previous server binary
  -h, --help         this help

Examples:
  ./install.sh --dry-run
  ./install.sh --wow-dir "\$HOME/Games/battlenet/drive_c/Program Files (x86)/World of Warcraft"
  ./install.sh --rollback
USAGE
}

say() { printf '%s\n' "$*"; }
step() { printf '==> %s\n' "$*"; }
ok() { printf '    %s\n' "$*"; }
warn() { printf '    WARNING: %s\n' "$*" >&2; }
die() {
	printf 'ERROR: %s\n' "$*" >&2
	exit 1
}

while [ $# -gt 0 ]; do
	case $1 in
	--wow-dir) [ $# -ge 2 ] || die "--wow-dir needs a value"; WOW_DIR=$2; shift 2 ;;
	--client) [ $# -ge 2 ] || die "--client needs a value"; CLIENT=$2; shift 2 ;;
	--install-dir) [ $# -ge 2 ] || die "--install-dir needs a value"; INSTALL_DIR=$2; shift 2 ;;
	--binary) [ $# -ge 2 ] || die "--binary needs a value"; BINARY=$2; shift 2 ;;
	--keep) [ $# -ge 2 ] || die "--keep needs a value"; KEEP=$2; shift 2 ;;
	--slots)
		case ${2:-} in
		'' | *[!0-9]*) SLOTS=600; shift ;;
		*) SLOTS=$2; shift 2 ;;
		esac
		;;
	--build) BUILD=1; shift ;;
	--skip-check) SKIP_CHECK=1; shift ;;
	--dry-run) DRY=1; shift ;;
	--rollback) ROLLBACK=1; shift ;;
	-h | --help) usage; exit 0 ;;
	*) usage >&2; die "unknown option: $1" ;;
	esac
done

case $KEEP in
'' | *[!0-9]*) die "--keep must be a number >= 1" ;;
esac
[ "$KEEP" -ge 1 ] || die "--keep must be a number >= 1"
case $CLIENT in
*/* | '' | . | ..) die "--client must be a single folder name such as _forever_" ;;
esac
case $SLOTS in
*[!0-9]*) die "--slots must be a number" ;;
esac
[ "$SLOTS" -le 9999 ] || die "--slots must be at most 9999 (the WowMcp_SNNNN naming)"
[ -n "$INSTALL_DIR" ] || die "the install directory must not be empty"

stamp() { date +%Y%m%d-%H%M%S; }

# unique_path BASE -> BASE, or BASE-2, BASE-3 ... when it already exists
unique_path() {
	up_base=$1
	up_path=$up_base
	up_n=2
	while [ -e "$up_path" ]; do
		up_path="$up_base-$up_n"
		up_n=$((up_n + 1))
	done
	printf '%s\n' "$up_path"
}

# run a state-changing command, or only announce it with --dry-run
act() {
	if [ "$DRY" = 1 ]; then
		ok "[dry-run] $*"
	else
		"$@"
	fi
}

toc_value() { # toc_value FILE KEY
	sed -n "s/^##[[:space:]]*$2:[[:space:]]*\\(.*[^[:space:]]\\)[[:space:]]*\$/\\1/p" "$1" | head -n 1
}

server_version() { # server_version BINARY -> "0.10.0" or empty
	"$1" --version 2>/dev/null | sed -n 's/^wow-mcp[[:space:]]\{1,\}\([^[:space:]]*\).*/\1/p' | head -n 1 || true
}

json_escape() { printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'; }

is_client_dir() { [ -d "$1/$CLIENT" ]; }

# Prints the game directory when exactly one is found; dies listing the candidates when there are several.
detect_game() {
	dg_list=$(mktemp)
	# macOS
	for dg_p in "/Applications/World of Warcraft" "$HOME/Applications/World of Warcraft"; do
		if is_client_dir "$dg_p"; then printf '%s\n' "$dg_p" >>"$dg_list"; fi
	done
	# Linux: Wine, Lutris, Bottles, Steam Proton (native and Flatpak). Quoted prefixes, unquoted globs only where needed.
	for dg_pf in "Program Files (x86)" "Program Files"; do
		for dg_p in \
			"$HOME/.wine/drive_c/$dg_pf/World of Warcraft" \
			"$HOME"/Games/*/drive_c/"$dg_pf"/"World of Warcraft" \
			"$HOME"/.local/share/bottles/bottles/*/drive_c/"$dg_pf"/"World of Warcraft" \
			"$HOME"/.var/app/com.usebottles.bottles/data/bottles/bottles/*/drive_c/"$dg_pf"/"World of Warcraft" \
			"$HOME"/.var/app/net.lutris.Lutris/data/lutris/*/drive_c/"$dg_pf"/"World of Warcraft" \
			"$HOME"/.steam/steam/steamapps/compatdata/*/pfx/drive_c/"$dg_pf"/"World of Warcraft" \
			"$HOME"/.local/share/Steam/steamapps/compatdata/*/pfx/drive_c/"$dg_pf"/"World of Warcraft" \
			"$HOME"/.var/app/com.valvesoftware.Steam/.local/share/Steam/steamapps/compatdata/*/pfx/drive_c/"$dg_pf"/"World of Warcraft"; do
			if is_client_dir "$dg_p"; then printf '%s\n' "$dg_p" >>"$dg_list"; fi
		done
	done
	# Wine prefixes where the game sits directly on C:
	for dg_p in "$HOME"/Games/*/drive_c/"World of Warcraft" "$HOME/.wine/drive_c/World of Warcraft"; do
		if is_client_dir "$dg_p"; then printf '%s\n' "$dg_p" >>"$dg_list"; fi
	done
	dg_sorted=$(sort -u "$dg_list")
	rm -f "${dg_list:?}"
	if [ -z "$dg_sorted" ]; then
		return 0
	fi
	if [ "$(printf '%s\n' "$dg_sorted" | wc -l | tr -d ' ')" -gt 1 ]; then
		die "found several game directories; pick one with --wow-dir:
$dg_sorted"
	fi
	printf '%s\n' "$dg_sorted"
}

resolve_game() {
	if [ -z "$WOW_DIR" ]; then
		WOW_DIR=$(detect_game)
	fi
	[ -n "$WOW_DIR" ] || die "could not find World of Warcraft. Pass the game directory with --wow-dir (or set WOW_DIR).
  macOS default:  /Applications/World of Warcraft
  Linux example:  \$HOME/Games/battlenet/drive_c/Program Files (x86)/World of Warcraft"
	# Allow pointing at the client folder itself (.../World of Warcraft/_forever_)
	if [ -d "$WOW_DIR/Interface/AddOns" ] && [ ! -d "$WOW_DIR/$CLIENT" ]; then
		rc_base=$(basename "$WOW_DIR")
		case $rc_base in
		_*_)
			CLIENT=$rc_base
			WOW_DIR=$(dirname "$WOW_DIR")
			;;
		esac
	fi
	[ -d "$WOW_DIR" ] || die "the game directory does not exist: $WOW_DIR"
	[ -d "$WOW_DIR/$CLIENT" ] || die "there is no client folder '$CLIENT' in $WOW_DIR (use --client to choose another, e.g. _retail_)"
	[ -d "$WOW_DIR/$CLIENT/Interface/AddOns" ] || die "the AddOns folder does not exist: $WOW_DIR/$CLIENT/Interface/AddOns (start the game once, or check --client)"
	WOW_DIR=$(cd "$WOW_DIR" && pwd)
	ADDONS_DIR="$WOW_DIR/$CLIENT/Interface/AddOns"
	ADDON_TARGET="$ADDONS_DIR/$ADDON_NAME"
}

# We only ever copy into / delete inside .../AddOns/WowMcp.
assert_safe_addon_target() {
	if [ "$(basename "$ADDON_TARGET")" != "$ADDON_NAME" ] || [ "$(basename "$(dirname "$ADDON_TARGET")")" != AddOns ]; then
		die "unsafe addon target: $ADDON_TARGET"
	fi
	# mirror_dir deletes files that are not in the source: through a symlink (e.g. to a repository checkout)
	# that would delete files outside the AddOns folder.
	if [ -L "$ADDON_TARGET" ]; then
		die "the addon folder is a symbolic link: $ADDON_TARGET. The script mirrors only into a real folder; remove the link or update the linked folder yourself."
	fi
}

# physical_path PATH: the absolute path with symlinks resolved; for a path that does not exist yet, its nearest
# existing parent is resolved and the rest appended.
physical_path() {
	pp_rest=
	pp_dir=$1
	case $pp_dir in /*) ;; *) pp_dir="$(pwd -P)/$pp_dir" ;; esac
	while [ ! -d "$pp_dir" ]; do
		pp_rest="/$(basename "$pp_dir")$pp_rest"
		pp_dir=$(dirname "$pp_dir")
	done
	printf '%s%s\n' "$(cd "$pp_dir" && pwd -P)" "$pp_rest"
}

# The server's own files (backups, history, binary) must never be inside the game directory.
assert_outside_game() {
	ao_dir=$(physical_path "$INSTALL_DIR")
	ao_game=$(physical_path "$WOW_DIR")
	case "$ao_dir/" in
	"$ao_game"/*) die "the install directory must be outside the game directory: $ao_dir" ;;
	esac
}

# mirror_dir SRC DST: copies SRC into DST and removes files in DST that are not in SRC
mirror_dir() {
	md_src=$1
	md_dst=$2
	mkdir -p "$md_dst"
	cp -R "$md_src"/. "$md_dst"/
	md_a=$(mktemp)
	md_b=$(mktemp)
	(cd "$md_src" && find . -type f | sort) >"$md_a"
	(cd "$md_dst" && find . -type f | sort) >"$md_b"
	comm -13 "$md_a" "$md_b" >"$md_a.extra"
	while IFS= read -r md_extra; do
		[ -n "$md_extra" ] || continue
		rm -f "${md_dst:?}/${md_extra:?}"
		ok "removed obsolete file: $md_extra"
	done <"$md_a.extra"
	rm -f "${md_a:?}" "${md_b:?}" "${md_a:?}.extra"
	find "$md_dst" -depth -type d -empty ! -path "$md_dst" -exec rmdir {} \; 2>/dev/null || true
}

list_backups() { # one name per line, newest last
	[ -d "$BACKUP_DIR" ] || return 0
	(cd "$BACKUP_DIR" && ls -1d addon-* 2>/dev/null | sort) || true
}

backup_addon() {
	if [ ! -d "$ADDON_TARGET" ]; then
		ok "the addon was not installed yet: no backup needed"
		return 0
	fi
	ba_dest=$(unique_path "$BACKUP_DIR/addon-$(stamp)")
	if [ "$DRY" = 1 ]; then
		ok "[dry-run] back up $ADDON_TARGET -> $ba_dest"
	else
		mkdir -p "$ba_dest"
		cp -R "$ADDON_TARGET"/. "$ba_dest"/
		ok "addon backup: $ba_dest"
	fi
}

remove_backup() { # only names addon-* inside the backup directory
	case $1 in
	addon-*) act rm -rf "${BACKUP_DIR:?}/${1:?}" ;;
	*) warn "refusing to remove unexpected backup name: $1" ;;
	esac
}

prune_backups() {
	pb_total=$(list_backups | wc -l | tr -d ' ')
	[ "$pb_total" -gt "$KEEP" ] || return 0
	list_backups | head -n $((pb_total - KEEP)) | while IFS= read -r pb_dir; do
		remove_backup "$pb_dir"
	done
}

install_addon() {
	step "Addon: backup and installation"
	assert_safe_addon_target
	backup_addon
	if [ "$DRY" = 1 ]; then
		ok "[dry-run] mirror $ADDON_SRC -> $ADDON_TARGET (copy, remove obsolete files)"
	else
		mirror_dir "$ADDON_SRC" "$ADDON_TARGET"
		ok "addon installed: $ADDON_TARGET"
	fi
	prune_backups
}

# install_slots generates the request-slot addons WowMcp_S0001..WowMcp_S$SLOTS: the inbound
# channel the server writes requests into (the same files server/internal/slots.Install creates).
# It writes ONLY folders named WowMcp_SNNNN directly under Interface/AddOns, and removes
# slot folders above $SLOTS so an older, larger install leaves no stale bodies.
install_slots() {
	[ "$SLOTS" -gt 0 ] || return 0
	step "Request slots: $SLOTS folders WowMcp_S0001..$(printf 'WowMcp_S%04d' "$SLOTS")"
	case $ADDONS_DIR in
	*/Interface/AddOns) ;;
	*) die "request slots go only under Interface/AddOns: $ADDONS_DIR" ;;
	esac
	if [ "$DRY" = 1 ]; then
		ok "[dry-run] create $SLOTS slot folders (toc + body.lua) in $ADDONS_DIR and remove slots above $SLOTS"
		return 0
	fi
	is_t=$(date +%s)
	is_n=1
	while [ "$is_n" -le "$SLOTS" ]; do
		is_name=$(printf 'WowMcp_S%04d' "$is_n")
		is_dir="$ADDONS_DIR/$is_name"
		mkdir -p "$is_dir"
		printf '## Interface: 16001\n## Title: WowMcp slot %04d\n## Notes: Request slot for WowMcp.\n## Group: WowMcp\n## LoadOnDemand: 1\n\nbody.lua\n' "$is_n" >"$is_dir/$is_name.toc"
		printf 'WowMcpInbox = {["t"]=%s,["reqs"]={},["got"]={}}\n' "$is_t" >"$is_dir/body.lua"
		is_n=$((is_n + 1))
	done
	for is_dir in "$ADDONS_DIR"/WowMcp_S[0-9][0-9][0-9][0-9]; do
		[ -d "$is_dir" ] || continue
		is_num=$(basename "$is_dir" | sed 's/^WowMcp_S0*//')
		[ -n "$is_num" ] || is_num=0
		if [ "$is_num" -gt "$SLOTS" ]; then
			rm -rf "${is_dir:?}"
			ok "removed slot above $SLOTS: $(basename "$is_dir")"
		fi
	done
	ok "$SLOTS request slots installed (the server writes requests into their body.lua files)"
}

check_interface() {
	step "Interface number check"
	ci_toc="$ADDON_SRC/$ADDON_NAME.toc"
	ci_iface=$(toc_value "$ci_toc" Interface)
	if [ -z "$ci_iface" ]; then
		warn "the .toc has no ## Interface line"
		return 0
	fi
	ci_ver=
	for ci_f in "$WOW_DIR/.build.info" "$WOW_DIR/$CLIENT/.build.info"; do
		[ -f "$ci_f" ] || continue
		ci_ver=$(awk -F'|' 'NR==1{for(i=1;i<=NF;i++) if($i ~ /^Version!/) c=i} NR==2&&c{print $c}' "$ci_f")
		[ -z "$ci_ver" ] || break
	done
	if [ -z "$ci_ver" ]; then
		ok "could not read the client version: skipping the check"
		return 0
	fi
	ci_num=$(printf '%s' "$ci_ver" | awk -F. 'NF>=3{printf "%d", $1*10000+$2*100+$3}')
	if [ -z "$ci_num" ]; then
		ok "unknown client version format: $ci_ver"
		return 0
	fi
	if [ "$ci_num" = "$ci_iface" ]; then
		ok "Interface matches: .toc $ci_iface, client $ci_ver"
	else
		warn "Interface in .toc = $ci_iface, client $ci_ver suggests $ci_num. The addon may be marked 'out of date' (tick 'Load out of date AddOns' in the game)."
	fi
}

unique_old_path() { # unique_old_path VERSION
	uo_token=$(printf '%s' "$1" | tr -c '0-9A-Za-z._-' '_')
	[ -n "$uo_token" ] || uo_token=$(stamp)
	uo_path="$INSTALL_DIR/wow-mcp-$uo_token.old"
	uo_n=2
	while [ -e "$uo_path" ]; do
		uo_path="$INSTALL_DIR/wow-mcp-$uo_token-$uo_n.old"
		uo_n=$((uo_n + 1))
	done
	printf '%s\n' "$uo_path"
}

newest_old_binary() {
	ls -1t "$INSTALL_DIR"/wow-mcp-*.old 2>/dev/null | head -n 1 || true
}

prune_old_binaries() {
	pob_total=$(ls -1t "$INSTALL_DIR"/wow-mcp-*.old 2>/dev/null | wc -l | tr -d ' ')
	[ "$pob_total" -gt "$KEEP" ] || return 0
	ls -1t "$INSTALL_DIR"/wow-mcp-*.old | tail -n $((pob_total - KEEP)) | while IFS= read -r pob_f; do
		act rm -f "${pob_f:?}"
	done
}

install_binary() {
	step "Server: installing the binary"
	act mkdir -p "$INSTALL_DIR"
	ib_cur="$INSTALL_DIR/wow-mcp"
	if [ -f "$ib_cur" ]; then
		ib_old=$(unique_old_path "$(server_version "$ib_cur")")
		act mv "$ib_cur" "$ib_old"
		if [ "$DRY" = 0 ]; then ok "previous server kept as $(basename "$ib_old")"; fi
	fi
	if [ "$DRY" = 1 ]; then
		ok "[dry-run] install $BINARY -> $ib_cur"
	else
		ib_tmp="$INSTALL_DIR/.wow-mcp.tmp.$$"
		cp "$BINARY" "$ib_tmp"
		chmod 755 "$ib_tmp"
		mv "$ib_tmp" "$ib_cur"
		ok "server installed: $ib_cur ($(server_version "$ib_cur"))"
	fi
	prune_old_binaries
}

do_rollback() {
	step "Rollback"
	assert_safe_addon_target
	rb_last=$(list_backups | tail -n 1)
	rb_old=$(newest_old_binary)
	if [ -z "$rb_last" ] && [ -z "$rb_old" ]; then
		die "nothing to roll back: no addon backups in $BACKUP_DIR and no wow-mcp-*.old in $INSTALL_DIR"
	fi

	if [ -n "$rb_last" ]; then
		# the current version is backed up too, so a second --rollback returns to the newer one
		backup_addon
		if [ "$DRY" = 1 ]; then
			ok "[dry-run] restore addon from $BACKUP_DIR/$rb_last"
		else
			mirror_dir "$BACKUP_DIR/$rb_last" "$ADDON_TARGET"
			remove_backup "$rb_last"
			ok "addon restored from $rb_last"
		fi
	else
		warn "no addon backup: the addon was left as it is"
	fi

	if [ -n "$rb_old" ]; then
		rb_cur="$INSTALL_DIR/wow-mcp"
		if [ -f "$rb_cur" ]; then
			rb_keep=$(unique_old_path "$(server_version "$rb_cur")")
			act mv "$rb_cur" "$rb_keep"
		fi
		act mv "$rb_old" "$rb_cur"
		if [ "$DRY" = 0 ]; then ok "server restored from $(basename "$rb_old") ($(server_version "$rb_cur"))"; fi
	else
		warn "no previous server binary (wow-mcp-*.old): the server was left as it is"
	fi
}

run_check() {
	[ "$SKIP_CHECK" = 0 ] || return 0
	step "Check: wow-mcp --check"
	if [ "$DRY" = 1 ]; then
		ok "[dry-run] $INSTALL_DIR/wow-mcp --check --wow-dir \"$WOW_DIR\""
		return 0
	fi
	if "$INSTALL_DIR/wow-mcp" --check --wow-dir "$WOW_DIR"; then
		ok "the check passed"
	else
		warn "the check did not pass. This is normal on a first install: log in, type /mcp reload in the game, then run it again:"
		warn "  $INSTALL_DIR/wow-mcp --check --wow-dir \"$WOW_DIR\""
	fi
}

print_config() {
	pc_bin="$INSTALL_DIR/wow-mcp"
	pc_bin_j=$(json_escape "$pc_bin")
	pc_dir_j=$(json_escape "$WOW_DIR")
	step "Connect your MCP client"
	say ""
	say "Add this entry under \"mcpServers\" in your MCP client's configuration (see the client's documentation for where it is):"
	say ""
	cat <<JSON
  "wow": {
    "command": "$pc_bin_j",
    "args": ["--wow-dir", "$pc_dir_j"]
  }
JSON
	say ""
	say "Then restart the MCP client completely. After an install that adds or removes addon files, close the game fully and start it"
	say "again (/reload does not re-read the .toc), tick WowMcp on the character selection screen and type /mcp reload."
}

if [ "$DRY" = 1 ]; then
	step "DRY RUN: nothing will be changed"
fi

resolve_game
BACKUP_DIR="$INSTALL_DIR/backup"
assert_outside_game
step "Game: $WOW_DIR (client $CLIENT)"
ok "addon target: $ADDON_TARGET"
ok "install directory: $INSTALL_DIR"

if [ "$ROLLBACK" = 1 ]; then
	do_rollback
	run_check
	print_config
	exit 0
fi

# Locate the sources: release archive layout first, then a source checkout.
if [ -f "$HERE/$ADDON_NAME/$ADDON_NAME.toc" ]; then
	ADDON_SRC="$HERE/$ADDON_NAME"
elif [ -f "$HERE/addon/$ADDON_NAME/$ADDON_NAME.toc" ]; then
	ADDON_SRC="$HERE/addon/$ADDON_NAME"
else
	die "the addon source ($ADDON_NAME/$ADDON_NAME.toc) was not found next to install.sh. Run it from the unpacked release archive or the repository."
fi

BUILT_TMP=
if [ -z "$BINARY" ]; then
	if [ "$BUILD" = 1 ]; then
		command -v go >/dev/null 2>&1 || die "--build needs Go (>= 1.25) in PATH"
		[ -d "$HERE/server" ] || die "--build needs the source checkout (server/ not found)"
		BUILT_TMP=$(mktemp -d)
		BINARY="$BUILT_TMP/wow-mcp"
		step "Server: building from server/"
		if [ "$DRY" = 1 ]; then
			ok "[dry-run] (cd server && go build -trimpath -ldflags \"-s -w\" -o $BINARY .)"
		else
			(cd "$HERE/server" && go build -trimpath -ldflags "-s -w" -o "$BINARY" .)
		fi
	elif [ -f "$HERE/wow-mcp" ]; then
		BINARY="$HERE/wow-mcp"
	elif [ -f "$HERE/bin/wow-mcp" ]; then
		BINARY="$HERE/bin/wow-mcp"
	else
		die "no server binary found. Use the release archive for your platform, pass --binary FILE, or --build (needs Go)."
	fi
fi
if [ -z "$BUILT_TMP" ] && [ ! -f "$BINARY" ]; then
	die "the server binary does not exist: $BINARY"
fi

ok "addon source: $ADDON_SRC (version $(toc_value "$ADDON_SRC/$ADDON_NAME.toc" Version))"
ok "server binary: $BINARY"

install_addon
install_slots
check_interface
install_binary
if [ -n "$BUILT_TMP" ]; then rm -rf "${BUILT_TMP:?}"; fi
run_check
print_config
if [ "$DRY" = 1 ]; then
	say ""
	say "Dry run finished: nothing was changed."
fi
