import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { parse as parseYaml } from "yaml";
import { ensureStartupNotifierDefaults } from "../../src/lib/startup-notifier-defaults.js";

describe("startup notifier defaults", () => {
  let tempDir: string;
  let configPath: string;

  beforeEach(() => {
    tempDir = mkdtempSync(join(tmpdir(), "ao-startup-notifiers-"));
    configPath = join(tempDir, "agent-orchestrator.yaml");
  });

  afterEach(() => {
    rmSync(tempDir, { recursive: true, force: true });
  });

  function readConfig(): {
    defaults?: { notifiers?: string[] };
    notifiers?: Record<
      string,
      { plugin?: string; backend?: string; dashboardUrl?: string; limit?: number }
    >;
    notificationRouting?: Record<string, string[]>;
  } {
    return parseYaml(readFileSync(configPath, "utf-8")) as {
      defaults?: { notifiers?: string[] };
      notifiers?: Record<
        string,
        { plugin?: string; backend?: string; dashboardUrl?: string; limit?: number }
      >;
      notificationRouting?: Record<string, string[]>;
    };
  }

  it("replaces legacy implicit Composio routes with dashboard all-priority and desktop urgent-only defaults", () => {
    writeFileSync(
      configPath,
      [
        "port: 3000",
        "defaults:",
        "  notifiers:",
        "    - composio",
        "    - desktop",
        "notifiers: {}",
        "notificationRouting:",
        "  urgent: [desktop, composio]",
        "  action: [desktop, composio]",
        "  warning: [composio]",
        "  info: [composio]",
        "projects: {}",
        "",
      ].join("\n"),
    );

    expect(
      ensureStartupNotifierDefaults({
        configPath,
        dashboardUrl: "http://localhost:3000",
        desktopMode: "enable",
      }),
    ).toBe(true);

    const parsed = readConfig();
    expect(parsed.defaults?.notifiers).toEqual([]);
    expect(parsed.notifiers?.["dashboard"]).toEqual({ plugin: "dashboard", limit: 50 });
    expect(parsed.notifiers?.["desktop"]).toMatchObject({
      plugin: "desktop",
      backend: "ao-app",
      dashboardUrl: "http://localhost:3000",
    });
    expect(parsed.notificationRouting).toEqual({
      urgent: ["desktop", "dashboard"],
      action: ["dashboard"],
      warning: ["dashboard"],
      info: ["dashboard"],
    });
    expect(readFileSync(configPath, "utf-8")).not.toContain("composio");
  });

  it("removes default desktop routing when startup desktop setup cannot complete", () => {
    writeFileSync(
      configPath,
      [
        "port: 3000",
        "defaults:",
        "  notifiers: []",
        "notifiers:",
        "  desktop:",
        "    plugin: desktop",
        "    backend: ao-app",
        "    dashboardUrl: http://localhost:3000",
        "  dashboard:",
        "    plugin: dashboard",
        "    limit: 50",
        "notificationRouting:",
        "  urgent: [desktop, dashboard]",
        "  action: [dashboard]",
        "  warning: [dashboard]",
        "  info: [dashboard]",
        "projects: {}",
        "",
      ].join("\n"),
    );

    expect(
      ensureStartupNotifierDefaults({
        configPath,
        dashboardUrl: "http://localhost:3000",
        desktopMode: "disable-default",
      }),
    ).toBe(true);

    const parsed = readConfig();
    expect(parsed.notifiers?.["desktop"]).toBeUndefined();
    expect(parsed.notificationRouting).toEqual({
      urgent: ["dashboard"],
      action: ["dashboard"],
      warning: ["dashboard"],
      info: ["dashboard"],
    });
  });

  it("preserves configured manual opt-in notifiers while removing only implicit manual defaults", () => {
    writeFileSync(
      configPath,
      [
        "port: 3000",
        "defaults:",
        "  notifiers:",
        "    - slack",
        "    - composio",
        "notifiers:",
        "  slack:",
        "    plugin: slack",
        "    webhookUrl: https://hooks.slack.com/services/T/B/C",
        "notificationRouting:",
        "  urgent: [slack, composio]",
        "projects: {}",
        "",
      ].join("\n"),
    );

    ensureStartupNotifierDefaults({
      configPath,
      dashboardUrl: "http://localhost:3000",
      desktopMode: "enable",
    });

    const parsed = readConfig();
    expect(parsed.defaults?.notifiers).toEqual(["slack"]);
    expect(parsed.notificationRouting?.urgent).toEqual(["slack", "desktop", "dashboard"]);
    expect(parsed.notifiers?.["slack"]?.plugin).toBe("slack");
    expect(readFileSync(configPath, "utf-8")).not.toContain("composio");
  });
});
