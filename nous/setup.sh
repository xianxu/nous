#!/usr/bin/env bash
# Nous Layer Setup
# Bootstraps a target repo with nous infrastructure and plugins.
#
# Usage:
#   cd /path/to/your-repo && ../nous/nous/setup.sh [--vendor] [--yes]
#
#   --vendor   Copy files instead of symlinking (for public repos that
#              can't depend on nous as a sibling clone). Re-running
#              refreshes.
#   --yes      Skip confirmation prompt when switching modes.
#
# Mode is recorded in .nous-mode (content: "symlink" or "vendor"),
# mirroring ariadne/construct/setup.sh's pattern. Idempotent — safe
# to re-run for updates.
#
# Historical: pre-2026-05-19 this script had --all / --add <plugin>
# / --rm <plugin> for selective plugin management. That distinction
# was operator-confusing without solving a real problem (the plugin
# set is small and operators always wanted everything). Switched to
# the simpler ariadne-shaped two-mode design. Old .nous-mode values
# "all" and "selective" are auto-migrated to "symlink" / "vendor"
# on first run.
set -euo pipefail

# ── Parse flags ───────────────────────────────────────────────────────────────
# MODE empty here = "use previous mode if .nous-mode exists, else symlink".
# Explicit --vendor / --symlink overrides.
MODE=""
ASSUME_YES=false
for arg in "$@"; do
    case "$arg" in
        --vendor)  MODE="vendor" ;;
        --symlink) MODE="symlink" ;;
        --yes|-y)  ASSUME_YES=true ;;
        *)         echo "Error: unknown flag: $arg" >&2; exit 2 ;;
    esac
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

    # Apply base-layer .gitignore entries — the rest of construct/setup.sh's
    # consumer flow is skipped in self-mode, but the base layer still owns
    # the .gitignore list (`.DS_Store`, `.openshell/.bootstrap/`, etc.).
    # Without this, nous's .gitignore drifts behind every consumer.
    APPLY_GITIGNORE="$TARGET_DIR/construct/scripts/apply-gitignore-entries.sh"
    if [[ ! -f "$APPLY_GITIGNORE" ]] && [[ -d "$ARIADNE_DIR/construct" ]]; then
        APPLY_GITIGNORE="$ARIADNE_DIR/construct/scripts/apply-gitignore-entries.sh"
    fi
    if [[ -f "$APPLY_GITIGNORE" ]]; then
        printf "\n  ${CYAN}[gitignore]${RESET}\n"
        bash "$APPLY_GITIGNORE" "$TARGET_DIR" || true
    fi

    printf "\n${GREEN}Done.${RESET}\n"
    exit 0
fi

# ── State files + mode resolution ─────────────────────────────────────────────
MODE_MARKER="$TARGET_DIR/.nous-mode"
LEGACY_PLUGINS_FILE="$TARGET_DIR/.nous-plugins"
PREVIOUS_MODE=""

if [[ -f "$MODE_MARKER" ]]; then
    PREVIOUS_MODE="$(tr -d '[:space:]' < "$MODE_MARKER")"
    # Migrate legacy mode values from the pre-2026-05-19 selective-plugins
    # design: "all" → "symlink", "selective" → "vendor". Done in-memory
    # so the rest of the script sees the new vocabulary; the marker file
    # is rewritten with the canonical name at the end.
    case "$PREVIOUS_MODE" in
        all)       PREVIOUS_MODE="symlink" ;;
        selective) PREVIOUS_MODE="vendor" ;;
    esac
fi

# Resolve MODE: flag wins; else previous mode if known; else default to
# symlink (the cheapest path — operator typically wants nous tracking
# its sibling checkout).
if [[ -z "$MODE" ]]; then
    if [[ -n "$PREVIOUS_MODE" ]]; then
        MODE="$PREVIOUS_MODE"
    else
        MODE="symlink"
    fi
fi

