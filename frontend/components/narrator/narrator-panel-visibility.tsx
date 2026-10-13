import type { DockviewPanelApi } from "dockview-react";
import { createContext, type ReactNode, useContext, useLayoutEffect, useState } from "react";

const NarratorPanelVisibleContext = createContext(true);

export const NarratorPanelVisibilityProvider = NarratorPanelVisibleContext.Provider;

export function useNarratorPanelVisible(): boolean {
	return useContext(NarratorPanelVisibleContext);
}

export function DockviewNarratorPanelVisibilityProvider({
	api,
	children,
}: {
	api: DockviewPanelApi;
	children: ReactNode;
}) {
	// Every split group's selected tab is visible; only one group owns focus.
	// Layout and document loading must not wait for api.isActive.
	const [visible, setVisible] = useState(api.isVisible);
	useLayoutEffect(() => {
		setVisible(api.isVisible);
		const subscription = api.onDidVisibilityChange(() => setVisible(api.isVisible));
		return () => subscription.dispose();
	}, [api]);
	return (
		<NarratorPanelVisibilityProvider value={visible}>{children}</NarratorPanelVisibilityProvider>
	);
}
