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
#
# Checksums are computed ONCE, by the leg that built the asset (`<asset>.sha256`
# beside it, sha256sum format, bare name). `collect` parses and verifies each
# one, ships the asset, and aggregates the lines into dist/SHA256SUMS, which is
# itself a release asset. `notes` and `publish-dispatch` read digests from that
# manifest and refuse rather than render an empty one; nothing here rehashes.
#
# Every function is written to be correct with errexit OFF: a function called
# as `f || …` or inside `$(…)` runs without `set -e`, so each side effect is
# guarded explicitly and each lookup is assigned before it is printed.
# Contract: any-ui docs/specs/2026-10-10-any-nightly-per-platform-publish.md R2–R4.
set -euo pipefail

: "${DESKTOP_RESULT:=skipped}" "${DESKTOP_SMOKE_RESULT:=skipped}" "${ANDROID_RESULT:=skipped}" "${IOS_RESULT:=skipped}"
: "${VERSION_RESULT:=success}" "${PUBLISH_RESULT:=success}"

readonly FANIN_CLIENTS="anyproto/any-ui anyproto/anytype-swift anyproto/any-kotlin"

_fanin_error() { # <message> — a refusal: an annotated ::error:: on stderr, so the run summary names it
    echo "::error::release-fanin: $1" >&2
}

fanin_platform_ok() { # <desktop|android|ios> → 0 when the platform passed
    case "$1" in
    desktop) [ "$DESKTOP_RESULT" = success ] && [ "$DESKTOP_SMOKE_RESULT" = success ] ;;
    android) [ "$ANDROID_RESULT" = success ] ;;
    ios) [ "$IOS_RESULT" = success ] ;;
    *)
        _fanin_error "unknown platform $1"
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

fanin_any_ok() { # → 0 when at least one platform passed; the publish job's own gate, re-checked by collect
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
        _fanin_error "unknown client $1"
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

_fanin_sha256_check() { # <dir> <name.sha256> — the bytes that arrived against the digest the leg wrote
    # sha256sum on the ubuntu publish runner; shasum is the fallback for a host without it.
    if command -v sha256sum >/dev/null 2>&1; then (cd "$1" && sha256sum -c --quiet "$2"); else (cd "$1" && shasum -a 256 -c --quiet "$2"); fi
}

_fanin_verify_line() { # <dir> <name> — the leg's digest file is exactly one sha256sum line naming the bare asset
    local file="$1/$2.sha256" lines
    lines="$(grep -c '' "$file")" || lines=0
    if [ "$lines" != 1 ]; then
        _fanin_error "$2.sha256 has $lines lines; a leg writes exactly one"
        return 1
    fi
    if ! grep -qE "^[0-9a-f]{64}  $2\$" "$file"; then
        _fanin_error "$2.sha256 is not '<sha256>  $2' — the leg hashed the wrong file or wrote a path, not the bare name"
        return 1
    fi
}

_fanin_digest_of() { # <dist-dir> <asset name> → the ONE digest the manifest records for it
    local d
    d="$(awk -v n="$2" '$2 == n { print $1 }' "$1/SHA256SUMS")"
    case "$d" in
    '')
        _fanin_error "$2 is not in $1/SHA256SUMS"
        return 1
        ;;
    *$'\n'*)
        _fanin_error "$2 appears more than once in $1/SHA256SUMS"
        return 1
        ;;
    esac
    printf '%s' "$d"
}

