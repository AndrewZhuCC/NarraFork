/**
 * Legacy `/knowledge/personal/$personalEntryId` (KNOWLEDGE_UI_REDESIGN §4.2): redirect only.
 * Every personal version — linked or standalone — now lives on `/knowledge/mine/$id`.
 * Kept so links shared before the move keep working.
 */
import { createFileRoute, redirect } from "@tanstack/react-router";
import { knowledgePersonalEntryDestination } from "../../../lib/knowledge-review-nav";

export const Route = createFileRoute("/knowledge/personal/$personalEntryId")({
	beforeLoad: ({ params }) => {
		throw redirect({
			...knowledgePersonalEntryDestination(params.personalEntryId),
			replace: true,
		});
	},
});