if [[ "$MODE" != "symlink" && "$MODE" != "vendor" ]]; then
    echo "Error: invalid mode '$MODE' (expected symlink or vendor)" >&2
    exit 2
fi

# ── Confirmations ─────────────────────────────────────────────────────────────
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

# First-time setup in a new repo (no .nous-mode marker yet) — guard against
# accidental runs in the wrong directory.
if [[ -z "$PREVIOUS_MODE" ]]; then
    REPO_NAME=$(basename "$TARGET_DIR")
    printf "${YELLOW}First-time nous setup in:${RESET} ${BOLD_RED}%s${RESET}\n" "$REPO_NAME"
    printf "  Path:   %s\n" "$TARGET_DIR"
    printf "  Mode:   %s\n" "$MODE"
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

# Mode switch confirmation. Switching symlink ↔ vendor flips every
# fragment's representation, so make the operator confirm.
if [[ -n "$PREVIOUS_MODE" && "$PREVIOUS_MODE" != "$MODE" ]]; then
    if [[ "$PREVIOUS_MODE" == "symlink" && "$MODE" == "vendor" ]]; then
        confirm "Switching from symlink → vendor. All symlinked fragments will be replaced by copies you own."
    else
        confirm "Switching from vendor → symlink. Vendored fragments with local modifications will be REPLACED by symlinks into nous."
    fi
fi

# ── Execute ──────────────────────────────────────────────────────────────────
printf "${CYAN}Nous setup: %s → %s (mode=%s)${RESET}\n\n" "$NOUS_DIR" "$TARGET_DIR" "$MODE"

# Ariadne base layer (re-exported from nous's vendored construct/,
# .openshell/, etc.).
if [[ -f "$ARIADNE_BASE_MANIFEST" ]]; then
    printf "  ${CYAN}[ariadne base]${RESET}\n"
    process_manifest "$ARIADNE_BASE_MANIFEST" "$MODE"
fi

# Nous core (skills, Makefile.nous, scaffolds).
printf "  ${CYAN}[nous core]${RESET}\n"
process_manifest "$CORE_MANIFEST" "$MODE"

# All plugins, every time. The pre-2026-05-19 selective-plugin design
# is gone — plugin sets are small enough that always-on is operator-
# friendly, and the .nous-plugins state file caused more confusion
# than it solved.
for manifest in "$PLUGINS_DIR"/*.manifest; do
    [[ -f "$manifest" ]] || continue
    name=$(basename "$manifest" .manifest)
    printf "  ${CYAN}[plugin: %s]${RESET}\n" "$name"
    process_manifest "$manifest" "$MODE"
done

# Clean up the legacy .nous-plugins marker if it's still around from a
# pre-migration setup. The file is no longer authoritative.
if [[ -f "$LEGACY_PLUGINS_FILE" ]]; then
    rm -f "$LEGACY_PLUGINS_FILE"
    printf "  ${YELLOW}removed${RESET} legacy .nous-plugins (no longer used)\n"
fi

# Record the resolved mode for next-run refresh.
echo "$MODE" > "$MODE_MARKER"

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
    # Use the in-memory MODE rather than re-reading the marker — the
    # marker was just written with the canonical name above, but using
    # MODE keeps the branch self-contained.
    if [[ "$MODE" == "vendor" ]]; then
        # Vendored sources: rewrite import paths so the copies build
        # against the target's module path rather than nous's.
        find "$TARGET_DIR/cmd" "$TARGET_DIR/lib" -name '*.go' -exec \
            sed -i '' "s|$NOUS_MODULE|$TARGET_MODULE|g" {} + 2>/dev/null || true
        printf "  ${GREEN}rewrote${RESET} imports: %s → %s\n" "$NOUS_MODULE" "$TARGET_MODULE"
    elif [[ "$MODE" == "symlink" ]]; then
        # Symlinked sources: add a go.mod replace directive so the
        # target's go build resolves nous's module path to the sibling
        # checkout.
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
