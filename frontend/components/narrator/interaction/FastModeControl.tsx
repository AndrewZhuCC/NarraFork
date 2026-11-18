import {
	ActionIcon,
	Group,
	Popover,
	SegmentedControl,
	Stack,
	Switch,
	Text,
	Tooltip,
} from "@mantine/core";
import { type FastModeOverride, normalizeFastModeOverride } from "@shared/fast-mode";
import { IconBolt, IconBoltFilled } from "@tabler/icons-react";
import type React from "react";
import { useState } from "react";

export interface FastModeControlProps {
	/** Popover placement — desktop uses top-end, the mobile toolbar bottom-end. */
	position: "top-end" | "bottom-end";
	/** Session-level override; "inherit" follows the user default. */
	fastModeOverride: FastModeOverride;
	/** The user-level default fast-mode state (what "inherit" resolves to). */
	fastModeDefault: boolean;
	/** Effective state after resolving inherit — drives the icon colour. */
	fastModeEnabled: boolean;
	supportsCodexControls: boolean;
	supportsUltrafast: boolean;
	/**
	 * Coarse-pointer / mobile viewports open the settings by long-press instead of
	 * hover, so hover handlers are suppressed when true.
	 */
	fastModeUsesTapSettings: boolean;
	settingsOpened: boolean;
	setSettingsOpened: (opened: boolean) => void;
	openSettings: () => void;
	closeSettings: () => void;
	scheduleSettingsClose: () => void;
	startLongPress: (event: React.PointerEvent) => void;
	clearLongPressTimer: () => void;
	/** True immediately after a long-press fired, so the ensuing click is ignored. */
	longPressFiredRef: React.MutableRefObject<boolean>;
	narratorId: string;
	// biome-ignore lint/suspicious/noExplicitAny: mutation object passthrough from useUpdateFastMode.
	fastModeMutation: any;
	// biome-ignore lint/suspicious/noExplicitAny: mutation object passthrough from useUpdateUserPreferences.
	updateUserPrefs: any;
	/** narrator translation fn (namespace "narrator"). */
	t: (key: string, opts?: Record<string, unknown>) => string;
}

/**
 * Fast-mode toggle button + its settings popover (session override segmented
 * control + user-default switch). Extracted verbatim from NarratorPanel; all
 * state/handlers are injected so behaviour is identical across the desktop and
 * mobile status-bar mounts.
 */
