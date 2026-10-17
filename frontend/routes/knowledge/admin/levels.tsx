import { createFileRoute } from "@tanstack/react-router";
import { LevelsAdmin } from "../../../components/knowledge/admin/LevelsAdmin";

export const Route = createFileRoute("/knowledge/admin/levels")({
	component: LevelsAdmin,
});
