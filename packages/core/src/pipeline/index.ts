/**
 * Pipeline subsystem — public re-exports.
 *
 * Consumers import from `@aoagents/ao-core` or, for granular bundles,
 * `@aoagents/ao-core/pipeline` (when an export entry is added).
 */

export * from "./types.js";
export type { PipelineEvent, PipelineEffect, ReducerResult } from "./events.js";
export { reduce } from "./reducer.js";
export {
  createPipelineStore,
  type PipelineStore,
  type PersistedStageRun,
} from "./store.js";
export {
  pipelineLayout,
  runFilePath,
  stageFilePath,
  artifactsDirForRun,
  artifactsFilePath,
  loopFilePath,
  type PipelineLayout,
} from "./paths.js";
