import { createFileRoute } from "@tanstack/react-router";
import { UsersAdmin } from "../../../components/knowledge/admin/UsersAdmin";

export const Route = createFileRoute("/knowledge/admin/users")({
	component: UsersAdmin,
});
