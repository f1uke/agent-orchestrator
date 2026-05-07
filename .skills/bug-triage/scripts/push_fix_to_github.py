#!/usr/bin/env python3
"""
Push a file fix to GitHub via API and create a PR.

Usage: python3 push_fix_to_github.py <repo> <branch-name> <file-path> <commit-message> <pr-title> <pr-body>

Reads the original file content from GitHub (main branch), applies a sed-like
replacement using OLD_STRING / NEW_STRING env vars, and pushes via GitHub API.

Environment variables:
  OLD_STRING   - The exact string to find in the file (required)
  NEW_STRING   - The replacement string (required)
  BASE_SHA     - Override the base commit SHA (optional, defaults to main HEAD)

Example:
  OLD_STRING='<td className="foo">{bar}</td>' \
  NEW_STRING='<td className="foo"><a href="#">{bar}</a></td>' \
  python3 push_fix_to_github.py ComposioHQ/agent-orchestrator fix/branch packages/web/src/File.tsx \
    "fix: description" "fix: title" "Fixes #123"
"""
import sys, os, json, subprocess, base64


def run_gh(args, check=True):
    """Run a gh API command and return parsed JSON."""
    cmd = ["gh", "api"] + args
    result = subprocess.run(cmd, capture_output=True, text=True, timeout=30)
    if check and result.returncode != 0:
        print(f"ERROR: gh api failed: {result.stderr}", file=sys.stderr)
        sys.exit(1)
    try:
        return json.loads(result.stdout) if result.stdout.strip() else {}
    except json.JSONDecodeError:
        if check:
            print(f"ERROR: Invalid JSON: {result.stdout[:300]}", file=sys.stderr)
            sys.exit(1)
        return {}


if __name__ == "__main__":
    if len(sys.argv) < 7:
        print("Usage: push_fix_to_github.py <repo> <branch> <file-path> <commit-msg> <pr-title> <pr-body>", file=sys.stderr)
        sys.exit(1)

    repo = sys.argv[1]
    branch = sys.argv[2]
    file_path = sys.argv[3]
    commit_msg = sys.argv[4]
    pr_title = sys.argv[5]
    pr_body = sys.argv[6]

    old_string = os.environ.get("OLD_STRING", "")
    new_string = os.environ.get("NEW_STRING", "")

    if not old_string or not new_string:
        print("ERROR: OLD_STRING and NEW_STRING env vars are required", file=sys.stderr)
        sys.exit(1)

    # 1. Get current file content and SHA from GitHub
    print(f"Fetching {file_path} from {repo}...")
    file_data = run_gh([f"repos/{repo}/contents/{file_path}"])
    file_sha = file_data["sha"]
    decoded_content = base64.b64decode(file_data["content"]).decode("utf-8")

    # 2. Get main HEAD SHA
    base_sha = os.environ.get("BASE_SHA")
    if not base_sha:
        ref_data = run_gh([f"repos/{repo}/git/ref/heads/main"])
        base_sha = ref_data["object"]["sha"]

    print(f"Base SHA: {base_sha}")
    print(f"File SHA: {file_sha}")

    # 3. Apply replacement
    if old_string not in decoded_content:
        print(f"ERROR: OLD_STRING not found in file!", file=sys.stderr)
        print(f"Looking for:\n{old_string}", file=sys.stderr)
        sys.exit(1)

    new_content = decoded_content.replace(old_string, new_string, 1)
    encoded = base64.b64encode(new_content.encode("utf-8")).decode("ascii")

    # 4. Create branch (ignore error if exists)
    print(f"Creating branch {branch}...")
    run_gh([
        "-X", "POST", f"repos/{repo}/git/refs",
        "-f", f"ref=refs/heads/{branch}",
        "-f", f"sha={base_sha}"
    ], check=False)

    # 5. Push file to branch
    print(f"Pushing updated file...")
    result = run_gh([
        "-X", "PUT", f"repos/{repo}/contents/{file_path}",
        "-f", f"message={commit_msg}",
        "-f", f"content={encoded}",
        "-f", f"sha={file_sha}",
        "-f", f"branch={branch}"
    ])

    # 6. Create PR
    print(f"Creating PR...")
    pr_result = run_gh([
        "-X", "POST", f"repos/{repo}/pulls",
        "-f", f"title={pr_title}",
        "-f", f"body={pr_body}",
        "-f", f"head={branch}",
        "-f", "base=main"
    ])

    pr_url = pr_result.get("html_url", "unknown")
    pr_number = pr_result.get("number", "?")
    print(f"\n✅ PR #{pr_number}: {pr_url}")
