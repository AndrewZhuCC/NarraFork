import { Menu, Portal } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconFolder, IconWindowMaximize } from "@tabler/icons-react";
import { DockviewDefaultTab, type IDockviewPanelHeaderProps } from "dockview-react";
import { type FunctionComponent, useState } from "react";
import { useTranslation } from "react-i18next";
import { usePlatform } from "../../hooks/usePlatform";
import { useUserPreferences } from "../../hooks/useUserPreferences";
import { api } from "../../lib/api";
import { canRevealFromBrowser } from "../../lib/local-origin";
import { fileRevealDirectory } from "../../lib/reveal-directory";
import { usePluginUiSurface } from "../plugins/PluginUiSurfaceContext";
import {
	normalizePanelWindowDescriptor,
	openPanelInWindow,
	pluginPanelWindowHostContext,
} from "../window/panel-window";
import { closeSurfaceTabs, getTabCloseTargets, type TabCloseAction } from "./tab-close";

const actions: TabCloseAction[] = ["all", "others", "left", "right", "auxiliary"];
const wrappers = new WeakMap<
	FunctionComponent<IDockviewPanelHeaderProps>,
	FunctionComponent<IDockviewPanelHeaderProps>
>();

/** Reveal runs on the server desktop, never on a remote executor or browser. */
export function fileTabRevealDirectory(
	params: IDockviewPanelHeaderProps["params"],
	platform: string,
	localOrigin: boolean,
): string | null {
	if (params?.panelType !== "file") return null;
	return fileRevealDirectory(params.filePath, params.deviceId, platform, localOrigin);
}

/** Cache by renderer, not registry identity, so parent rerenders never remount tabs. */
export function withSurfaceTabMenu(Tab: FunctionComponent<IDockviewPanelHeaderProps>) {
	const existing = wrappers.get(Tab);
	if (existing) return existing;
	function SurfaceTab(props: IDockviewPanelHeaderProps) {
		const { t } = useTranslation("common");
		const platform = usePlatform();
		const { data: userPrefs } = useUserPreferences();
		const revealDirectory = fileTabRevealDirectory(
			props.params,
			platform,
			canRevealFromBrowser(userPrefs?.treatAsLocalAccess),
		);
		// Every panel whose params carry its identity can live in its own window
		// (mock and params-less panels normalize to null). Copy semantics: the
		// source panel stays put.
		const surface = usePluginUiSurface();
		let windowDescriptor = normalizePanelWindowDescriptor(props.params);
		if (windowDescriptor?.panelType === "plugin") {
			// Copy the resolved panel session, not just the surface's generic host.
			const hostContext = surface?.resolveSessionContext(windowDescriptor);
			if (hostContext) {
				const binding =
					windowDescriptor.binding.kind === "focus-current-narrator" && hostContext.narratorId
						? { kind: "focus-current-narrator" as const, narratorId: hostContext.narratorId }
						: windowDescriptor.binding;
				windowDescriptor = normalizePanelWindowDescriptor({
					...windowDescriptor,
					binding,
					hostContext,
				});
			} else if (!pluginPanelWindowHostContext(windowDescriptor)) {
				windowDescriptor = null;
			}
		}
		const canOpenInWindow = windowDescriptor !== null;
		const [position, setPosition] = useState<{ x: number; y: number } | null>(null);
		return (
			// The wrapper leaves ordinary click, middle-click and drag handling to the tab.
			// biome-ignore lint/a11y/noStaticElementInteractions: context menu supplements native tab interactions
			<div
				style={{ height: "100%" }}
				onContextMenu={(event) => {
					event.preventDefault();
					event.stopPropagation();
					setPosition({ x: event.clientX, y: event.clientY });
				}}
			>
				<Tab {...props} />
				{position && (
					<Menu
						opened={position !== null}
						onChange={(opened) => {
							if (!opened) setPosition(null);
						}}
						position="bottom-start"
						withinPortal
					>
						{/* Escape transformed graph ancestors: client coordinates are viewport-relative. */}
						<Portal>
							<Menu.Target>
								<span
									style={{
										position: "fixed",
										left: position?.x ?? 0,
										top: position?.y ?? 0,
										width: 0,
										height: 0,
										pointerEvents: "none",
									}}
								/>
							</Menu.Target>
						</Portal>
						<Menu.Dropdown
							onMouseDown={(event) => event.stopPropagation()}
							onClick={(event) => event.stopPropagation()}
						>
							{canOpenInWindow && (
								<>
									<Menu.Item
										leftSection={<IconWindowMaximize size={14} />}
										onClick={() => {
											setPosition(null);
											openPanelInWindow(windowDescriptor);
										}}
									>
										{t("dockTabs.openInWindow")}
									</Menu.Item>
									<Menu.Divider />
								</>
							)}
							{revealDirectory && (
								<>
									<Menu.Item
										leftSection={<IconFolder size={14} />}
										onClick={() => {
											setPosition(null);
											void api.fsReveal(revealDirectory).catch((error: unknown) => {
												notifications.show({
													color: "red",
													title: t("dockTabs.revealFailed"),
													message: error instanceof Error ? error.message : t("unknownError"),
												});
											});
										}}
									>
										{t("dockTabs.revealInExplorer")}
									</Menu.Item>
									<Menu.Divider />
								</>
							)}
							{actions.map((action) => (
								<Menu.Item
									key={action}
									disabled={
										getTabCloseTargets(props.containerApi, props.api.id, action).length === 0
									}
									onClick={() => {
										setPosition(null);
										closeSurfaceTabs(props.containerApi, props.api.id, action);
									}}
								>
									{t(`dockTabs.${action}`)}
								</Menu.Item>
							))}
						</Menu.Dropdown>
					</Menu>
				)}
			</div>
		);
	}
	wrappers.set(Tab, SurfaceTab);
	return SurfaceTab;
}

export const DefaultSurfaceTab = withSurfaceTabMenu(DockviewDefaultTab);
