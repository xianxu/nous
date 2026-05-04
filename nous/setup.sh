#!/usr/bin/env bash
# Nous Layer Setup
# Bootstraps a target repo with nous infrastructure and plugins.
#
# Usage:
#   cd /path/to/your-repo && ../nous/nous/setup.sh <mode> [--yes]
#
# Modes:
#   --all              Symlink everything (all plugins, track nous HEAD)
#   --add <plugin>     Vendor a specific plugin (copy files, you own them)
#   --rm <plugin>      Remove a vendored plugin
#   (no mode flag)     Refresh: re-run in whatever mode was previously set
#
# Mode is recorded in .nous-mode ("all" or "selective").
# Selected plugins recorded in .nous-plugins (one per line).
# Idempotent — safe to re-run for updates.
set -euo pipefail

# ── Parse flags ───────────────────────────────────────────────────────────────
ACTION=""          # all, add, rm, refresh
PLUGIN=""          # plugin name for add/rm
ASSUME_YES=false

while [[ $# -gt 0 ]]; do
    case "$1" in
        --all)       ACTION="all" ;;
        --add)       ACTION="add"; PLUGIN="${2:-}"; shift ;;
        --rm)        ACTION="rm";  PLUGIN="${2:-}"; shift ;;
        --yes|-y)    ASSUME_YES=true ;;
        *)           echo "Error: unknown flag: $1" >&2; exit 2 ;;
    esac
    shift
done

# ── Resolve paths ────────────────────────────────────────────────────────────
SCRIPT_REAL="$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}" 2>/dev/null || realpath "${BASH_SOURCE[0]}")")" && pwd)"
NOUS_DIR="$(dirname "$SCRIPT_REAL")"
TARGET_DIR="$(pwd)"
CORE_MANIFEST="$SCRIPT_REAL/nous.manifest"
ARIADNE_BASE_MANIFEST="$SCRIPT_REAL/ariadne-base.manifest"
PLUGINS_DIR="$SCRIPT_REAL/plugins"

# Where to find ariadne when refreshing nous itself. Override via env if needed.
ARIADNE_DIR="${ARIADNE_DIR:-$(dirname "$NOUS_DIR")/ariadne}"

# ── Colors ────────────────────────────────────────────────────────────────────
GREEN='\033[1;32m'
YELLOW='\033[1;33m'
RED='\033[1;31m'
BOLD_RED='\033[1;31m'
CYAN='\033[1;36m'
RESET='\033[0m'

# ── Helpers ───────────────────────────────────────────────────────────────────
rel_path() {
    python3 -c "import os.path; print(os.path.relpath('$1', '$2'))"
}

ensure_parent() {
    local parent
    parent=$(dirname "$1")
    [[ -d "$parent" ]] || mkdir -p "$parent"
}

create_symlink() {
    local src="$1" dst="$2"
    ensure_parent "$dst"
    local rel
    rel=$(rel_path "$src" "$(dirname "$dst")")

    if [[ -L "$dst" ]]; then
        local existing
        existing=$(readlink "$dst")
        if [[ "$existing" == "$rel" ]]; then
            return 0
        fi
        rm "$dst"
        printf "  ${YELLOW}updated${RESET} %s\n" "${dst#$TARGET_DIR/}"
    elif [[ -e "$dst" ]]; then
        rm -rf "$dst"
        printf "  ${YELLOW}relinked${RESET} %s (was vendored)\n" "${dst#$TARGET_DIR/}"
    else
        printf "  ${GREEN}linked${RESET}  %s\n" "${dst#$TARGET_DIR/}"
    fi
    ln -s "$rel" "$dst"
}

