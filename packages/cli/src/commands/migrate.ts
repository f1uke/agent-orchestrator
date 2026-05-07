import type { Command } from "commander";
import chalk from "chalk";
import { writeFileSync } from "node:fs";

import {
  formatBytes,
  getAoBaseDir,
  getGlobalConfigPath,
  inventoryV3,
  planV3,
  type V3Plan,
} from "@aoagents/ao-core";
import { homedir } from "node:os";
import { join } from "node:path";

import { getCliVersion } from "../options/version.js";

interface MigrateOptions {
  dryRun?: boolean;
  json?: boolean;
  output?: string;
  execute?: boolean;
  rollback?: boolean;
}

const FEEDBACK_ISSUE_URL =
  "https://github.com/ComposioHQ/agent-orchestrator/issues/new?title=ao+migrate+dry-run+output";

const GATED_MESSAGE = `
${chalk.bold.red("ao migrate execution is gated in v0.6.0.")}

This release ships ${chalk.cyan("--dry-run")} only so we can review real-world plan output
before the migration touches any disk on user machines.

Please share dry-run output:
  1. ${chalk.dim("ao migrate --json --output ~/ao-migrate-plan.json")}
  2. Open an issue with that file attached: ${FEEDBACK_ISSUE_URL}

Execution unlocks in v0.6.1.
`;

export function registerMigrate(program: Command): void {
  program
    .command("migrate")
    .description(
      "Inventory + plan storage migration to V3 (one-format identity, one prefix allocator, observability inside projects). Dry-run only in v0.6.0.",
    )
    .option("--dry-run", "Inventory + plan only (default)", true)
    .option("--json", "Emit V3Plan as JSON to stdout")
    .option("--output <path>", "Write the V3Plan record to a file instead of stdout")
    .option("--execute", "[gated] Apply the plan to disk")
    .option("--rollback", "[gated] Reverse a previous migration")
    .action(async (opts: MigrateOptions) => {
      if (opts.execute || opts.rollback) {
        process.stderr.write(GATED_MESSAGE + "\n");
        process.exit(1);
      }

      const aoBaseDir = getAoBaseDir();
      const globalConfigPath = getGlobalConfigPath();
      const legacyWorktreeRoot = join(homedir(), ".worktrees");

      const inventory = await inventoryV3({
        aoBaseDir,
        globalConfigPath,
        legacyWorktreeRoot,
      });

      const plan = planV3(inventory, getCliVersion());

      if (opts.json) {
        const json = JSON.stringify(plan, null, 2);
        if (opts.output) {
          writeFileSync(opts.output, json + "\n", "utf-8");
          process.stdout.write(`Plan written to ${opts.output}\n`);
        } else {
          process.stdout.write(json + "\n");
        }
        return;
      }

      printHumanPlan(plan);

      if (opts.output) {
        writeFileSync(opts.output, JSON.stringify(plan, null, 2) + "\n", "utf-8");
        process.stdout.write(
          `\n${chalk.dim("Full JSON record written to:")} ${opts.output}\n`,
        );
      }
    });
}