# shellcheck disable=SC2086  # the glob IS the argument: which files of the artifact dir to ship
_fanin_take() { # <artifact-dir> <dist-dir> <glob> — verify each file against the digest its leg wrote, ship it, record the line
    local dir="$1" dst="$2" pattern="$3" f name
    for f in "$dir"/$pattern; do
        name="$(basename "$f")"
        if [ ! -f "$f" ]; then
            _fanin_error "$dir has no $pattern — the leg passed but its artifact never arrived"
            return 1
        fi
        if [ ! -f "$f.sha256" ]; then
            _fanin_error "$name arrived without the digest its leg writes beside it ($name.sha256) — refusing to ship an unverified asset"
            return 1
        fi
        _fanin_verify_line "$dir" "$name" || return 1
        if ! _fanin_sha256_check "$dir" "$name.sha256"; then
            _fanin_error "$name does not match the digest its leg wrote — corrupted or substituted in transit, refusing to ship it"
            return 1
        fi
        cp "$f" "$dst/" || {
            _fanin_error "copying $name into $dst failed"
            return 1
        }
        cat "$f.sha256" >> "$dst/SHA256SUMS" || {
            _fanin_error "recording $name in $dst/SHA256SUMS failed"
            return 1
        }
    done
}

fanin_collect() { # <artifacts-dir> <dist-dir> — flatten the PASSED platforms' artifacts into one asset dir + the SHA256SUMS manifest
    local src="$1" dst="$2"
    if ! fanin_any_ok; then
        _fanin_error "no platform passed; there is nothing to publish (the publish job's own gate should have skipped it)"
        return 1
    fi
    mkdir -p "$dst"
    : > "$dst/SHA256SUMS"
    # A platform that passed but left no artifact or no digest is a publish
    # failure, not a partial release: the step fails, notify-failed reports `publish`.
    if fanin_platform_ok desktop; then _fanin_take "$src/desktop-tarballs" "$dst" 'any-*.tar.gz' || return 1; fi
    if fanin_platform_ok android; then _fanin_take "$src/android-aar" "$dst" 'any.aar' || return 1; fi
    if fanin_platform_ok ios; then _fanin_take "$src/ios-xcframework" "$dst" 'any.xcframework.zip' || return 1; fi
    ls -la "$dst"
}

# shellcheck disable=SC2016  # the backticks are markdown code spans in the release notes, not shell
fanin_notes() { # <dist-dir> → release notes on stdout, rendered FROM the manifest: partial marker first, then the sectioned asset list
    local dst="$1" line name digest llamacpp aar='' xcf=''
    # Every lookup is assigned BEFORE anything is printed, so a missing value
    # refuses the whole step instead of rendering an empty span (the base YAML's
    # `LLAMACPP="$(…)"` assignment had this property; a `$(…)` inside printf does not).
    if [ -n "${LLAMACPP_VERSION:-}" ]; then
        llamacpp="$LLAMACPP_VERSION"
    else
        llamacpp="$("$(dirname "${BASH_SOURCE[0]}")/llamacpp-version.sh")" || return 1
    fi
    if fanin_platform_ok android; then aar="$(_fanin_digest_of "$dst" any.aar)" || return 1; fi
    if fanin_platform_ok ios; then xcf="$(_fanin_digest_of "$dst" any.xcframework.zip)" || return 1; fi
    line="$(fanin_partial_line)"
    [ -z "$line" ] || printf '%s\n\n' "$line"
    printf '## Assets\n\n### Desktop\n'
    if fanin_platform_ok desktop; then
        while read -r digest name; do
            case "$name" in any-*.tar.gz) printf '`%s` sha256: `%s`\n' "$name" "$digest" ;; esac
        done < "$dst/SHA256SUMS"
    else echo 'not built'; fi
    printf '\n### Android\n'
    if fanin_platform_ok android; then printf '`any.aar` sha256: `%s`\n' "$aar"; else echo 'not built'; fi
    printf '\n### iOS\n'
    if fanin_platform_ok ios; then printf '`any.xcframework.zip` sha256: `%s`\n' "$xcf"; else echo 'not built'; fi
    printf '\n### Libs\nllama.cpp: %s\n' "$llamacpp"
    printf '\n### Checksums\n`SHA256SUMS` lists every asset above, each digest computed by the leg that built it; verify with `sha256sum -c SHA256SUMS`.\n'
}

