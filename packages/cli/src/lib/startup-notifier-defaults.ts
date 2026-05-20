import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { DEFAULT_DASHBOARD_NOTIFICATION_LIMIT } from "@aoagents/ao-core";
import { parseDocument } from "yaml";
import {
  NOTIFICATION_PRIORITIES,
  asStringArray,
  type NotificationPriority,
} from "./notifier-routing.js";

type StartupDesktopMode = "enable" | "disable-default" | "preserve";

export interface StartupNotifierDefaultsOptions {
  configPath: string;
  dashboardUrl: string;
  desktopMode?: StartupDesktopMode;
}

interface StartupNotifierConfig {
  notifiers: Record<string, Record<string, unknown>>;
  notificationRouting: Record<NotificationPriority, string[]>;
}

const MANUAL_OPT_IN_NOTIFIER_NAMES = new Set([
  "composio",
  "composio-slack",
  "composio-discord",
  "composio-discord-bot",
  "composio-mail",
  "discord",
  "openclaw",
  "slack",
  "webhook",
]);

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function unique(values: string[]): string[] {
  return [...new Set(values)];
}

function arraysEqual(left: string[], right: string[]): boolean {
  return left.length === right.length && left.every((value, index) => value === right[index]);
}

function hasNotifierConfig(notifiers: Record<string, unknown>, notifierName: string): boolean {
  return isRecord(notifiers[notifierName]);
}

function isImplicitManualOptInNotifier(
  notifiers: Record<string, unknown>,
  notifierName: string,
): boolean {
  return (
    MANUAL_OPT_IN_NOTIFIER_NAMES.has(notifierName) && !hasNotifierConfig(notifiers, notifierName)
  );
}

function sanitizeNotifierReferences(
  notifiers: Record<string, unknown>,
  references: unknown,
): string[] {
  return unique(
    asStringArray(references).filter(
      (notifierName) => !isImplicitManualOptInNotifier(notifiers, notifierName),
    ),
  );
}

function configuredPlugin(
  notifiers: Record<string, unknown>,
  notifierName: string,
): string | undefined {
  const entry = notifiers[notifierName];
  if (!isRecord(entry)) return undefined;
  const plugin = entry["plugin"];
  return typeof plugin === "string" && plugin.length > 0 ? plugin : undefined;
}

function canManageNotifier(
  notifiers: Record<string, unknown>,
  notifierName: "dashboard" | "desktop",
): boolean {
  const plugin = configuredPlugin(notifiers, notifierName);
  return plugin === undefined || plugin === notifierName;
}

function ensureDashboardNotifier(notifiers: Record<string, unknown>): boolean {
  if (!canManageNotifier(notifiers, "dashboard")) return false;

  const existing = isRecord(notifiers["dashboard"]) ? notifiers["dashboard"] : {};
  const next = {
    ...existing,
    plugin: "dashboard",
    limit:
      typeof existing["limit"] === "number"
        ? existing["limit"]
        : DEFAULT_DASHBOARD_NOTIFICATION_LIMIT,
  };
  const changed = JSON.stringify(existing) !== JSON.stringify(next);
  notifiers["dashboard"] = next;
  return changed;
}

function ensureDesktopNotifier(notifiers: Record<string, unknown>, dashboardUrl: string): boolean {
  if (!canManageNotifier(notifiers, "desktop")) return false;

  const existing = isRecord(notifiers["desktop"]) ? notifiers["desktop"] : {};
  const existingDashboardUrl =
    typeof existing["dashboardUrl"] === "string" && existing["dashboardUrl"].length > 0
      ? existing["dashboardUrl"]
      : undefined;
  const next = {
    ...existing,
    plugin: "desktop",
    backend: typeof existing["backend"] === "string" ? existing["backend"] : "ao-app",
    dashboardUrl:
      existingDashboardUrl && !isLocalhostDashboardUrl(existingDashboardUrl)
        ? existingDashboardUrl
        : dashboardUrl,
  };
  const changed = JSON.stringify(existing) !== JSON.stringify(next);
  notifiers["desktop"] = next;
  return changed;
}

function isLocalhostDashboardUrl(value: string): boolean {
  return /^http:\/\/localhost:\d+$/.test(value);
}

function isDefaultDesktopNotifier(entry: unknown, dashboardUrl: string): boolean {
  if (!isRecord(entry)) return false;
  const allowedKeys = new Set(["plugin", "backend", "dashboardUrl"]);
  const plugin = entry["plugin"];
  const backend = entry["backend"];
  const configuredDashboardUrl = entry["dashboardUrl"];
  return (
    plugin === "desktop" &&
    (backend === undefined || backend === "ao-app") &&
    (configuredDashboardUrl === undefined ||
      configuredDashboardUrl === dashboardUrl ||
      (typeof configuredDashboardUrl === "string" &&
        isLocalhostDashboardUrl(configuredDashboardUrl))) &&
    Object.keys(entry).every((key) => allowedKeys.has(key))
  );
}

