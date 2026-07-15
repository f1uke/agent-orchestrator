import { createElement, Fragment, type ReactNode } from "react";
import { ArrowUpRight, Check, Paperclip } from "lucide-react";
import type { AdfNode } from "../hooks/useSessionJiraContext";
import { JiraMediaNode } from "./JiraMedia";

/**
 * Renders a Jira description (normalized ADF node tree) as safe React elements.
 *
 * The backend already normalized ADF into a small whitelisted tree, so this maps
 * each node kind onto a real element — never `dangerouslySetInnerHTML`, so there
 * is no HTML-injection surface. It renders the body FAITHFULLY (bold paragraphs
 * stay bold paragraphs; the author's own headings/links/AC appear where they
 * wrote them) — it does not parse the description into cards.
 */
export function JiraAdf({ nodes }: { nodes?: AdfNode[] }) {
	if (!nodes || nodes.length === 0) return null;
	return <div className="jira-adf">{renderNodes(nodes)}</div>;
}

function renderNodes(nodes?: AdfNode[]): ReactNode {
	if (!nodes) return null;
	return nodes.map((node, i) => <AdfBlock key={i} node={node} />);
}

function AdfBlock({ node }: { node: AdfNode }): ReactNode {
	const children = () => renderNodes(node.content);
	switch (node.type) {
		case "text":
			return renderText(node);
		case "hardBreak":
			return <br />;
		case "paragraph":
			return <p className="jira-adf__p">{children()}</p>;
		case "heading": {
			const level = Math.min(Math.max(node.attrs?.level ?? 3, 1), 6);
			return createElement(`h${level}`, { className: "jira-adf__h" }, children());
		}
		case "blockquote":
			return <blockquote className="jira-adf__quote">{children()}</blockquote>;
		case "bulletList":
			return <ul className="jira-adf__ul">{children()}</ul>;
		case "orderedList":
			return <ol className="jira-adf__ol">{children()}</ol>;
		case "listItem":
			return <li className="jira-adf__li">{children()}</li>;
		case "codeBlock":
			return (
				<pre className="jira-adf__pre">
					<code>{plainText(node)}</code>
				</pre>
			);
		case "panel":
			return (
				<div className="jira-adf__panel" data-panel={node.attrs?.panelType || "info"}>
					{children()}
				</div>
			);
		case "rule":
			return <hr className="jira-adf__rule" />;
		case "table":
			return (
				<div className="jira-adf__table-wrap">
					<table className="jira-adf__table">
						<tbody>{children()}</tbody>
					</table>
				</div>
			);
		case "tableRow":
			return <tr>{children()}</tr>;
		case "tableCell":
			return <td className="jira-adf__td">{children()}</td>;
		case "tableHeader":
			return <th className="jira-adf__th">{children()}</th>;
		case "mediaSingle":
		case "mediaGroup":
			return <div className="jira-adf__media-wrap">{children()}</div>;
		case "media":
		case "mediaInline":
			// Inline preview when a matching attachment resolves (Summary tab),
			// else the filename chip (via AttachmentChip). See JiraMedia.
			return <JiraMediaNode node={node} />;
		case "inlineCard":
		case "blockCard":
			return <SmartLink url={node.attrs?.url} />;
		case "taskList":
			return <ul className="jira-adf__tasklist">{children()}</ul>;
		case "taskItem":
			return (
				<li className="jira-adf__taskitem">
					<span
						className="jira-adf__checkbox"
						data-checked={node.attrs?.state === "DONE" ? "true" : "false"}
						aria-hidden="true"
					>
						{node.attrs?.state === "DONE" ? <Check className="jira-adf__check" /> : null}
					</span>
					<span>{children()}</span>
				</li>
			);
		case "mention":
			return <span className="jira-adf__mention">{node.attrs?.text || "@mention"}</span>;
		case "emoji":
			return <span>{node.attrs?.text || ""}</span>;
		case "date":
			return <span className="jira-adf__date">{formatAdfDate(node.attrs?.text)}</span>;
		case "status":
			return (
				<span className="jira-adf__status" data-color={node.attrs?.color || "neutral"}>
					{node.attrs?.text || ""}
				</span>
			);
		default:
			// Unknown wrapper: surface its children so nothing is silently lost.
			return <Fragment>{children()}</Fragment>;
	}
}

/** A text node with its inline marks folded into nested elements. */
function renderText(node: AdfNode): ReactNode {
	let el: ReactNode = node.text ?? "";
	for (const mark of node.marks ?? []) {
		switch (mark.type) {
			case "code":
				el = <code className="jira-adf__code">{el}</code>;
				break;
			case "strong":
				el = <strong>{el}</strong>;
				break;
			case "em":
				el = <em>{el}</em>;
				break;
			case "strike":
				el = <s>{el}</s>;
				break;
			case "underline":
				el = <u>{el}</u>;
				break;
			case "link":
				el = (
					<a className="jira-adf__link" href={mark.href} target="_blank" rel="noopener noreferrer">
						{el}
					</a>
				);
				break;
			default:
				break;
		}
	}
	return <>{el}</>;
}

/** The gray attachment chip (Paperclip + filename) — the fallback for a media node
 * with no matching attachment, and the render outside a JiraMediaProvider. */
export function AttachmentChip({ filename }: { filename?: string }) {
	return (
		<span className="jira-adf__att">
			<Paperclip className="jira-adf__att-icon" aria-hidden="true" />
			{filename || "attachment"}
		</span>
	);
}

function SmartLink({ url }: { url?: string }) {
	if (!url) return null;
	return (
		<a className="jira-adf__link jira-adf__smartlink" href={url} target="_blank" rel="noopener noreferrer">
			{linkLabel(url)}
			<ArrowUpRight className="jira-adf__ext-icon" aria-hidden="true" />
		</a>
	);
}

/** Concatenate all descendant text (for code blocks, which carry text nodes). */
function plainText(node: AdfNode): string {
	if (node.type === "text") return node.text ?? "";
	return (node.content ?? []).map(plainText).join("");
}

/** A friendly label for a smart link: its hostname without a leading "www.". */
function linkLabel(url: string): string {
	try {
		return new URL(url).hostname.replace(/^www\./, "");
	} catch {
		return url;
	}
}

/** ADF date attrs carry an epoch-ms timestamp string; format it, else pass through. */
function formatAdfDate(timestamp?: string): string {
	if (!timestamp) return "";
	const ms = Number(timestamp);
	if (!Number.isFinite(ms)) return timestamp;
	const d = new Date(ms);
	if (Number.isNaN(d.getTime())) return timestamp;
	return d.toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" });
}