export function FastModeControl(props: FastModeControlProps) {
	const {
		position,
		fastModeOverride,
		fastModeDefault,
		fastModeEnabled,
		supportsCodexControls,
		supportsUltrafast,
		fastModeUsesTapSettings,
		settingsOpened,
		setSettingsOpened,
		openSettings,
		closeSettings,
		scheduleSettingsClose,
		startLongPress,
		clearLongPressTimer,
		longPressFiredRef,
		narratorId,
		fastModeMutation,
		updateUserPrefs,
		t,
	} = props;
	const [keyboardOpened, setKeyboardOpened] = useState(false);
	const closeOnMouseLeave =
		fastModeUsesTapSettings || keyboardOpened ? undefined : scheduleSettingsClose;
	const ultrafastSelected = fastModeOverride === "ultrafast";
	const ultrafastUnavailable = ultrafastSelected && !supportsUltrafast;
	const ultrafastActive = ultrafastSelected && supportsUltrafast;
	const defaultState = fastModeDefault ? t("fast_mode_on") : t("fast_mode_off");
	let tooltip = t("fast_mode_tooltip");
	let buttonLabel = t("fast_mode");
	if (ultrafastUnavailable) {
		tooltip = t("fast_mode_ultrafast_unavailable");
		buttonLabel = tooltip;
	} else if (ultrafastActive) {
		tooltip = t("fast_mode_ultrafast_hint");
		buttonLabel = t("fast_mode_ultrafast");
	} else if (fastModeOverride === "inherit") {
		tooltip = t("fast_mode_inherit_tooltip", { state: defaultState });
	}

	return (
		<Popover
			opened={settingsOpened}
			onChange={setSettingsOpened}
			onClose={() => {
				closeSettings();
				setKeyboardOpened(false);
			}}
			trapFocus={keyboardOpened}
			returnFocus={keyboardOpened}
			position={position}
			width={fastModeUsesTapSettings ? 280 : 320}
			shadow="md"
			withinPortal
		>
			<Popover.Target>
				<Group
					gap={4}
					wrap="nowrap"
					onMouseEnter={fastModeUsesTapSettings ? undefined : openSettings}
					onMouseLeave={closeOnMouseLeave}
					style={{ flexShrink: 0 }}
				>
					<Tooltip
						label={tooltip}
						position={position.startsWith("top") ? "top" : "bottom"}
						disabled={settingsOpened}
					>
						<ActionIcon
							variant="subtle"
							color={ultrafastActive ? "orange" : fastModeEnabled ? "yellow" : "gray"}
							size="sm"
							aria-label={buttonLabel}
							aria-pressed={fastModeEnabled}
							aria-haspopup="dialog"
							aria-expanded={settingsOpened}
							aria-keyshortcuts="ArrowDown"
							onKeyDown={(event) => {
								if (event.key !== "ArrowDown") return;
								event.preventDefault();
								setKeyboardOpened(true);
								openSettings();
							}}
							onPointerDown={startLongPress}
							onPointerUp={clearLongPressTimer}
							onPointerCancel={clearLongPressTimer}
							onPointerLeave={clearLongPressTimer}
							onContextMenu={(event) => event.preventDefault()}
							onClick={(event) => {
								if (longPressFiredRef.current) {
									event.preventDefault();
									event.stopPropagation();
									longPressFiredRef.current = false;
									return;
								}
								// Clicking pins this session against its current effective
								// state; the popover restores "follow default".
								fastModeMutation.mutate({
									id: narratorId,
									fastModeOverride: fastModeEnabled || !supportsCodexControls ? "off" : "on",
								});
							}}
						>
							{ultrafastActive ? <IconBoltFilled size={16} /> : <IconBolt size={16} />}
						</ActionIcon>
					</Tooltip>
				</Group>
			</Popover.Target>
			<Popover.Dropdown
				onMouseEnter={fastModeUsesTapSettings ? undefined : openSettings}
				onMouseLeave={closeOnMouseLeave}
			>
				<Stack gap={8}>
					<Text size="sm" fw={600}>
						{t("fast_mode")}
					</Text>
					<SegmentedControl
						size="xs"
						fullWidth
						aria-label={t("fast_mode_choose_tier")}
						value={ultrafastUnavailable ? "" : fastModeOverride}
						onChange={(value) =>
							fastModeMutation.mutate({
								id: narratorId,
								fastModeOverride: normalizeFastModeOverride(value),
							})
						}
						data={[
							{ value: "inherit", label: t("fast_mode_session_inherit", { state: defaultState }) },
							...(supportsCodexControls ? [{ value: "on", label: t("fast_mode_on") }] : []),
							{ value: "off", label: t("fast_mode_off") },
							...(supportsUltrafast
								? [{ value: "ultrafast", label: t("fast_mode_ultrafast") }]
								: []),
						]}
					/>
					<Text size="xs" c="dimmed">
						{ultrafastUnavailable
							? t("fast_mode_ultrafast_unavailable")
							: t("fast_mode_session_desc")}
					</Text>
					{supportsUltrafast && (
						<Text size="xs" c="dimmed">
							{t("fast_mode_ultrafast_hint")}
						</Text>
					)}
					<Switch
						size="sm"
						checked={fastModeDefault}
						onChange={(event) =>
							updateUserPrefs.mutate({ fastModeDefault: event.currentTarget.checked })
						}
						label={t("fast_mode_default_switch")}
					/>
					<Text size="xs" c="dimmed">
						{fastModeDefault ? t("fast_mode_default_on_desc") : t("fast_mode_default_off_desc")}
					</Text>
					<Text size="xs" c="dimmed">
						{fastModeUsesTapSettings ? t("fast_mode_mobile_hint") : t("fast_mode_desktop_hint")}
					</Text>
				</Stack>
			</Popover.Dropdown>
		</Popover>
	);
}