create_vendored() {
    local src="$1" dst="$2"
    ensure_parent "$dst"

    if [[ ! -e "$src" ]]; then
        printf "  ${YELLOW}missing${RESET} %s (source not found)\n" "${dst#$TARGET_DIR/}"
        return 0
    fi
    if [[ -L "$dst" ]]; then
        rm "$dst"
        cp -RL "$src" "$dst"
        printf "  ${YELLOW}vendored${RESET} %s (was symlinked)\n" "${dst#$TARGET_DIR/}"
    elif [[ -e "$dst" ]]; then
        rm -rf "$dst"
        cp -RL "$src" "$dst"
        printf "  ${YELLOW}refreshed${RESET} %s\n" "${dst#$TARGET_DIR/}"
    else
        cp -RL "$src" "$dst"
        printf "  ${GREEN}vendored${RESET} %s\n" "${dst#$TARGET_DIR/}"
    fi
}

create_scaffold() {
    local dir="$1"
    if [[ -d "$dir" ]]; then
        return 0
    fi
    mkdir -p "$dir"
    touch "$dir/.gitkeep"
    printf "  ${GREEN}created${RESET} %s/\n" "${dir#$TARGET_DIR/}"
}

remove_entry() {
    local dst="$1"
    if [[ -L "$dst" || -e "$dst" ]]; then
        rm -rf "$dst"
        printf "  ${RED}removed${RESET} %s\n" "${dst#$TARGET_DIR/}"
    fi
}

merge_settings() {
    local base_file="$1"   # e.g. .claude/settings.ariadne.json (in nous)
    local target_file="$2" # e.g. .claude/settings.json (in target)

    ensure_parent "$target_file"
    [[ -L "$target_file" ]] && rm "$target_file"

    local merge_script="$NOUS_DIR/construct/scripts/merge-settings.sh"
    if [[ ! -f "$merge_script" ]]; then
        printf "  ${YELLOW}skipped${RESET} %s (merge-settings.sh not found in nous)\n" "${target_file#$TARGET_DIR/}"
        return 0
    fi

    local target_dir
    target_dir=$(dirname "$target_file")
    local had_local=false
    [[ -f "$target_dir/settings.local.json" ]] && had_local=true

    bash "$merge_script" "$base_file" "$target_dir" >/dev/null

    if "$had_local"; then
        printf "  ${YELLOW}merged${RESET}  %s (base + local)\n" "${target_file#$TARGET_DIR/}"
    else
        printf "  ${GREEN}created${RESET} %s (from base, no local overrides)\n" "${target_file#$TARGET_DIR/}"
    fi
}

# Process a manifest file with a given action (symlink, vendor, or remove).
# source_root defaults to $NOUS_DIR; pass $ARIADNE_DIR when self-refreshing
# the ariadne base layer into nous.
process_manifest() {
    local manifest="$1"
    local mode="$2"  # symlink, vendor, remove
    local source_root="${3:-$NOUS_DIR}"

    [[ -f "$manifest" ]] || return 0

    while IFS= read -r line; do
        [[ "$line" =~ ^[[:space:]]*# ]] && continue
        [[ -z "${line// /}" ]] && continue

        read -r action source target <<< "$line"
        target="${target:-$source}"

        case "$action" in
            symlink)
                if [[ "$mode" == "remove" ]]; then
                    remove_entry "$TARGET_DIR/$target"
                elif [[ "$mode" == "vendor" ]]; then
                    create_vendored "$source_root/$source" "$TARGET_DIR/$target"
                else
                    create_symlink "$source_root/$source" "$TARGET_DIR/$target"
                fi
                ;;
            scaffold)
                if [[ "$mode" != "remove" ]]; then
                    create_scaffold "$TARGET_DIR/$target"
                fi
                ;;
            copy)
                if [[ "$mode" == "remove" ]]; then
                    remove_entry "$TARGET_DIR/$target"
                elif [[ ! -f "$TARGET_DIR/$target" ]]; then
                    ensure_parent "$TARGET_DIR/$target"
                    cp "$source_root/$source" "$TARGET_DIR/$target"
                    printf "  ${GREEN}copied${RESET}  %s\n" "$target"
                else
                    printf "  ${YELLOW}skipped${RESET} %s (already exists)\n" "$target"
                fi
                ;;
            merge)
                if [[ "$mode" != "remove" ]]; then
                    merge_settings "$source_root/$source" "$TARGET_DIR/$target"
                fi
                ;;
            touch)
                if [[ "$mode" != "remove" ]]; then
                    ensure_parent "$TARGET_DIR/$source"
                    if [[ ! -f "$TARGET_DIR/$source" ]]; then
                        touch "$TARGET_DIR/$source"
                        printf "  ${GREEN}created${RESET} %s\n" "$source"
                    fi
                fi
                ;;
        esac
    done < "$manifest"
}

