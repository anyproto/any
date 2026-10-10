#!/usr/bin/env bash
# The publish fan-in of _build-any.yml, out of the YAML so it can be tested
# without a runner (scripts/release-fanin.test.sh, run by `make test-scripts`).
#
# Usage: scripts/release-fanin.sh collect <artifacts-dir> <dist-dir>
#        scripts/release-fanin.sh notes <dist-dir>
#        scripts/release-fanin.sh publish-dispatch <dist-dir> <github-output>
#        scripts/release-fanin.sh fail-dispatch
#   or source it for the fanin_* functions (the test does).
#
# A platform is one of desktop | android | ios. desktop passes only when BOTH
# the desktop and desktop-smoke jobs succeeded. Every function reads the job
# results GitHub hands the publish / notify-failed jobs as env:
#   VERSION_RESULT DESKTOP_RESULT DESKTOP_SMOKE_RESULT ANDROID_RESULT IOS_RESULT PUBLISH_RESULT
# plus VERSION, CHANNEL, RUN_URL and, for the failure dispatch, PUBLISHED_CLIENTS.
# Contract: any-ui docs/specs/2026-10-10-any-nightly-per-platform-publish.md R2–R4.
set -euo pipefail

: "${DESKTOP_RESULT:=skipped}" "${DESKTOP_SMOKE_RESULT:=skipped}" "${ANDROID_RESULT:=skipped}" "${IOS_RESULT:=skipped}"
: "${VERSION_RESULT:=success}" "${PUBLISH_RESULT:=success}"

readonly FANIN_CLIENTS="anyproto/any-ui anyproto/anytype-swift anyproto/any-kotlin"

fanin_platform_ok() { # <desktop|android|ios> → 0 when the platform passed
    case "$1" in
    desktop) [ "$DESKTOP_RESULT" = success ] && [ "$DESKTOP_SMOKE_RESULT" = success ] ;;
    android) [ "$ANDROID_RESULT" = success ] ;;
    ios) [ "$IOS_RESULT" = success ] ;;
    *)
        echo "fanin: unknown platform $1" >&2
        return 2
        ;;
    esac
}

fanin_failed_platforms() { # → comma-separated platforms that did not pass
    local p out=""
    for p in desktop android ios; do
        fanin_platform_ok "$p" || out="${out:+$out,}$p"
    done
    printf '%s' "$out"
}

fanin_any_ok() { # → 0 when at least one platform passed
    local p
    for p in desktop android ios; do fanin_platform_ok "$p" && return 0; done
    return 1
}

fanin_partial_line() { # → the R2 marker line, nothing when complete
    local failed
    failed="$(fanin_failed_platforms)"
    [ -n "$failed" ] || return 0
    printf '> **Partial release** — missing: %s (run %s)\n' "${failed//,/, }" "${RUN_URL:?}"
}

fanin_client_platform() { # <repo> → the one platform that client consumes
    case "$1" in
    anyproto/any-ui) echo desktop ;;
    anyproto/anytype-swift) echo ios ;;
    anyproto/any-kotlin) echo android ;;
    *)
        echo "fanin: unknown client $1" >&2
        return 2
        ;;
    esac
}

fanin_clients_to_publish() { # → one repo per line, every client whose platform passed
    local c
    for c in $FANIN_CLIENTS; do
        fanin_platform_ok "$(fanin_client_platform "$c")" && echo "$c"
    done
    return 0
}

fanin_clients_to_fail() { # <published-csv> → one repo per line, every client NOT in the list
    local c
    for c in $FANIN_CLIENTS; do
        case ",$1," in
        *",$c,"*) ;;
        *) echo "$c" ;;
        esac
    done
}

fanin_failed_stages() { # → the failed_platforms payload value: platforms, plus version/publish as stages
    if [ "$VERSION_RESULT" != success ]; then
        printf 'version'
        return 0
    fi
    local out
    out="$(fanin_failed_platforms)"
    # A SKIPPED publish is "every platform red" (R2), already named above; only
    # a publish that RAN and failed is a stage of its own.
    [ "$PUBLISH_RESULT" != failure ] || out="${out:+$out,}publish"
    printf '%s' "$out"
}

_fanin_sha256_check() { # <dir> <name.sha256> — verify the asset the leg hashed against the bytes that arrived
    # sha256sum on the ubuntu publish runner; shasum where the test runs on a Mac.
    if command -v sha256sum >/dev/null 2>&1; then (cd "$1" && sha256sum -c --quiet "$2"); else (cd "$1" && shasum -a 256 -c --quiet "$2"); fi
}

_fanin_digest_of() { # <dist-dir> <asset name> → the digest the manifest records for it; fails if absent
    local d
    d="$(awk -v n="$2" '$2 == n { print $1 }' "$1/SHA256SUMS")"
    if [ -z "$d" ]; then
        echo "fanin: $2 is not in $1/SHA256SUMS" >&2
        return 1
    fi
    printf '%s' "$d"
}

# shellcheck disable=SC2086  # the glob IS the argument: which files of the artifact dir to ship
_fanin_take() { # <artifact-dir> <dist-dir> <glob> — verify each file against the digest its leg wrote, ship it, record the line
    local dir="$1" dst="$2" pattern="$3" f name
    for f in "$dir"/$pattern; do
        name="$(basename "$f")"
        if [ ! -f "$f.sha256" ]; then
            echo "fanin: $name arrived without the digest its leg writes beside it ($name.sha256) — refusing to ship an unverified asset" >&2
            return 1
        fi
        if ! _fanin_sha256_check "$dir" "$name.sha256"; then
            echo "fanin: $name does not match the digest its leg wrote — corrupted or substituted in transit, refusing to ship it" >&2
            return 1
        fi
        cp "$f" "$dst/"
        cat "$f.sha256" >> "$dst/SHA256SUMS"
    done
}