function disableDefaultDesktopNotifier(
  notifiers: Record<string, unknown>,
  dashboardUrl: string,
): boolean {
  if (!isDefaultDesktopNotifier(notifiers["desktop"], dashboardUrl)) return false;
  delete notifiers["desktop"];
  return true;
}

function hasDesktopRouting(routing: Record<string, unknown>): boolean {
  return NOTIFICATION_PRIORITIES.some((priority) =>
    asStringArray(routing[priority]).includes("desktop"),
  );
}

export function createStartupNotifierConfig(port = 3000): StartupNotifierConfig {
  const dashboardUrl = `http://localhost:${port}`;
  return {
    notifiers: {
      desktop: {
        plugin: "desktop",
        backend: "ao-app",
        dashboardUrl,
      },
      dashboard: {
        plugin: "dashboard",
        limit: DEFAULT_DASHBOARD_NOTIFICATION_LIMIT,
      },
    },
    notificationRouting: {
      urgent: ["desktop", "dashboard"],
      action: ["dashboard"],
      warning: ["dashboard"],
      info: ["dashboard"],
    },
  };
}

export function ensureStartupNotifierDefaults(options: StartupNotifierDefaultsOptions): boolean {
  if (!existsSync(options.configPath)) return false;

  const rawYaml = readFileSync(options.configPath, "utf-8");
  const doc = parseDocument(rawYaml);
  const rawConfig = (doc.toJS() as Record<string, unknown> | null) ?? {};
  const desktopMode = options.desktopMode ?? "enable";
  let changed = false;

  const notifiers = isRecord(rawConfig["notifiers"]) ? rawConfig["notifiers"] : {};
  const desktopWasConfigured = configuredPlugin(notifiers, "desktop") === "desktop";
  const routing = isRecord(rawConfig["notificationRouting"])
    ? rawConfig["notificationRouting"]
    : {};
  const shouldManageDesktopRouting =
    desktopMode === "enable" &&
    canManageNotifier(notifiers, "desktop") &&
    (!desktopWasConfigured || !hasDesktopRouting(routing));

  changed = ensureDashboardNotifier(notifiers) || changed;
  if (desktopMode === "enable") {
    changed = ensureDesktopNotifier(notifiers, options.dashboardUrl) || changed;
  } else if (desktopMode === "disable-default") {
    changed = disableDefaultDesktopNotifier(notifiers, options.dashboardUrl) || changed;
  }
  rawConfig["notifiers"] = notifiers;

  const defaults = isRecord(rawConfig["defaults"]) ? rawConfig["defaults"] : {};
  const sanitizedDefaultNotifiers = sanitizeNotifierReferences(
    notifiers,
    defaults["notifiers"],
  ).filter((notifierName) => notifierName !== "dashboard" && notifierName !== "desktop");
  if (!arraysEqual(asStringArray(defaults["notifiers"]), sanitizedDefaultNotifiers)) {
    defaults["notifiers"] = sanitizedDefaultNotifiers;
    changed = true;
  }
  rawConfig["defaults"] = defaults;

  const dashboardConfigured = configuredPlugin(notifiers, "dashboard") === "dashboard";
  const desktopConfigured = configuredPlugin(notifiers, "desktop") === "desktop";

  for (const priority of NOTIFICATION_PRIORITIES) {
    const existingRoute = sanitizeNotifierReferences(notifiers, routing[priority]);
    let nextRoute = existingRoute;

    if (desktopMode === "disable-default") {
      nextRoute = nextRoute.filter((notifierName) => notifierName !== "desktop");
    } else if (shouldManageDesktopRouting && desktopConfigured) {
      nextRoute = nextRoute.filter((notifierName) => notifierName !== "desktop");
      if (priority === "urgent") nextRoute = [...nextRoute, "desktop"];
    }

    if (dashboardConfigured) {
      nextRoute = [
        ...nextRoute.filter((notifierName) => notifierName !== "dashboard"),
        "dashboard",
      ];
    }

    nextRoute = unique(nextRoute);
    if (!arraysEqual(asStringArray(routing[priority]), nextRoute)) {
      routing[priority] = nextRoute;
      changed = true;
    }
  }
  rawConfig["notificationRouting"] = routing;

  if (!changed) return false;

  doc.setIn(["notifiers"], rawConfig["notifiers"]);
  doc.setIn(["defaults"], rawConfig["defaults"]);
  doc.setIn(["notificationRouting"], rawConfig["notificationRouting"]);
  writeFileSync(options.configPath, doc.toString({ indent: 2 }));
  return true;
}
