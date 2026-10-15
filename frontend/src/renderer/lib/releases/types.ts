import type { components } from "../../../api/schema";

type S = components["schemas"];

export type BoardResponse = S["ReleaseTrainBoardResponse"];
export type Board = S["ReleaseTrainBoard"];
export type BoardApp = S["ReleaseTrainBoardApp"];
export type Train = S["ReleaseTrainTrain"];
export type AppVersion = S["ReleaseTrainAppVersion"];
export type Hotfix = S["ReleaseTrainHotfix"];
export type Stage = S["ReleaseTrainStage"];
export type StageKey = S["ReleaseTrainStageKey"];
export type StageStatus = S["ReleaseTrainStageStatus"];
export type AlertHit = S["ReleaseTrainAlertHit"];
export type Badge = S["ReleaseTrainBadge"];
export type Severity = S["ReleaseTrainSeverity"];
export type RuleInfo = S["ReleaseTrainRuleInfo"];
export type RuleOutcome = S["ReleaseTrainRuleOutcome"];
export type SourceFreshness = S["ReleaseTrainSourceFreshness"];
export type ResourceStatus = S["ReleaseTrainResourceStatus"];
export type LLBGroup = S["ReleaseTrainLLBGroup"];

/** "2026-10-15": a civil date in the Board's timezone, never a Date. */
export type Day = string;
export type TrainKey = string;
export type AppId = string;
export type HitId = string;