# List available plugins
list_plugins() {
    local plugins=()
    for f in "$PLUGINS_DIR"/*.manifest; do
        [[ -f "$f" ]] || continue
        plugins+=("$(basename "$f" .manifest)")
    done
    echo "${plugins[@]}"
}

# ── Self mode: running inside nous itself ────────────────────────────────────
# When invoked from the nous repo root, refresh the ariadne base layer that
# nous re-exports to its descendants. We deliberately do NOT call
# ariadne/construct/setup.sh — that script is private to ariadne. Instead we
# vendor the upstream base.manifest verbatim into nous/ariadne-base.manifest
# and process it ourselves (vendor mode, source_root=ARIADNE_DIR).
if [[ "$NOUS_DIR" == "$TARGET_DIR" ]]; then
    printf "${CYAN}Nous setup (self): refreshing ariadne base layer${RESET}\n\n"

    UPSTREAM_BASE_MANIFEST="$ARIADNE_DIR/construct/base.manifest"
    if [[ -f "$UPSTREAM_BASE_MANIFEST" ]]; then
        if ! cmp -s "$UPSTREAM_BASE_MANIFEST" "$ARIADNE_BASE_MANIFEST"; then
            cp "$UPSTREAM_BASE_MANIFEST" "$ARIADNE_BASE_MANIFEST"
            printf "  ${GREEN}synced${RESET}  nous/ariadne-base.manifest from %s\n" "$ARIADNE_DIR"
        fi
        printf "  ${CYAN}[ariadne base]${RESET}\n"
        process_manifest "$ARIADNE_BASE_MANIFEST" "vendor" "$ARIADNE_DIR"
    elif [[ -f "$ARIADNE_BASE_MANIFEST" ]]; then
        printf "  ${YELLOW}ariadne not found at %s; skipping base re-vendor.${RESET}\n" "$ARIADNE_DIR"
    fi

    printf "\n  ${CYAN}[nous skills]${RESET}\n"
    for skill_dir in "$SCRIPT_REAL/skills"/*/; do
        [[ -d "$skill_dir" ]] || continue
        name=$(basename "$skill_dir")
        create_symlink "${skill_dir%/}" "$TARGET_DIR/.claude/skills/$name"
    done
    printf "\n${GREEN}Done.${RESET}\n"
    exit 0
fi

# ── State files ──────────────────────────────────────────────────────────────
MODE_MARKER="$TARGET_DIR/.nous-mode"
PLUGINS_FILE="$TARGET_DIR/.nous-plugins"
PREVIOUS_MODE=""

if [[ -f "$MODE_MARKER" ]]; then
    PREVIOUS_MODE="$(tr -d '[:space:]' < "$MODE_MARKER")"
fi

# ── Determine action ─────────────────────────────────────────────────────────
if [[ -z "$ACTION" ]]; then
    # No flag — refresh mode
    if [[ -z "$PREVIOUS_MODE" ]]; then
        echo "First run. Use --all (symlink everything) or --add <plugin> (vendor selectively)."
        echo ""
        echo "Available plugins: $(list_plugins)"
        exit 0
    fi
    ACTION="refresh"
fi

# Validate plugin name for --add/--rm
if [[ "$ACTION" == "add" || "$ACTION" == "rm" ]]; then
    if [[ -z "$PLUGIN" ]]; then
        echo "Error: --$ACTION requires a plugin name." >&2
        echo "Available plugins: $(list_plugins)" >&2
        exit 1
    fi
    if [[ ! -f "$PLUGINS_DIR/$PLUGIN.manifest" ]]; then
        echo "Error: unknown plugin '$PLUGIN'." >&2
        echo "Available plugins: $(list_plugins)" >&2
        exit 1
    fi
fi

# ── Mode switching confirmation ──────────────────────────────────────────────
confirm() {
    local msg="$1"
    printf "${YELLOW}%s${RESET}\n" "$msg"
    if $ASSUME_YES; then return 0; fi
    if [[ ! -t 0 ]]; then
        echo "Error: requires --yes in non-interactive runs." >&2
        exit 1
    fi
    read -r -p "Continue? [y/N] " reply
    case "$reply" in
        y|Y|yes|YES) return 0 ;;
        *) echo "Aborted."; exit 1 ;;
    esac
}

