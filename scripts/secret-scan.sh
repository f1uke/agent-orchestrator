#!/usr/bin/env bash
# Scan for committed secrets with gitleaks and the rules in .gitleaks.toml.
#
#   scripts/secret-scan.sh staged          what `git commit` is about to add (the pre-commit hook)
#   scripts/secret-scan.sh range A..B      every commit in a range (CI: the PR's or the push's commits)
#   scripts/secret-scan.sh all             every commit since the baseline in .gitleaks-baseline
#
# Findings are printed redacted - rule, file, line, commit, fingerprint - never
# the secret itself, so a CI log or terminal scrollback cannot leak it again.
# What to do when it fires: AGENTS.md, "Secret scanning".
#
# Extra rules that must not be published (say, exact values of accounts you
# know are in use) can live in an untracked gitleaks config, scanned in a
# second pass: $GITLEAKS_EXTRA_CONFIG if set, else <git-common-dir>/info/gitleaks-extra.toml.
set -euo pipefail

# The version CI installs. A local `gitleaks` on PATH is used as is; without
# one this exact version is built and cached by `go run`.
gitleaks_version="8.30.1"
leaks_exit=3

repo_root="$(git rev-parse --show-toplevel)"
cd "${repo_root}"

usage() {
  sed -n '2,7p' "$0" | sed 's/^# \{0,1\}//' >&2
  exit 2
}

gitleaks_cmd() {
  if command -v gitleaks >/dev/null 2>&1; then
    gitleaks "$@"
  elif command -v go >/dev/null 2>&1; then
    go run "github.com/zricethezav/gitleaks/v8@v${gitleaks_version}" "$@"
  else
    echo "secret-scan: neither gitleaks nor go is installed; install one (brew install gitleaks)." >&2
    return 1
  fi
}

# One line per finding, built by gitleaks itself so no secret field is ever read.
template="$(mktemp)"
report="$(mktemp)"
trap 'rm -f "${template}" "${report}"' EXIT
if [[ "${GITHUB_ACTIONS:-}" == "true" ]]; then
  cat >"${template}" <<'TMPL'
{{- range . }}
::error file={{ .File }},line={{ .StartLine }},title=Secret scan: {{ .RuleID }}::{{ .Description }} (commit {{ printf "%.12s" .Commit }}, fingerprint {{ .Fingerprint }})
{{- end }}
TMPL
else
  cat >"${template}" <<'TMPL'
{{- range . }}
  {{ .RuleID }}  {{ .File }}:{{ .StartLine }}{{ if .Commit }}  commit {{ printf "%.12s" .Commit }}{{ end }}
    {{ .Description }}
    fingerprint: {{ .Fingerprint }}
{{- end }}
TMPL
fi

# scan <config> <gitleaks git args...>: 0 clean, 3 leaks, anything else an error.
scan() {
  local config="$1"
  shift
  local status=0
  gitleaks_cmd git "$@" \
    --config "${config}" \
    --gitleaks-ignore-path "${repo_root}" \
    --redact \
    --no-banner \
    --log-level error \
    --exit-code "${leaks_exit}" \
    --report-format template \
    --report-template "${template}" \
    --report-path "${report}" \
    "${repo_root}" || status=$?
  if [[ "${status}" -eq "${leaks_exit}" ]]; then
    cat "${report}"
    echo
  fi
  return "${status}"
}

mode="${1:-}"
case "${mode}" in
  staged)
    args=(--pre-commit --staged)
    ;;
  range)
    [[ -n "${2:-}" ]] || usage
    args=(--log-opts "$2")
    ;;
  all)
    baseline="$(grep -Eo '^[0-9a-f]{40}' .gitleaks-baseline)"
    args=(--log-opts "${baseline}..HEAD")
    ;;
  *)
    usage
    ;;
esac

extra_config="${GITLEAKS_EXTRA_CONFIG:-$(git rev-parse --path-format=absolute --git-common-dir)/info/gitleaks-extra.toml}"

status=0
scan "${repo_root}/.gitleaks.toml" "${args[@]}" || status=$?
if [[ "${status}" -ne 0 && "${status}" -ne "${leaks_exit}" ]]; then
  echo "secret-scan: gitleaks failed (exit ${status})." >&2
  exit "${status}"
fi
if [[ -f "${extra_config}" ]]; then
  extra_status=0
  scan "${extra_config}" "${args[@]}" || extra_status=$?
  if [[ "${extra_status}" -ne 0 && "${extra_status}" -ne "${leaks_exit}" ]]; then
    echo "secret-scan: gitleaks failed on ${extra_config} (exit ${extra_status})." >&2
    exit "${extra_status}"
  fi
  [[ "${extra_status}" -eq 0 ]] || status="${extra_status}"
fi

if [[ "${status}" -eq "${leaks_exit}" ]]; then
  cat >&2 <<'MSG'
secret-scan: the change adds something that looks like a secret (values are redacted above).
  - Real credential, account email or password: take it out, and treat it as leaked if it was
    ever pushed - rotate it. Read it from the environment or a git-ignored file instead.
  - Made-up value or false positive: make it obviously synthetic, or allowlist it in
    .gitleaks.toml with a comment saying why, or end the line with `gitleaks:allow`.
  Details: AGENTS.md, "Secret scanning".
MSG
  exit 1
fi