_fanin_dispatch() { # <repo> <event_type> [gh -f args…] — one dispatch; a failure warns, the caller decides what it means
    local repo="$1" type="$2"
    shift 2
    if "${GH:-gh}" api "repos/$repo/dispatches" -f "event_type=$type" "$@"; then
        echo "dispatched $type → $repo"
    else
        echo "::warning::release-fanin: repository_dispatch $type to $repo failed"
        return 1
    fi
}

_fanin_none_succeeded() { # <attempted> <succeeded> — zero of N is not best-effort, it is a token that is set but unusable
    if [ "$1" -gt 0 ] && [ "$2" -eq 0 ]; then
        _fanin_error "every one of $1 client dispatches failed — ANY_CI_TOKEN is set but not usable; what was published stands, the clients were not told"
        return 1
    fi
}

fanin_publish_dispatch() { # <dist-dir> <github-output> — any-published to every client whose platform passed
    local dst="$1" out="$2" c published="" attempted=0 ok=0 d
    for c in $(fanin_clients_to_publish); do
        # Recorded BEFORE the attempt: the release IS out, and a client must
        # never get any-build-failed for a release it can consume (R4), even if
        # this very dispatch fails or the loop dies after it.
        published="${published:+$published,}$c"
        attempted=$((attempted + 1))
        case "$c" in
        anyproto/any-ui)
            if _fanin_dispatch "$c" any-published -f "client_payload[version]=$VERSION" -f "client_payload[channel]=$CHANNEL"; then ok=$((ok + 1)); fi
            ;;
        anyproto/anytype-swift)
            d="$(_fanin_digest_of "$dst" any.xcframework.zip)" || return 1
            if _fanin_dispatch "$c" any-published -f "client_payload[version]=$VERSION" -f "client_payload[channel]=$CHANNEL" \
                -f "client_payload[asset]=any.xcframework.zip" -f "client_payload[sha256]=$d"; then ok=$((ok + 1)); fi
            ;;
        anyproto/any-kotlin)
            d="$(_fanin_digest_of "$dst" any.aar)" || return 1
            if _fanin_dispatch "$c" any-published -f "client_payload[version]=$VERSION" -f "client_payload[channel]=$CHANNEL" \
                -f "client_payload[asset]=any.aar" -f "client_payload[sha256]=$d"; then ok=$((ok + 1)); fi
            ;;
        esac
    done
    echo "published_clients=$published" >> "$out" || return 1
    _fanin_none_succeeded "$attempted" "$ok"
}

fanin_fail_dispatch() { # any-build-failed to every client that got no any-published
    local c stages attempted=0 ok=0
    stages="$(fanin_failed_stages)"
    if [ -z "$stages" ]; then
        # Nothing failed, so nobody is told a build failed (R4: at most one of
        # the two events). A client missing from PUBLISHED_CLIENTS on a green run
        # means the publish job's output did not reach this one — say so.
        if [ -n "$(fanin_clients_to_fail "${PUBLISHED_CLIENTS:-}")" ]; then
            echo "::warning::release-fanin: nothing failed but published_clients does not name every client (got '${PUBLISHED_CLIENTS:-}') — the publish job's output did not reach notify-failed; no any-build-failed is sent"
        fi
        return 0
    fi
    for c in $(fanin_clients_to_fail "${PUBLISHED_CLIENTS:-}"); do
        attempted=$((attempted + 1))
        if _fanin_dispatch "$c" any-build-failed \
            -f "client_payload[version]=${VERSION:-unresolved}" \
            -f "client_payload[channel]=$CHANNEL" \
            -f "client_payload[platform]=$(fanin_client_platform "$c")" \
            -f "client_payload[failed_platforms]=$stages" \
            -f "client_payload[run_url]=$RUN_URL"; then ok=$((ok + 1)); fi
    done
    _fanin_none_succeeded "$attempted" "$ok"
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
