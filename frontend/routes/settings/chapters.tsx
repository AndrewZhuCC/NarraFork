import { createFileRoute, Navigate } from "@tanstack/react-router";

// The page was renamed when chapters were removed; old bookmarks land on its successor.
export const Route = createFileRoute("/settings/chapters")({
	component: () => <Navigate to="/settings/workspace" replace />,
});