function printHumanPlan(plan: V3Plan): void {
  const out = process.stdout;

  out.write(`\n${chalk.bold("ao migrate v3")} ${chalk.dim(`(dry-run · ${plan.aoVersion})`)}\n`);
  out.write(`${chalk.dim("Scanned:")}  ${plan.inventory.aoBaseDir}\n`);
  out.write(`${chalk.dim("At:")}       ${plan.generatedAt}\n\n`);

  // Inventory summary
  out.write(`${chalk.bold("Inventory")}\n`);
  out.write(`  Projects:                  ${plan.inventory.projects.length}\n`);
  const v1 = plan.inventory.projects.filter((p) => p.layout === "v1-bare").length;
  const v2 = plan.inventory.projects.filter((p) => p.layout === "v2-hashed").length;
  out.write(`    V1 bare-basename:        ${v1}\n`);
  out.write(`    V2 hashed:               ${v2}\n`);
  out.write(`  Sessions:                  ${plan.inventory.totals.sessions}\n`);
  out.write(`  Worktrees:                 ${plan.inventory.totals.worktrees}\n`);
  out.write(
    `  Observability dirs:        ${plan.inventory.observability.rootLevelDirCount}` +
      ` (${formatBytes(plan.inventory.observability.bytes)})\n`,
  );
  out.write(`  Stranded worktrees:        ${plan.inventory.strandedWorktrees.length}\n`);
  out.write(`  Bare hash dirs:            ${plan.inventory.bareHashDirs.length}\n`);
  out.write(`  .migrated dirs:            ${plan.inventory.migratedDirs.length}\n`);
  out.write(`  Live tmux sessions:        ${plan.inventory.liveTmuxSessions.length}\n`);
  out.write(`  Same-repo duplicates:      ${plan.inventory.duplicateRepos.length}\n`);
  out.write(`  V1 hash dirs (legacy):     ${plan.inventory.v1HashDirs.length}\n`);
  out.write(`  Total bytes:               ${formatBytes(plan.inventory.totals.bytes)}\n\n`);

  // Issues by project
  const projectsWithIssues = plan.inventory.projects.filter((p) => p.issues.length > 0);
  if (projectsWithIssues.length > 0) {
    out.write(`${chalk.bold("Per-project issues")}\n`);
    for (const p of projectsWithIssues) {
      out.write(`  ${chalk.cyan(p.projectId)} ${chalk.dim(`[${p.layout}]`)}\n`);
      for (const issue of p.issues) {
        out.write(`    ${chalk.yellow("•")} ${issue.detail}\n`);
      }
    }
    out.write("\n");
  }

  // Global config issues
  if (plan.inventory.globalConfigIssues.length > 0) {
    out.write(`${chalk.bold("Global config issues")}\n`);
    for (const issue of plan.inventory.globalConfigIssues) {
      out.write(`  ${chalk.yellow("•")} ${issue.detail}\n`);
    }
    out.write("\n");
  }

  // Plan steps
  out.write(`${chalk.bold("Plan")} ${chalk.dim("(would execute these in order if unlocked)")}\n`);
  if (plan.steps.length === 0) {
    out.write(
      `  ${chalk.green("Nothing to do — disk is already V3-compliant.")}\n\n`,
    );
  } else {
    for (const step of plan.steps) {
      out.write(`  ${chalk.bold(step.order + ".")} ${step.title} ${chalk.dim(`(${step.count})`)}\n`);
      out.write(`     ${chalk.dim(step.description)}\n`);
      if (step.details.length > 0 && step.details.length <= 8) {
        for (const detail of step.details) {
          out.write(`     - ${detail}\n`);
        }
      } else if (step.details.length > 8) {
        for (const detail of step.details.slice(0, 6)) {
          out.write(`     - ${detail}\n`);
        }
        out.write(`     ${chalk.dim(`… ${step.details.length - 6} more`)}\n`);
      }
    }
    out.write("\n");
  }

  // Totals
  out.write(`${chalk.bold("Totals")}\n`);
  out.write(`  Projects to re-key:        ${plan.totals.projectsToRekey}\n`);
  out.write(`  Sessions to rewrite:       ${plan.totals.sessionsToRewrite}\n`);
  out.write(`  Tmux renames:              ${plan.totals.tmuxRenames}\n`);
  out.write(`  Worktree adoptions:        ${plan.totals.worktreeAdoptions}\n`);
  out.write(`  Orchestrators to normalize: ${plan.totals.orchestratorsToNormalize}\n`);
  out.write(`  Observability dirs to GC:  ${plan.totals.observabilityDirsToCollapse}\n`);
  out.write(`  Bare hash dirs to remove:  ${plan.totals.bareHashDirsToRemove}\n`);
  out.write(`  storageKey fields to strip: ${plan.totals.storageKeyFieldsToStrip}\n`);
  out.write(
    `  Estimated bytes freed:     ~${formatBytes(plan.totals.estimatedBytesFreed)}\n\n`,
  );

  // Warnings
  if (plan.warnings.length > 0) {
    out.write(`${chalk.bold.yellow("Warnings")}\n`);
    for (const w of plan.warnings) {
      out.write(`  ${chalk.yellow("⚠")} ${w}\n`);
    }
    out.write("\n");
  }

  // Footer
  out.write(`${chalk.dim("─".repeat(60))}\n`);
  out.write(`${chalk.bold("Execution is gated in v0.6.0.")}\n`);
  out.write(
    `Share this plan at: ${chalk.cyan(FEEDBACK_ISSUE_URL)}\n` +
      `${chalk.dim("Execution unlocks in v0.6.1.")}\n\n`,
  );
}
