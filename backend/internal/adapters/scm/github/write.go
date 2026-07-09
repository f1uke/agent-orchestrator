package github

// This file implements the GitHub side of the provider-neutral review-thread
// write contract (internal/observe/scm.ReviewThreadWriter): replying to and
// resolving a pull-request review thread via GitHub's GraphQL mutations.

import (
	"context"
	"errors"
	"fmt"

	scmobserve "github.com/aoagents/agent-orchestrator/backend/internal/observe/scm"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Provider satisfies the review-thread write capability. The gitlab and
// composite providers declare the same assertion in production code, so this
// mirrors the codebase convention (no import cycle: observe/scm defines the
// interfaces and never imports the concrete adapters).
var _ scmobserve.ReviewThreadWriter = (*Provider)(nil)

const replyThreadMutation = `mutation($threadId:ID!,$body:String!){addPullRequestReviewThreadReply(input:{pullRequestReviewThreadId:$threadId,body:$body}){comment{id body url author{login __typename}}}}`
const resolveThreadMutation = `mutation($threadId:ID!){resolveReviewThread(input:{threadId:$threadId}){thread{id isResolved}}}`

// ReplyToThread posts body as a reply to the given review thread and returns
// the normalized comment GitHub created.
func (p *Provider) ReplyToThread(ctx context.Context, ref ports.SCMPRRef, threadID, body string) (ports.SCMReviewCommentObservation, error) {
	data, err := p.client.doGraphQL(ctx, replyThreadMutation, map[string]any{"threadId": threadID, "body": body})
	if err != nil {
		return ports.SCMReviewCommentObservation{}, classifyWriteErr(err)
	}
	reply, _ := data["addPullRequestReviewThreadReply"].(map[string]any)
	cn, _ := reply["comment"].(map[string]any)
	author, _ := cn["author"].(map[string]any)
	return ports.SCMReviewCommentObservation{
		ID:     str(cn["id"]),
		Author: str(author["login"]),
		Body:   str(cn["body"]),
		URL:    str(cn["url"]),
		IsBot:  isBotAuthor(author),
	}, nil
}

// ResolveThread marks the given review thread resolved.
func (p *Provider) ResolveThread(ctx context.Context, ref ports.SCMPRRef, threadID string) error {
	_, err := p.client.doGraphQL(ctx, resolveThreadMutation, map[string]any{"threadId": threadID})
	return classifyWriteErr(err)
}

// classifyWriteErr maps client transport errors onto the provider-neutral
// write sentinels. ErrNotFound is already ports.ErrSCMNotFound (client.go:30),
// so it passes through; auth failures become ports.ErrSCMForbidden so the
// service can render a distinct 403 instead of a generic 503.
func classifyWriteErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrAuthFailed) {
		return fmt.Errorf("%w: %w", ports.ErrSCMForbidden, err)
	}
	return err
}
