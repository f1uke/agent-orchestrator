import * as Dialog from "@radix-ui/react-dialog";
import { ArrowLeft, Check, Folder, FolderPlus, GitBranch, Loader2, X } from "lucide-react";
import { useState, type ReactNode } from "react";
import type { WorkspaceRepoScanItem } from "../../preload";
import { apiErrorMessage } from "../lib/api-client";
import { aoBridge } from "../lib/bridge";
import { cn } from "../lib/utils";
import { Button } from "./ui/button";
import { CreateProjectAgentSheet, type CreateProjectAgentSelection } from "./CreateProjectAgentSheet";

export type ImportProjectInput = {
	path: string;
	workerAgent?: string;
	orchestratorAgent?: string;
	asWorkspace?: boolean;
};

type ImportProjectDialogProps = {
	children: (state: { open: () => void; disabled: boolean; label: string }) => ReactNode;
	onImport: (input: ImportProjectInput) => Promise<void>;
};

type ImportMode = "workspace" | "project";
type Step = "type" | "workspace" | "project";

export function ImportProjectDialog({ children, onImport }: ImportProjectDialogProps) {
	const [open, setOpen] = useState(false);
	const [step, setStep] = useState<Step>("type");
	const [selectedPath, setSelectedPath] = useState<string | null>(null);
	const [repos, setRepos] = useState<WorkspaceRepoScanItem[]>([]);
	const [error, setError] = useState<string | null>(null);
	const [isChoosing, setIsChoosing] = useState(false);
	const [isImporting, setIsImporting] = useState(false);
	const [agentSheetOpen, setAgentSheetOpen] = useState(false);

	const reset = () => {
		setStep("type");
		setSelectedPath(null);
		setRepos([]);
		setError(null);
		setIsChoosing(false);
		setIsImporting(false);
		setAgentSheetOpen(false);
	};

	const close = () => {
		if (isChoosing || isImporting) return;
		setOpen(false);
		reset();
	};

	const chooseFolder = async (mode: ImportMode) => {
		setError(null);
		setIsChoosing(true);
		try {
			const path = await aoBridge.app.chooseDirectory();
			if (!path) return;
			setSelectedPath(path);
			if (mode === "workspace") {
				setRepos(await aoBridge.app.scanWorkspaceRepos(path));
			}
		} catch (err) {
			setError(err instanceof Error ? err.message : "Could not inspect folder");
		} finally {
			setIsChoosing(false);
		}
	};

	const importWorkspace = async () => {
		if (!selectedPath || repos.length === 0 || repos.some((repo) => !repo.valid)) return;
		setError(null);
		setIsImporting(true);
		try {
			await onImport({ path: selectedPath, asWorkspace: true });
			close();
		} catch (err) {
			setError(apiErrorMessage(err, "Could not import workspace"));
		} finally {
			setIsImporting(false);
		}
	};

	const importProject = async (selection: CreateProjectAgentSelection) => {
		if (!selectedPath) return;
		setError(null);
		setIsImporting(true);
		try {
			await onImport({ path: selectedPath, ...selection });
			close();
		} catch (err) {
			setError(apiErrorMessage(err, "Could not import project"));
		} finally {
			setIsImporting(false);
		}
	};

	const openDialog = () => {
		reset();
		setOpen(true);
	};

	return (
		<>
			{children({
				open: openDialog,
				disabled: isChoosing || isImporting,
				label: isChoosing ? "Opening..." : "New project",
			})}
			<Dialog.Root open={open} onOpenChange={(next) => (next ? setOpen(true) : close())}>
				<Dialog.Portal>
					<Dialog.Overlay className="fixed inset-0 z-50 bg-black/55 data-[state=open]:animate-overlay-in" />
					<Dialog.Content className="fixed left-1/2 top-1/2 z-50 w-[min(680px,calc(100vw-32px))] -translate-x-1/2 -translate-y-1/2 overflow-hidden rounded-lg border border-border bg-popover text-popover-foreground shadow-xl data-[state=open]:animate-modal-in">
						{step === "type" ? (
							<TypePicker onSelect={(mode) => setStep(mode)} />
						) : (
							<ImportFolderStep
								error={error}
								isChoosing={isChoosing}
								isImporting={isImporting}
								mode={step}
								onBack={() => {
									setError(null);
									setSelectedPath(null);
									setRepos([]);
									setStep("type");
								}}
								onCancel={close}
								onChoose={() => void chooseFolder(step)}
								onImportProject={() => setAgentSheetOpen(true)}
								onImportWorkspace={() => void importWorkspace()}
								repos={repos}
								selectedPath={selectedPath}
							/>
						)}
					</Dialog.Content>
				</Dialog.Portal>
			</Dialog.Root>
			<CreateProjectAgentSheet
				error={error}
				isCreating={isImporting}
				onOpenChange={setAgentSheetOpen}
				onSubmit={importProject}
				open={agentSheetOpen}
				path={selectedPath}
			/>
		</>
	);
}

