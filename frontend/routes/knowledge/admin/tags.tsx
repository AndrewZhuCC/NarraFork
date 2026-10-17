import { createFileRoute } from "@tanstack/react-router";
import { TagsAdmin } from "../../../components/knowledge/admin/TagsAdmin";

export const Route = createFileRoute("/knowledge/admin/tags")({
	component: TagsAdmin,
});
