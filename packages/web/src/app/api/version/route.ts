/**
 * GET /api/version — current AO version, latest available, and channel state.
 *
 * Backed by the same cache file that the CLI's `update-check.ts` writes to
 * (`$XDG_CACHE_HOME/ao/update-check.json` or `~/.cache/ao/update-check.json`),
 * so the dashboard banner and the CLI startup notice always agree.
 *
 * Cache-only by design — never makes a network call inside a request handler.
 * The CLI keeps the cache fresh (24 h TTL) via `scheduleBackgroundRefresh()`,
 * and `ao update --check` forces a refresh on demand.
 */

import { existsSync, readFileSync } from "node:fs";
import { homedir } from "node:os";
import { createRequire } from "node:module";
import { join } from "node:path";
import { NextResponse } from "next/server";
import {
  isVersionOutdated,
  loadGlobalConfig,
  type UpdateChannel,
} from "@aoagents/ao-core";

export const dynamic = "force-dynamic";

interface CacheData {
  latestVersion?: string;
  checkedAt?: string;
  currentVersionAtCheck?: string;
  channel?: UpdateChannel;
  /**
   * Mirrors the CLI's CacheData. Only the literal "git" matters here — for git
   * installs we read the cached `isOutdated` directly because `latestVersion`
   * is a git ref like "origin/main" (not a semver), so `isVersionOutdated`
   * would always return false.
   */
  installMethod?: string;
  isOutdated?: boolean;
}

interface VersionResponse {
  current: string;
  latest: string | null;
  channel: UpdateChannel;
  isOutdated: boolean;
  checkedAt: string | null;
}

function getCachePath(): string {
  const xdg = process.env["XDG_CACHE_HOME"];
  const base = xdg || join(homedir(), ".cache");
  return join(base, "ao", "update-check.json");
}

function readCache(): CacheData | null {
  const path = getCachePath();
  if (!existsSync(path)) return null;
  try {
    const raw = readFileSync(path, "utf-8");
    return JSON.parse(raw) as CacheData;
  } catch {
    return null;
  }
}

function getCurrentVersion(): string {
  try {
    const require = createRequire(import.meta.url);
    const pkg = require("@aoagents/ao/package.json") as { version: string };
    return pkg.version;
  } catch {
    // Fall back to the web package's own version. The dashboard ships in lockstep
    // with `@aoagents/ao` (changeset linked group), so this is a safe proxy when
    // the wrapper isn't in node_modules (dev mode).
    try {
      const require = createRequire(import.meta.url);
      const pkg = require("@aoagents/ao-web/package.json") as { version: string };
      return pkg.version;
    } catch {
      return "0.0.0";
    }
  }
}

function resolveChannel(): UpdateChannel {
  try {
    const config = loadGlobalConfig();
    return config?.updateChannel ?? "manual";
  } catch {
    return "manual";
  }
}

export async function GET() {
  const current = getCurrentVersion();
  const channel = resolveChannel();
  const cache = readCache();

  // Cache must match the active channel — otherwise we'd report a stale
  // @latest version to a user who recently switched to @nightly.
  const cacheMatchesChannel = !cache?.channel || cache.channel === channel;
  const latest = cache?.latestVersion && cacheMatchesChannel ? cache.latestVersion : null;

  // Git installs cache `latestVersion: "origin/main"` (a ref, not a semver),
  // so `isVersionOutdated(current, "origin/main")` would always return false.
  // The CLI works around this by trusting the precomputed `cached.isOutdated`
  // for git installs — mirror that here so the dashboard banner actually
  // appears when a git-installed user is behind origin/main.
  let isOutdated = false;
  if (latest && cacheMatchesChannel) {
    isOutdated =
      cache?.installMethod === "git"
        ? cache.isOutdated === true
        : isVersionOutdated(current, latest);
  }

  const body: VersionResponse = {
    current,
    latest,
    channel,
    isOutdated,
    checkedAt: cache?.checkedAt && cacheMatchesChannel ? cache.checkedAt : null,
  };

  return NextResponse.json(body);
}