function TypePicker({ onSelect }: { onSelect: (mode: ImportMode) => void }) {
	return (
		<div className="p-5">
			<Dialog.Title className="text-[16px] font-semibold text-foreground">Import to Agent Orchestrator</Dialog.Title>
			<Dialog.Description className="sr-only">
				Choose whether to import a workspace folder or a single project repository.
			</Dialog.Description>
			<div className="mt-5 grid gap-3 sm:grid-cols-2">
				<TypeCard
					description="Several Git repos that live under one parent folder"
					icon={<WorkspacePreview />}
					onClick={() => onSelect("workspace")}
					tag="• Multiple repositories"
					title="Workspace"
				/>
				<TypeCard
					description="A single Git repository"
					icon={<ProjectPreview />}
					onClick={() => onSelect("project")}
					tag="• One repository"
					title="Project"
				/>
			</div>
		</div>
	);
}

function TypeCard({
	description,
	icon,
	onClick,
	tag,
	title,
}: {
	description: string;
	icon: ReactNode;
	onClick: () => void;
	tag: string;
	title: string;
}) {
	return (
		<button
			type="button"
			className="flex min-h-[220px] flex-col rounded-lg border border-border bg-surface p-4 text-left transition hover:border-border-strong hover:bg-raised focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
			onClick={onClick}
		>
			<div className="flex-1">{icon}</div>
			<div className="mt-4">
				<div className="text-[15px] font-semibold text-foreground">{title}</div>
				<p className="mt-1 min-h-10 text-[12px] leading-5 text-muted-foreground">{description}</p>
				<div className="mt-3 text-[11px] font-medium uppercase tracking-[0.08em] text-passive">{tag}</div>
			</div>
		</button>
	);
}

function WorkspacePreview() {
	return (
		<div className="rounded-md border border-border bg-background p-3">
			<div className="flex items-center gap-2 text-[12px] font-medium text-foreground">
				<Folder className="size-4 text-accent" aria-hidden="true" />
				workspace
			</div>
			<div className="mt-3 space-y-2 pl-3">
				{["api", "web", "workers"].map((name) => (
					<div key={name} className="flex items-center gap-2 rounded border border-border bg-surface px-2 py-1.5">
						<GitBranch className="size-3.5 text-passive" aria-hidden="true" />
						<span className="text-[11px] text-muted-foreground">{name}</span>
					</div>
				))}
			</div>
		</div>
	);
}

function ProjectPreview() {
	return (
		<div className="rounded-md border border-border bg-background p-3">
			<div className="inline-flex items-center gap-2 rounded border border-border bg-surface px-2.5 py-1.5 text-[12px] text-foreground">
				<GitBranch className="size-3.5 text-accent" aria-hidden="true" />
				web-app · main
			</div>
		</div>
	);
}

function ImportFolderStep({
	error,
	isChoosing,
	isImporting,
	mode,
	onBack,
	onCancel,
	onChoose,
	onImportProject,
	onImportWorkspace,
	repos,
	selectedPath,
}: {
	error: string | null;
	isChoosing: boolean;
	isImporting: boolean;
	mode: "workspace" | "project";
	onBack: () => void;
	onCancel: () => void;
	onChoose: () => void;
	onImportProject: () => void;
	onImportWorkspace: () => void;
	repos: WorkspaceRepoScanItem[];
	selectedPath: string | null;
}) {
	const isWorkspace = mode === "workspace";
	const failedCount = repos.filter((repo) => !repo.valid).length;
	const canImportWorkspace =
		isWorkspace && selectedPath !== null && repos.length > 0 && failedCount === 0 && !isImporting;
	const canImportProject = !isWorkspace && selectedPath !== null && !isImporting;

	return (
		<div className="flex max-h-[min(720px,calc(100vh-32px))] flex-col">
			<div className="flex items-start gap-3 border-b border-border px-5 py-4">
				<button
					type="button"
					aria-label="Back"
					className="grid size-7 shrink-0 place-items-center rounded-md text-muted-foreground transition hover:bg-surface hover:text-foreground"
					onClick={onBack}
					disabled={isChoosing || isImporting}
				>
					<ArrowLeft className="size-4" aria-hidden="true" />
				</button>
				<div className="min-w-0">
					<Dialog.Title className="text-[15px] font-semibold text-foreground">
						{isWorkspace ? "Import workspace" : "Import project"}
					</Dialog.Title>
					<Dialog.Description className="mt-1 text-[12px] text-muted-foreground">
						{isWorkspace
							? "Pick a folder that contains your Git repositories."
							: "Pick a folder that contains your Git repository."}
					</Dialog.Description>
				</div>
			</div>
			<div className="min-h-0 flex-1 overflow-y-auto px-5 py-4">
				{selectedPath ? (
					<SelectedFolder
						mode={mode}
						repos={repos}
						selectedPath={selectedPath}
						failedCount={failedCount}
						onChoose={onChoose}
					/>
				) : (
					<button
						type="button"
						className="flex min-h-[260px] w-full flex-col items-center justify-center rounded-lg border border-dashed border-border-strong bg-background px-6 text-center transition hover:bg-surface focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
						onClick={onChoose}
						disabled={isChoosing}
					>
						{isChoosing ? (
							<Loader2 className="size-8 animate-spin text-accent" aria-hidden="true" />
						) : (
							<FolderPlus className="size-9 text-accent" aria-hidden="true" />
						)}
						<div className="mt-4 text-[15px] font-semibold text-foreground">
							{isWorkspace ? "Choose a folder" : "Choose a project folder"}
						</div>
						<p className="mt-1 max-w-[360px] text-[12px] leading-5 text-muted-foreground">
							Opens your system file picker — pick the folder that holds your repos
						</p>
					</button>
				)}
				{error && (
					<div className="mt-3 rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-[12px] leading-5 text-destructive">
						{error}
					</div>
				)}
			</div>
			<div className="flex items-center justify-between gap-3 border-t border-border px-5 py-4">
				<div className="min-w-0 text-[12px] text-muted-foreground">
					{isWorkspace
						? selectedPath && failedCount > 0
							? `Resolve ${failedCount} failed ${failedCount === 1 ? "repository" : "repositories"} to continue`
							: repos.length === 0
								? "No repositories to import"
								: `${repos.length} repositories ready`
						: ""}
				</div>
				<div className="flex shrink-0 items-center gap-2">
					<Button type="button" variant="ghost" onClick={onCancel} disabled={isChoosing || isImporting}>
						Cancel
					</Button>
					<Button
						type="button"
						variant="primary"
						disabled={isWorkspace ? !canImportWorkspace : !canImportProject}
						onClick={isWorkspace ? onImportWorkspace : onImportProject}
					>
						{isImporting && <Loader2 className="size-4 animate-spin" aria-hidden="true" />}
						{isWorkspace ? "Import workspace" : "Import project"}
					</Button>
				</div>
			</div>
		</div>
	);
}

