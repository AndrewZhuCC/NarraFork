import { MD_HEADING_SLUG_ATTR } from "@frontend/lib/markdown-anchor-scroll";
import { MarkdownOptionsContext } from "@openuidev/react-ui";
import { reactChildrenToHeadingText, slugifyHeading } from "@shared/pretext-layout/markdown-anchor";
import type { ComponentPropsWithoutRef, ReactNode, Ref } from "react";
import type { ExtraProps, Options } from "react-markdown";
import { MarkdownImage } from "../markdown/MarkdownImage";
import { MarkdownLink } from "../markdown/MarkdownLink";
import { remarkLocalFileLinks } from "../markdown/remark-local-file-links";

function OpenUIMarkdownLink({ href, children, title }: ComponentPropsWithoutRef<"a"> & ExtraProps) {
	return (
		<MarkdownLink
			href={href}
			title={title}
			labelText={reactChildrenToHeadingText(children)}
			linkClassName="openui-markdown-renderer-link"
		>
			{children}
		</MarkdownLink>
	);
}

function OpenUIMarkdownHeading({
	node,
	children,
	...props
}: ComponentPropsWithoutRef<"h1"> & ExtraProps) {
	const Tag = (node?.tagName ?? "h3") as "h1" | "h2" | "h3" | "h4" | "h5" | "h6";
	const text = reactChildrenToHeadingText(children);
	const slug = slugifyHeading(text);
	return (
		<Tag
			{...props}
			data-heading={text}
			data-level={Number(Tag.slice(1))}
			{...(slug ? { [MD_HEADING_SLUG_ATTR]: slug } : {})}
		>
			{children}
		</Tag>
	);
}

function withNarraForkMarkdown(options: Options): Options {
	return {
		...options,
		remarkPlugins: [...(options.remarkPlugins ?? []), remarkLocalFileLinks],
		components: {
			h1: OpenUIMarkdownHeading,
			h2: OpenUIMarkdownHeading,
			h3: OpenUIMarkdownHeading,
			h4: OpenUIMarkdownHeading,
			h5: OpenUIMarkdownHeading,
			h6: OpenUIMarkdownHeading,
			// Preserve inline renderers' flattened headings and the SDK's citations/code/tables.
			...options.components,
			a: OpenUIMarkdownLink,
			img: MarkdownImage,
		},
	};
}

export function OpenUIMarkdownProvider({
	children,
	rootRef,
}: {
	children: ReactNode;
	rootRef?: Ref<HTMLDivElement>;
}) {
	// ponytail: 0.17.0 exposes this hook through our Bun patch; remove the patch
	// when the SDK offers an equivalent shared Markdown customization point.
	return (
		<MarkdownOptionsContext.Provider value={withNarraForkMarkdown}>
			<div ref={rootRef} data-md-body="">
				{children}
			</div>
		</MarkdownOptionsContext.Provider>
	);
}