fanin_collect() { # <artifacts-dir> <dist-dir> — flatten the PASSED platforms' artifacts into one asset dir + the SHA256SUMS manifest
    local src="$1" dst="$2"
    mkdir -p "$dst"
    : > "$dst/SHA256SUMS"
    # Every digest is the one the LEG computed over what it built; publish only
    # verifies and aggregates. A platform that passed but left no artifact or no
    # digest is a publish failure, not a partial release: the step fails,
    # notify-failed reports `publish`. The manifest ships as a release asset
    # (dist/*), so a client verifies with `sha256sum -c SHA256SUMS`.
    # `|| return 1` on each: inside a `then` body a sourced caller under `set +e`
    # would otherwise carry on past a refused asset and report success.
    if fanin_platform_ok desktop; then _fanin_take "$src/desktop-tarballs" "$dst" 'any-*.tar.gz' || return 1; fi
    if fanin_platform_ok android; then _fanin_take "$src/android-aar" "$dst" 'any.aar' || return 1; fi
    if fanin_platform_ok ios; then _fanin_take "$src/ios-xcframework" "$dst" 'any.xcframework.zip' || return 1; fi
    ls -la "$dst"
}

# shellcheck disable=SC2016  # the backticks are markdown code spans in the release notes, not shell
fanin_notes() { # <dist-dir> → release notes on stdout, rendered FROM the manifest: partial marker first, then the sectioned asset list
    local dst="$1" line name digest
    line="$(fanin_partial_line)"
    [ -z "$line" ] || printf '%s\n\n' "$line"
    printf '## Assets\n\n### Desktop\n'
    if fanin_platform_ok desktop; then
        while read -r digest name; do
            case "$name" in any-*.tar.gz) printf '`%s` sha256: `%s`\n' "$name" "$digest" ;; esac
        done < "$dst/SHA256SUMS"
    else echo 'not built'; fi
    printf '\n### Android\n'
    if fanin_platform_ok android; then printf '`any.aar` sha256: `%s`\n' "$(_fanin_digest_of "$dst" any.aar)"; else echo 'not built'; fi
    printf '\n### iOS\n'
    if fanin_platform_ok ios; then printf '`any.xcframework.zip` sha256: `%s`\n' "$(_fanin_digest_of "$dst" any.xcframework.zip)"; else echo 'not built'; fi
    printf '\n### Libs\nllama.cpp: %s\n' "${LLAMACPP_VERSION:-$("$(dirname "${BASH_SOURCE[0]}")/llamacpp-version.sh")}"
    printf '\n### Checksums\n`SHA256SUMS` lists every asset above, each digest computed by the leg that built it; verify with `sha256sum -c SHA256SUMS`.\n'
}

_fanin_dispatch() { # <repo> <event_type> [gh -f args…] — best-effort, warns on failure
    local repo="$1" type="$2"
    shift 2
    if "${GH:-gh}" api "repos/$repo/dispatches" -f "event_type=$type" "$@"; then
        echo "dispatched $type → $repo"
    else
        echo "::warning::repository_dispatch $type to $repo failed"
        return 1
    fi
}

fanin_publish_dispatch() { # <dist-dir> <github-output> — any-published to every client whose platform passed
    local dst="$1" c published=""
    for c in $(fanin_clients_to_publish); do
        case "$c" in
        anyproto/any-ui)
            _fanin_dispatch "$c" any-published -f "client_payload[version]=$VERSION" -f "client_payload[channel]=$CHANNEL" || true
            ;;
        anyproto/anytype-swift)
            _fanin_dispatch "$c" any-published -f "client_payload[version]=$VERSION" -f "client_payload[channel]=$CHANNEL" \
                -f "client_payload[asset]=any.xcframework.zip" -f "client_payload[sha256]=$(_fanin_digest_of "$dst" any.xcframework.zip)" || true
            ;;
        anyproto/any-kotlin)
            _fanin_dispatch "$c" any-published -f "client_payload[version]=$VERSION" -f "client_payload[channel]=$CHANNEL" \
                -f "client_payload[asset]=any.aar" -f "client_payload[sha256]=$(_fanin_digest_of "$dst" any.aar)" || true
            ;;
        esac
        # Attempted counts as published: the release IS out, and a client must
        # never get any-build-failed for a release it can consume (R4).
        published="${published:+$published,}$c"
    done
    echo "published_clients=$published" >> "$2"
}

fanin_fail_dispatch() { # any-build-failed to every client that got no any-published
    local c stages
    stages="$(fanin_failed_stages)"
    for c in $(fanin_clients_to_fail "${PUBLISHED_CLIENTS:-}"); do
        _fanin_dispatch "$c" any-build-failed \
            -f "client_payload[version]=${VERSION:-unresolved}" \
            -f "client_payload[channel]=$CHANNEL" \
            -f "client_payload[platform]=$(fanin_client_platform "$c")" \
            -f "client_payload[failed_platforms]=$stages" \
            -f "client_payload[run_url]=$RUN_URL" || true
    done
}

if [ "${BASH_SOURCE[0]}" = "$0" ]; then
    case "${1:-}" in
    collect) fanin_collect "$2" "$3" ;;
    notes) fanin_notes "$2" ;;
    publish-dispatch) fanin_publish_dispatch "$2" "$3" ;;
    fail-dispatch) fanin_fail_dispatch ;;
    *)
        echo "usage: release-fanin.sh collect <artifacts> <dist> | notes <dist> | publish-dispatch <dist> <github-output> | fail-dispatch" >&2
        exit 2
        ;;
    esac
fi
