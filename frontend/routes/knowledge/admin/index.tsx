/** `/knowledge/admin` has no page of its own: it opens the first section (levels). */
import { createFileRoute, redirect } from "@tanstack/react-router";
import { KNOWLEDGE_ADMIN_DEFAULT_SECTION } from "../../../lib/knowledge-admin-nav";

export const Route = createFileRoute("/knowledge/admin/")({
	beforeLoad: () => {
		throw redirect({ to: KNOWLEDGE_ADMIN_DEFAULT_SECTION, replace: true });
	},
});
