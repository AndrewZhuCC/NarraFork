import { Center, Loader } from "@mantine/core";

/**
 * Lightweight loading fallback shared by narrator routes, panels and graph docks.
 * Keep the legacy export name for callers, but do not paint a fake panel layout.
 * Only the message-list loading states retain message-shaped skeletons.
 */
export function NarratorPanelSkeleton() {
	return (
		<Center h="100%" style={{ overflow: "hidden" }}>
			<Loader size="sm" />
		</Center>
	);
}
