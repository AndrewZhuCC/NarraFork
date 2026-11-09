/**
 * The permission form's button row, with keyboard-driven focus (shared by the plain permission
 * form and an outline plan's approval, `OutlinePlanApproval`).
 */
import { Button, Group, Text, Tooltip } from "@mantine/core";
import { type CSSProperties, useEffect } from "react";

export interface PermButton {
	label: string;
	color: string;
	variant?: string;
	tooltip?: string;
	loading?: boolean;
	disabled?: boolean;
	onClick: () => void;
}

/**
 * Renders a row of permission buttons with keyboard-driven focus highlight.
 * Reports button count to the parent via setButtonCount so the global
 * keydown handler knows the navigation range.
 */
export function PermButtonBar({
	buttons,
	focusIndex,
	setButtonCount,
	registerActions,
}: {
	buttons: PermButton[];
	focusIndex: number | null;
	setButtonCount: (n: number) => void;
	registerActions: (actions: (() => void)[]) => void;
}) {
	useEffect(() => {
		setButtonCount(buttons.length);
		registerActions(
			buttons.map((button) => () => {
				if (!button.disabled && !button.loading) button.onClick();
			}),
		);
	}, [buttons, setButtonCount, registerActions]);

	return (
		<Group gap="sm">
			{buttons.map((btn, i) => {
				const focused = focusIndex === i;
				const button = (
					<Button
						key={`${btn.label}-${btn.color}`}
						size="sm"
						color={btn.color}
						variant={btn.variant as "light" | "subtle" | undefined}
						onClick={btn.onClick}
						loading={btn.loading}
						disabled={btn.disabled}
						className={focused ? "perm-btn-pulse" : undefined}
						style={
							focused
								? ({
										"--perm-pulse-color": `var(--mantine-color-${btn.color}-filled)`,
									} as CSSProperties)
								: undefined
						}
					>
						{btn.label}
						{focused && (
							<Text span size="xs" ml={4} opacity={0.7}>
								⏎
							</Text>
						)}
					</Button>
				);
				return btn.tooltip ? (
					<Tooltip key={`${btn.label}-${btn.color}`} label={btn.tooltip} multiline>
						{button}
					</Tooltip>
				) : (
					button
				);
			})}
		</Group>
	);
}