function SelectedFolder({
	failedCount,
	mode,
	onChoose,
	repos,
	selectedPath,
}: {
	failedCount: number;
	mode: "workspace" | "project";
	onChoose: () => void;
	repos: WorkspaceRepoScanItem[];
	selectedPath: string;
}) {
	return (
		<div className="space-y-3">
			<div className="flex items-center justify-between gap-3 rounded-md border border-border bg-background px-3 py-2.5">
				<div className="min-w-0">
					<div className="text-[11px] font-medium uppercase tracking-[0.08em] text-passive">Selected folder</div>
					<div className="mt-1 break-all font-mono text-[12px] text-foreground">{selectedPath}</div>
				</div>
				<Button type="button" variant="secondary" size="sm" onClick={onChoose}>
					Change
				</Button>
			</div>
			{mode === "workspace" && failedCount > 0 && (
				<div className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-[11px] font-semibold uppercase tracking-[0.08em] text-destructive">
					VALIDATION FAILED · WORKSPACE NOT REGISTERED
				</div>
			)}
			{mode === "workspace" && (
				<div className="overflow-hidden rounded-md border border-border">
					{repos.length === 0 ? (
						<div className="px-3 py-4 text-[12px] text-muted-foreground">No repositories found in this folder.</div>
					) : (
						repos.map((repo) => <RepoRow key={repo.path} repo={repo} />)
					)}
				</div>
			)}
		</div>
	);
}

function RepoRow({ repo }: { repo: WorkspaceRepoScanItem }) {
	const host = repo.remote ? remoteHost(repo.remote) : "No remote configured";
	return (
		<div className="flex items-center gap-3 border-b border-border bg-surface px-3 py-2.5 last:border-b-0">
			<div
				className={cn(
					"grid size-6 shrink-0 place-items-center rounded-full",
					repo.valid ? "bg-success/15 text-success" : "bg-destructive/15 text-destructive",
				)}
			>
				{repo.valid ? <Check className="size-3.5" aria-hidden="true" /> : <X className="size-3.5" aria-hidden="true" />}
			</div>
			<div className="min-w-0 flex-1">
				<div className="truncate text-[13px] font-medium text-foreground">{repo.name}</div>
				<div className="mt-0.5 truncate font-mono text-[11px] text-passive">{repo.path}</div>
			</div>
			<div className={cn("shrink-0 text-right text-[12px]", repo.valid ? "text-muted-foreground" : "text-destructive")}>
				{repo.valid ? `${repo.branch ?? "main"} ${host}` : repo.error}
			</div>
		</div>
	);
}

function remoteHost(remote: string): string {
	const sshMatch = remote.match(/^git@([^:]+):(.+?)(?:\.git)?$/);
	if (sshMatch) return `${sshMatch[1]}/${sshMatch[2].replace(/\.git$/, "")}`;
	try {
		const url = new URL(remote);
		return `${url.host}${url.pathname.replace(/\.git$/, "")}`;
	} catch {
		return remote.replace(/^https?:\/\//, "").replace(/\.git$/, "");
	}
}
