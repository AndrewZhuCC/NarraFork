import { COMPOSER_ATTACHMENT_PLACEHOLDER } from "@shared/composer-document";

export type OrderedUserPart<I> = { type: "text"; text: string } | { type: "image"; image: I };

/**
 * Order a user turn's text and images for providers that accept interleaved parts.
 *
 * Composer projections mark an image's position with `[image #N: name]`, N being the image's
 * 1-based order among the message's images. Each named image is placed right after its
 * placeholder, which then doubles as the per-image label the provider guides recommend.
 * Images no placeholder names (older messages, attachments ahead of all text) keep the
 * provider's historical position, `unplaced`. A missing image leaves only its placeholder
 * text behind: either a `null` slot, or a gap in the `position` numbers of resolved images.
 */
export function orderUserContent<I>(
	text: string,
	resolved: readonly (I | null | undefined)[],
	unplaced: "before" | "after",
): OrderedUserPart<I>[] {
	const images = alignByPosition(resolved);
	const parts: OrderedUserPart<I>[] = [];
	const placed = new Set<number>();
	const pattern = new RegExp(COMPOSER_ATTACHMENT_PLACEHOLDER.source, "gu");
	let textStart = 0;
	for (const match of text.matchAll(pattern)) {
		const at = match.index ?? 0;
		// `\[image #…` is typed text the projection escaped, not a placeholder.
		if (match[1] !== "image" || text[at - 1] === "\\" || insideQuoteLine(text, at)) continue;
		const index = Number(match[2]) - 1;
		if (index >= images.length || placed.has(index)) continue;
		placed.add(index);
		const end = at + match[0].length;
		parts.push({ type: "text", text: text.slice(textStart, end) });
		const image = images[index];
		if (image != null) parts.push({ type: "image", image });
		textStart = end;
	}
	if (textStart < text.length) parts.push({ type: "text", text: text.slice(textStart) });
	const rest: OrderedUserPart<I>[] = [];
	images.forEach((image, index) => {
		if (image != null && !placed.has(index)) rest.push({ type: "image", image });
	});
	const ordered: OrderedUserPart<I>[] = [];
	for (const part of unplaced === "before" ? [...rest, ...parts] : [...parts, ...rest]) {
		const last = ordered[ordered.length - 1];
		if (part.type === "image") ordered.push(part);
		else if (last?.type === "text") last.text += part.text;
		// Providers reject whitespace-only text parts; such a tail belongs to the text before.
		else if (part.text.trim()) ordered.push({ ...part });
		else {
			const previous = ordered.findLast((candidate) => candidate.type === "text");
			if (previous?.type === "text") previous.text += part.text;
		}
	}
	return ordered;
}

/** Quoted excerpts (`> ` lines) are someone else's text; a placeholder there is literal. */
function insideQuoteLine(text: string, index: number): boolean {
	return text.startsWith(">", text.lastIndexOf("\n", index - 1) + 1);
}

function alignByPosition<I>(images: readonly (I | null | undefined)[]): (I | null | undefined)[] {
	const position = (image: I | null | undefined) => {
		const value =
			image && typeof image === "object" ? (image as { position?: unknown }).position : undefined;
		return typeof value === "number" && Number.isSafeInteger(value) && value >= 0 ? value : null;
	};
	if (!images.length || !images.every((image) => position(image) !== null)) return [...images];
	const aligned: (I | null)[] = [];
	for (const image of images) aligned[position(image) ?? 0] = image ?? null;
	return Array.from(aligned, (image) => image ?? null);
}
