import { createFileRoute, Navigate } from "@tanstack/react-router";

export const Route = createFileRoute("/admin/beta-trial")({
	component: () => <Navigate to="/settings/providers" replace />,
});