# Confirm first-time setup in a new repo (no .nous-mode marker yet).
# Guards against accidental runs in the wrong directory.
if [[ -z "$PREVIOUS_MODE" && "$ACTION" != "rm" ]]; then
    REPO_NAME=$(basename "$TARGET_DIR")
    printf "${YELLOW}First-time nous setup in:${RESET} ${BOLD_RED}%s${RESET}\n" "$REPO_NAME"
    printf "  Path:   %s\n" "$TARGET_DIR"
    if [[ "$ACTION" == "all" ]]; then
        printf "  Action: --all (symlink everything from nous)\n"
    elif [[ "$ACTION" == "add" ]]; then
        printf "  Action: --add %s (vendor selectively)\n" "$PLUGIN"
    fi
    if ! $ASSUME_YES; then
        if [[ ! -t 0 ]]; then
            echo "Error: first-time setup requires --yes in non-interactive runs." >&2
            exit 1
        fi
        read -r -p "Set up nous in this repo? [y/N] " reply
        case "$reply" in
            y|Y|yes|YES) ;;
            *) echo "Aborted."; exit 1 ;;
        esac
    fi
    printf "\n"
fi

if [[ "$ACTION" == "all" && "$PREVIOUS_MODE" == "selective" ]]; then
    confirm "Switching from selective → all. Vendored plugin files with local modifications will be REPLACED by symlinks."
elif [[ "$ACTION" == "add" && "$PREVIOUS_MODE" == "all" ]]; then
    confirm "Switching from all → selective. All symlinked plugins will be REMOVED. Only explicitly added plugins will be vendored."
elif [[ "$ACTION" == "rm" && "$PREVIOUS_MODE" == "all" ]]; then
    echo "Error: cannot --rm in --all mode (everything is symlinked). Switch to --add first." >&2
    exit 1
fi

# ── Execute ──────────────────────────────────────────────────────────────────
printf "${CYAN}Nous setup: %s → %s${RESET}\n\n" "$NOUS_DIR" "$TARGET_DIR"

# Install nous core manifest (which includes the ariadne base layer entries
# re-exported from nous's vendored construct/, .openshell/, etc.)
CORE_MODE=""
if [[ "$ACTION" == "all" ]]; then
    CORE_MODE="symlink"
elif [[ "$ACTION" == "add" ]]; then
    CORE_MODE="vendor"
elif [[ "$ACTION" == "refresh" ]]; then
    # Honor previous mode so refresh doesn't silently switch symlinks → copies.
    if [[ "$PREVIOUS_MODE" == "all" ]]; then
        CORE_MODE="symlink"
    else
        CORE_MODE="vendor"
    fi
fi

if [[ -n "$CORE_MODE" ]]; then
    if [[ -f "$ARIADNE_BASE_MANIFEST" ]]; then
        printf "  ${CYAN}[ariadne base]${RESET}\n"
        process_manifest "$ARIADNE_BASE_MANIFEST" "$CORE_MODE"
    fi
    printf "  ${CYAN}[nous core]${RESET}\n"
    process_manifest "$CORE_MANIFEST" "$CORE_MODE"
fi

