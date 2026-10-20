import { Alert, Button, Group, Text } from "@mantine/core";
import { IconRefresh } from "@tabler/icons-react";
import { useTranslation } from "react-i18next";
import { useVersionCheck } from "../hooks/useVersionCheck";
import { TOP_BANNER_SAFE_AREA_STYLE } from "../lib/safe-area";
import { Z } from "../lib/z-index";

export function VersionUpdateBanner() {
	const { t } = useTranslation("common");
	const { updateAvailable, serverVersion, refresh, dismiss } = useVersionCheck();

	if (!updateAvailable) return null;

	return (
		<Alert
			className="nf-top-banner"
			color="indigo"
			variant="light"
			withCloseButton
			onClose={dismiss}
			styles={{
				body: { minWidth: 0 },
				/*
				 * Alert renders the close button as a flex item of its wrapper and gives
				 * the part no rules of its own, so the default `align-items: stretch`
				 * leaves it parked at the chain's top edge. Centre it on the cross axis.
				 */
				closeButton: { flexShrink: 0, alignSelf: "center" },
			}}
			style={{
				position: "fixed",
				...TOP_BANNER_SAFE_AREA_STYLE,
				/*
				 * Centred with auto margins rather than `left: 50%` + translateX(-50%):
				 * the WCO no-drag rect is derived from this element's *layout* box, so
				 * the layout box is kept identical to the painted box instead of relying
				 * on a transform to coincide with it.
				 */
				left: 0,
				right: 0,
				marginInline: "auto",
				zIndex: Z.toast,
				maxWidth: 500,
				width: "calc(100% - 32px)",
				borderRadius: "var(--mantine-radius-sm)",
			}}
		>
			<Group justify="space-between" wrap="nowrap">
				<Text size="sm" style={{ flex: 1, minWidth: 0, overflowWrap: "anywhere" }}>
					{t("versionUpdate", { version: serverVersion })}
				</Text>
				<Button
					size="xs"
					variant="light"
					leftSection={<IconRefresh size={14} />}
					onClick={refresh}
					style={{ flexShrink: 0 }}
				>
					{t("refresh")}
				</Button>
			</Group>
		</Alert>
	);
}
