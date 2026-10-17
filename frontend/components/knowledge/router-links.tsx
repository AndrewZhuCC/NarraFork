/**
 * Mantine `Anchor` / `Button` / `ActionIcon` as type-safe TanStack Router links (`createLink`),
 * so links with path params (`/knowledge/mine/$personalEntryId`,
 * `/knowledge/requests/$submissionId`) are real `<a href>`s — middle-click / copy link work —
 * and their params stay type-checked. (`component={Link}` on a Mantine polymorphic component
 * loses the params typing.)
 */
import {
	ActionIcon,
	type ActionIconProps,
	Anchor,
	type AnchorProps,
	Button,
	type ButtonProps,
} from "@mantine/core";
import { createLink } from "@tanstack/react-router";
import { type AnchorHTMLAttributes, forwardRef } from "react";

const MantineAnchor = forwardRef<
	HTMLAnchorElement,
	AnchorProps & AnchorHTMLAttributes<HTMLAnchorElement>
>((props, ref) => <Anchor ref={ref} {...props} />);

const MantineButtonAnchor = forwardRef<
	HTMLAnchorElement,
	ButtonProps & AnchorHTMLAttributes<HTMLAnchorElement>
>((props, ref) => <Button ref={ref} component="a" {...props} />);

const MantineActionIconAnchor = forwardRef<
	HTMLAnchorElement,
	ActionIconProps & AnchorHTMLAttributes<HTMLAnchorElement>
>((props, ref) => <ActionIcon ref={ref} component="a" {...props} />);

export const AnchorLink = createLink(MantineAnchor);
export const ButtonLink = createLink(MantineButtonAnchor);
export const ActionIconLink = createLink(MantineActionIconAnchor);
