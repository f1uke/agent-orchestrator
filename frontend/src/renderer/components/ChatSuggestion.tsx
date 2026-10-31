import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { GitBranchPlus, Sparkles } from "lucide-react";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import type { SessionSuggestion, SkillSuggestion } from "../lib/chat-suggest";
import { cn } from "../lib/utils";
import { NewTaskDialog } from "./NewTaskDialog";

type Live = { live: boolean };

function Card({
	icon,
	title,
	detail,
	kind,
	children,
}: {
	icon: React.ReactNode;
	title: string;
	detail: string;
	kind: "session" | "skill";
	children: React.ReactNode;
}) {
	return (
		<div className="chat-suggest" data-suggest={kind}>
			<span aria-hidden="true" className="chat-suggest__icon">
				{icon}
			</span>
			<div className="chat-suggest__text">
				<div className="chat-suggest__title">{title}</div>
				{detail ? <div className="chat-suggest__detail">{detail}</div> : null}
			</div>
			<div className="chat-suggest__actions">{children}</div>
		</div>
	);
}

function Action({
	live,
	primary,
	onClick,
	children,
}: Live & { primary?: boolean; onClick: () => void; children: React.ReactNode }) {
	return (
		<button
			aria-disabled={live ? undefined : true}
			className={cn("chat-suggest__button", primary && "chat-suggest__button--primary")}
			onClick={live ? onClick : undefined}
			type="button"
		>
			{children}
		</button>
	);
}

export function SessionSuggestionCard({
	suggestion,
	live,
	onOpen,
	onPrompt,
}: Live & {
	suggestion: SessionSuggestion;
	onOpen: (suggestion: SessionSuggestion) => void;
	onPrompt: (text: string) => void;
}) {
	return (
		<Card
			detail={suggestion.title}
			icon={<GitBranchPlus className="chat-icon" />}
			kind="session"
			title="Start a new session"
		>
			<Action live={live} onClick={() => onOpen(suggestion)} primary>
				Open new session…
			</Action>
			<Action live={live} onClick={() => onPrompt(`Do it here: ${suggestion.title}`)}>
				Do it here
			</Action>
		</Card>
	);
}

export function SkillSuggestionCard({
	suggestion,
	live,
	onPrompt,
}: Live & { suggestion: SkillSuggestion; onPrompt: (text: string) => void }) {
	return (
		<Card
			detail={suggestion.reason}
			icon={<Sparkles className="chat-icon" />}
			kind="skill"
			title={`Skill: /${suggestion.name}`}
		>
			<Action live={live} onClick={() => onPrompt(`/${suggestion.name} `)}>
				Use /{suggestion.name}
			</Action>
		</Card>
	);
}

/**
 * The New task form, filled in from a suggestion. Creating anything stays the human's click on
 * Start or Add to TODO inside the dialog; this only opens it and follows the new session.
 */
export function SuggestedSessionDialog({
	suggestion,
	projectId,
	onClose,
}: {
	suggestion: SessionSuggestion;
	projectId: string;
	onClose: () => void;
}) {
	const navigate = useNavigate();
	const queryClient = useQueryClient();
	const created = async (sessionId: string) => {
		await queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
		void navigate({ to: "/projects/$projectId/sessions/$sessionId", params: { projectId, sessionId } });
	};
	return (
		<NewTaskDialog
			initialPrompt={suggestion.prompt}
			initialTitle={suggestion.title}
			onCreated={(sessionId) => void created(sessionId)}
			onOpenChange={(open) => {
				if (!open) onClose();
			}}
			open
			projectId={projectId}
		/>
	);
}