case "$ACTION" in
    all)
        # Remove previous selective state if switching
        if [[ "$PREVIOUS_MODE" == "selective" ]]; then
            rm -f "$PLUGINS_FILE"
        fi

        # Symlink all plugins
        for manifest in "$PLUGINS_DIR"/*.manifest; do
            [[ -f "$manifest" ]] || continue
            name=$(basename "$manifest" .manifest)
            printf "  ${CYAN}[plugin: %s]${RESET}\n" "$name"
            process_manifest "$manifest" "symlink"
        done

        echo "all" > "$MODE_MARKER"
        ;;

    add)
        # If switching from all, remove all symlinked plugins first
        if [[ "$PREVIOUS_MODE" == "all" ]]; then
            for manifest in "$PLUGINS_DIR"/*.manifest; do
                [[ -f "$manifest" ]] || continue
                process_manifest "$manifest" "remove"
            done
        fi

        # Vendor the requested plugin
        printf "  ${CYAN}[plugin: %s]${RESET}\n" "$PLUGIN"
        process_manifest "$PLUGINS_DIR/$PLUGIN.manifest" "vendor"

        # Update .nous-plugins
        touch "$PLUGINS_FILE"
        if ! grep -qxF "$PLUGIN" "$PLUGINS_FILE"; then
            echo "$PLUGIN" >> "$PLUGINS_FILE"
            printf "  ${GREEN}added${RESET}   %s to .nous-plugins\n" "$PLUGIN"
        fi

        echo "selective" > "$MODE_MARKER"
        ;;

    rm)
        # Remove the plugin's files
        printf "  ${CYAN}[removing: %s]${RESET}\n" "$PLUGIN"
        process_manifest "$PLUGINS_DIR/$PLUGIN.manifest" "remove"

        # Remove from .nous-plugins
        if [[ -f "$PLUGINS_FILE" ]]; then
            grep -vxF "$PLUGIN" "$PLUGINS_FILE" > "$PLUGINS_FILE.tmp" || true
            mv "$PLUGINS_FILE.tmp" "$PLUGINS_FILE"
            printf "  ${GREEN}removed${RESET} %s from .nous-plugins\n" "$PLUGIN"
        fi
        ;;

    refresh)
        if [[ "$PREVIOUS_MODE" == "all" ]]; then
            # Re-symlink all plugins
            for manifest in "$PLUGINS_DIR"/*.manifest; do
                [[ -f "$manifest" ]] || continue
                name=$(basename "$manifest" .manifest)
                printf "  ${CYAN}[plugin: %s]${RESET}\n" "$name"
                process_manifest "$manifest" "symlink"
                done
        elif [[ "$PREVIOUS_MODE" == "selective" ]]; then
            # Re-vendor selected plugins
            if [[ -f "$PLUGINS_FILE" ]]; then
                while IFS= read -r plugin; do
                    [[ -z "$plugin" ]] && continue
                    if [[ -f "$PLUGINS_DIR/$plugin.manifest" ]]; then
                        printf "  ${CYAN}[plugin: %s]${RESET}\n" "$plugin"
                        process_manifest "$PLUGINS_DIR/$plugin.manifest" "vendor"
                    else
                        printf "  ${YELLOW}skipped${RESET} %s (manifest not found)\n" "$plugin"
                    fi
                done < "$PLUGINS_FILE"
            else
                echo "  No plugins selected. Use --add <plugin> to add one."
            fi
        fi
        ;;
esac

# ── Go module wiring ─────────────────────────────────────────────────────────
NOUS_MODULE="github.com/xianxu/nous"

if [[ ! -f "$TARGET_DIR/go.mod" ]]; then
    MOD_PATH=""
    if remote=$(git -C "$TARGET_DIR" remote get-url origin 2>/dev/null); then
        MOD_PATH=$(echo "$remote" | sed 's|^https://||; s|^git@||; s|:|/|; s|\.git$||')
    fi
    MOD_PATH="${MOD_PATH:-example.com/brain}"
    printf "module %s\n\ngo 1.22\n" "$MOD_PATH" > "$TARGET_DIR/go.mod"
    printf "  ${GREEN}created${RESET} go.mod (module %s)\n" "$MOD_PATH"
fi

TARGET_MODULE=$(head -1 "$TARGET_DIR/go.mod" | awk '{print $2}')

if [[ "$TARGET_MODULE" != "$NOUS_MODULE" ]]; then
    MODE_NOW=$(cat "$MODE_MARKER" 2>/dev/null || echo "")
    if [[ "$MODE_NOW" == "selective" ]]; then
        # Vendor mode: rewrite import paths
        find "$TARGET_DIR/cmd" "$TARGET_DIR/lib" -name '*.go' -exec \
            sed -i '' "s|$NOUS_MODULE|$TARGET_MODULE|g" {} + 2>/dev/null || true
        printf "  ${GREEN}rewrote${RESET} imports: %s → %s\n" "$NOUS_MODULE" "$TARGET_MODULE"
    elif [[ "$MODE_NOW" == "all" ]]; then
        # Symlink mode: add replace directive
        NOUS_REL=$(rel_path "$NOUS_DIR" "$TARGET_DIR")
        if ! grep -q "replace $NOUS_MODULE" "$TARGET_DIR/go.mod" 2>/dev/null; then
            if ! grep -q "require $NOUS_MODULE" "$TARGET_DIR/go.mod"; then
                printf "\nrequire %s v0.0.0\n" "$NOUS_MODULE" >> "$TARGET_DIR/go.mod"
            fi
            printf "\nreplace %s => %s\n" "$NOUS_MODULE" "$NOUS_REL" >> "$TARGET_DIR/go.mod"
            printf "  ${GREEN}added${RESET}   go.mod replace: %s => %s\n" "$NOUS_MODULE" "$NOUS_REL"
        fi
    fi
fi

# ── Ensure Makefile.local includes Makefile.nous + upstream override ────────
# UPSTREAM_NAME/UPSTREAM_REFRESH are read by Makefile.workflow's `refresh`
# target. Defining them in Makefile.local (included after Makefile.workflow)
# overrides the ariadne-default `?=` assignments via lazy recipe expansion,
# so `make refresh` calls back into nous instead of ariadne.
MAKEFILE_LOCAL="$TARGET_DIR/Makefile.local"
if [[ -f "$MAKEFILE_LOCAL" ]]; then
    if ! grep -q 'Makefile\.nous' "$MAKEFILE_LOCAL"; then
        printf '\n-include Makefile.nous\n' >> "$MAKEFILE_LOCAL"
        printf "  ${GREEN}updated${RESET} Makefile.local (added Makefile.nous include)\n"
    fi
    if ! grep -q 'UPSTREAM_NAME' "$MAKEFILE_LOCAL"; then
        NOUS_REL_FROM_TARGET=$(rel_path "$NOUS_DIR" "$TARGET_DIR")
        cat >> "$MAKEFILE_LOCAL" <<EOF

# Refresh from nous (set by nous/setup.sh)
UPSTREAM_NAME    := nous
UPSTREAM_REFRESH := $NOUS_REL_FROM_TARGET/nous/setup.sh
EOF
        printf "  ${GREEN}updated${RESET} Makefile.local (added UPSTREAM_NAME=nous override)\n"
    fi
fi

# ── Ensure .gitignore entries ────────────────────────────────────────────────
GITIGNORE="$TARGET_DIR/.gitignore"
NOUS_IGNORES=(
    ".nous-mode"
    ".nous-plugins"
    "cmd/*/bin/"
)

touch "$GITIGNORE"
gitignore_changed=false
for entry in "${NOUS_IGNORES[@]}"; do
    if ! grep -qxF "$entry" "$GITIGNORE"; then
        echo "$entry" >> "$GITIGNORE"
        gitignore_changed=true
    fi
done

if "$gitignore_changed"; then
    printf "  ${GREEN}updated${RESET} .gitignore\n"
fi

printf "\n${GREEN}Done.${RESET} Review changes, then commit.\n"
