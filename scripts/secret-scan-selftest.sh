#!/usr/bin/env bash
# Check that the rules in .gitleaks.toml catch what they are for and stay quiet
# on what they are not. Run it after editing .gitleaks.toml; CI runs it too.
#
# Every sample is synthetic and assembled at run time from pieces, so this file
# never holds a whole credential-shaped string itself and the scanner it tests
# does not fire on it.
set -euo pipefail

gitleaks_version="8.30.1"
repo_root="$(git rev-parse --show-toplevel)"
work="$(mktemp -d)"
trap 'rm -rf "${work}"' EXIT

gitleaks_cmd() {
  if command -v gitleaks >/dev/null 2>&1; then
    gitleaks "$@"
  else
    go run "github.com/zricethezav/gitleaks/v8@v${gitleaks_version}" "$@"
  fi
}

pw="password"
PW="PASSWORD"
Pw="Password"
fin="finnomena.com"
# Thai pieces with no / or _ in them; joined below.
kd="าก"
s="ห"
a="ฟ"
# A Thai character as a \u escape, spelled out only at run time: gitleaks
# decodes escapes before matching, so a literal one here would be caught.
esc() { printf '\\u0e%s' "$1"; }
# 36 random characters behind GitHub's token prefix: shaped like a real token,
# never one.
gh_token="ghp_$(head -c 600 /dev/urandom | LC_ALL=C tr -dc 'A-Za-z0-9' | cut -c1-36)"

# want <expected rule id, or "none"> <file name> <content>
cases=()
want() {
  mkdir -p "${work}/$1"
  printf '%s\n' "$3" >"${work}/$1/$2"
  cases+=("$1/$2")
}

# Caught.
want company-email a.go "const tester = \"qa.bot+uat@${fin}\""
want password-literal b.go "${pw} := \"Tr0ub4dor-77\""
want password-literal c.yaml "account: {email: qa@example.com, ${pw}: 'Zq8kw2m9'}"
want password-literal-arg d.go "t.Setenv(\"MAESTRO_ACCOUNT_${PW}\", \"Zq8kw2m9\")"
want password-literal-unquoted e.env "APP_${PW}=Zq8kw2m9"
want password-literal-unquoted f.yaml "  ${pw}: Zq8kw2m9"
# "kd2s3a" typed on an iOS Thai layout, as text and as escapes.
want thai-keyboard-rendering g.go "// typed \"kd2s3a\", the field showed \"${kd}/${s}_${a}\""
want thai-keyboard-rendering h.go "want := \"$(esc 32)$(esc 01)/$(esc 2b)_$(esc 1f)\""
want github-pat i.go "token := \"${gh_token}\""

# Quiet.
want none j.go "${pw} := os.Getenv(\"APP_${PW}\")"
want none k.go "before := screen(field{\"0.14\", \"${pw}\", \"Password\"})"
want none l.go "for _, p := range []string{\"/etc/passwd\", \"~/.ssh/id_rsa\"} {"
want none m.json "{\"email\": \"\${MAESTRO_ACCOUNT_EMAIL}\", \"${pw}\": \"\${MAESTRO_ACCOUNT_${PW}}\"}"
want none n.md "Set \`CSC_KEY_${PW}\`, \`apple-api-key-base64\` and \`${pw}: <redacted>\`."
want none o.sh "git clone git@gitlab.${fin}:mobile/app.git"
want none p.go "if res.Flow.Name != \"เข้าสู่ระบบ-แล้วไปหน้าแรก\" {"
want none q.go "executeCLI(t, deps, \"sim\", \"type\", \"lf86428\") // arrives as \"สดคุภ/ค\""
want none r.go "account := Account{Email: \"someone@example.com\", ${Pw}: \"lf86428\"}"
want none s.go "${pw} := \"example-${pw}-1\""

template="${work}/found.tmpl"
printf '%s\n' '{{- range . }}{{ .File }} {{ .RuleID }}' '{{ end }}' >"${template}"
status=0
gitleaks_cmd dir "${work}" \
  --config "${repo_root}/.gitleaks.toml" \
  --redact --no-banner --log-level error --exit-code 0 \
  --report-format template --report-template "${template}" \
  --report-path "${work}/found.txt" || status=$?
if [[ "${status}" -ne 0 ]]; then
  echo "secret-scan-selftest: gitleaks failed (exit ${status})" >&2
  exit "${status}"
fi

failures=0
for c in "${cases[@]}"; do
  expected="${c%%/*}"
  got="$(awk -v f="${work}/${c}" '$1 == f { print $2 }' "${work}/found.txt" | sort -u | paste -sd, -)"
  got="${got:-none}"
  if [[ "${got}" == "${expected}" ]]; then
    echo "ok    ${c#*/}  ${expected}"
  else
    echo "FAIL  ${c#*/}  want ${expected}, got ${got}"
    failures=$((failures + 1))
  fi
done

if [[ "${failures}" -gt 0 ]]; then
  echo "secret-scan-selftest: ${failures} of ${#cases[@]} cases failed" >&2
  exit 1
fi
echo "secret-scan-selftest: all ${#cases[@]} cases pass"
