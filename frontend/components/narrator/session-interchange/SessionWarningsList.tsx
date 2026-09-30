import { Alert, List, Text } from "@mantine/core";
import { IconAlertTriangle } from "@tabler/icons-react";
import { useTranslation } from "react-i18next";
import type { SessionWarning } from "../../../lib/api/session-interchange";

/**
 * Import/export warnings, grouped by kind with a count. Unknown kinds fall back
 * to their raw name so a newer server never shows a blank line.
 *
 * `direction` picks the heading: the same list follows an import and an export,
 * and an export saying "imported with notes" misreports what just happened.
 */
export function SessionWarningsList({
	warnings,
	direction,
}: {
	warnings: SessionWarning[];
	direction: "import" | "export";
}) {
	const { t } = useTranslation("narrators");
	if (warnings.length === 0) return null;
	return (
		<Alert
			color="yellow"
			variant="light"
			icon={<IconAlertTriangle size={16} />}
			title={t(
				direction === "import"
					? "sessionInterchange.warningsTitle"
					: "sessionInterchange.exportWarningsTitle",
			)}
		>
			<List size="xs" spacing={2}>
				{warnings.map((warning) => (
					<List.Item key={warning.kind}>
						<Text size="xs" span>
							{t(`sessionInterchange.warning.${warning.kind}`, {
								defaultValue: warning.kind,
								count: warning.count,
							})}
						</Text>
						{warning.sample ? (
							<Text size="xs" c="dimmed" span ff="monospace">
								{" "}
								({warning.sample})
							</Text>
						) : null}
					</List.Item>
				))}
			</List>
		</Alert>
	);
}
