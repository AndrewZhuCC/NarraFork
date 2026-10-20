import {
	type ComposerAsset,
	type ComposerDocument,
	type ComposerInlineNode,
	composerDocumentText,
	legacyComposerDocumentText,
	readComposerDocument,
} from "@shared/composer-document";
import type { FileReference } from "@shared/file-reference";
import {
	fileReferenceQuote,
	parseComposerQuotePrefix,
	parseComposerSegments,
	serializeComposerQuotes,
} from "./composer-quotes";
import { getComposerAssetId } from "./draft-image-attachments";
import { type FileReferenceInput, readFileReferences } from "./file-reference-input";

export function composerNodeId(): string {
	return (
		globalThis.crypto?.randomUUID?.() ?? `node-${Date.now()}-${Math.random().toString(36).slice(2)}`
	);
}

/** Old records never contained attachment positions: import their actual old order, not a guess. */
export function importComposerInput(input: FileReferenceInput): ComposerDocument {
	const stored = readComposerDocument(input.composerDocument);
	if (stored) return stored;
	const refs = readFileReferences(input.fileReferences, input.text);
	const parsed = parseComposerQuotePrefix(input.text);
	const unknownFileAnchor = parsed.quotes.some(
		(quote) => !quote.anchor && quote.label.includes(" @@f:"),
	);
	const quoteDocument = unknownFileAnchor
		? undefined
		: readComposerDocument({
				version: 1,
				paragraphs: [
					{
						children: parsed.quotes.map((quote) => {
							const ref = quote.anchor?.startsWith("f:")
								? refs.find((ref) => quote.anchor === `f:${ref.id}`)
								: undefined;
							return ref
								? { type: "fileReference", id: ref.id, reference: ref, comment: quote.comment }
								: { type: "quote", id: composerNodeId(), ...quote };
						}),
					},
				],
				assets: [],
			});
	const projection = quoteDocument && legacyComposerDocumentText(quoteDocument);
	// File-comment nodes also project a body token. The legacy prefix itself must
	// still match exactly: do not replace a different excerpt or invent a reference.
	const projectedPrefix = projection?.text.slice(
		0,
		projection.fileReferences[0]?.inputRange?.[0] ?? projection.text.length,
	);
	const prefixLength =
		quoteDocument && projectedPrefix === input.text.slice(0, parsed.prefixLength)
			? parsed.prefixLength
			: 0;

	function importChildren(prefixLength: number, prefixNodes: ComposerInlineNode[]) {
		const children: ComposerInlineNode[] = [];
		const files = new Map(
			prefixNodes
				.filter((node) => node.type === "fileReference")
				.map((node) => [node.reference.id, node]),
		);
		let prefixIndex = 0;
		const appendQuotes = () => {
			while (prefixNodes[prefixIndex]?.type === "quote") children.push(prefixNodes[prefixIndex++]);
		};
		const appendReference = (ref: FileReference) => {
			const node = files.get(ref.id) ?? {
				type: "fileReference" as const,
				id: ref.id,
				reference: ref,
			};
			children.push(node);
			if (node === prefixNodes[prefixIndex]) {
				prefixIndex++;
				appendQuotes();
			}
		};
		appendQuotes();
		const ranges = refs
			.filter((ref) => ref.inputRange && ref.inputRange[0] >= prefixLength)
			.sort((a, b) => (a.inputRange?.[0] ?? 0) - (b.inputRange?.[0] ?? 0));
		let offset = prefixLength;
		const used = new Set<string>();
		for (const ref of ranges) {
			const range = ref.inputRange;
			if (!range || range[0] < offset) continue;
			if (range[0] > offset)
				children.push({ type: "text", text: input.text.slice(offset, range[0]) });
			appendReference(ref);
			used.add(ref.id);
			offset = range[1];
		}
		if (offset < input.text.length) children.push({ type: "text", text: input.text.slice(offset) });
		for (const ref of refs) {
			if (!used.has(ref.id)) appendReference(ref);
		}
		return { children, complete: prefixIndex === prefixNodes.length };
	}
	// Unknown or noncanonical prefixes remain original text, never source atoms.
	// ponytail: conflicting legacy prefix/file orders stay raw; promoting both would
	// need a separate prefix-order model instead of moving real file occurrences.
	const imported = prefixLength
		? importChildren(prefixLength, quoteDocument?.paragraphs[0].children ?? [])
		: undefined;
	const positional = importPositionalQuotes(input.text, refs);
	const children =
		positional ?? (imported?.complete ? imported.children : importChildren(0, []).children);
	return { version: 1, paragraphs: [{ children }], assets: [] };
}

/** Current text-only records may contain quote/comment blocks after typed content. */
function importPositionalQuotes(
	text: string,
	refs: FileReference[],
): ComposerInlineNode[] | undefined {
	const segments = parseComposerSegments(text);
	const firstText = segments.findIndex(
		(segment) => segment.type === "text" && /\S/u.test(segment.text),
	);
	if (
		!segments.some(
			(segment, index) => segment.type === "quote" && firstText >= 0 && index > firstText,
		)
	)
		return;
	const comments = new Map<string, string>();
	for (const segment of segments) {
		if (segment.type !== "quote" || !segment.quote.anchor?.startsWith("f:")) continue;
		const ref = refs.find((ref) => segment.quote.anchor === `f:${ref.id}`);
		if (
			!ref ||
			!segment.quote.comment ||
			comments.has(ref.id) ||
			serializeComposerQuotes([{ ...fileReferenceQuote(ref), comment: segment.quote.comment }]) !==
				serializeComposerQuotes([segment.quote])
		)
			return;
		comments.set(ref.id, segment.quote.comment);
	}
	const children: ComposerInlineNode[] = [];
	const used = new Set<string>();
	const ranges = refs
		.filter((ref) => ref.inputRange)
		.sort((a, b) => (a.inputRange?.[0] ?? 0) - (b.inputRange?.[0] ?? 0));
	const appendText = (value: string) => {
		if (!value) return;
		const last = children[children.length - 1];
		if (last?.type === "text") last.text += value;
		else children.push({ type: "text", text: value });
	};
	let cursor = 0;
	for (const segment of segments) {
		if (segment.type === "quote") {
			const block = serializeComposerQuotes([segment.quote]);
			if (!text.startsWith(block, cursor)) return;
			const last = children[children.length - 1];
			// Drop exactly the padding break the projection adds before a positional block.
			if (last?.type === "text" && last.text.endsWith("\n")) {
				last.text = last.text.slice(0, -1);
				if (!last.text) children.pop();
			}
			if (!segment.quote.anchor?.startsWith("f:"))
				children.push({ type: "quote", id: composerNodeId(), ...segment.quote });
			cursor += block.length;
		} else if (segment.type === "text") {
			const end = cursor + segment.text.length;
			for (const ref of ranges) {
				const range = ref.inputRange;
				if (!range || range[0] < cursor || range[1] > end) continue;
				appendText(text.slice(cursor, range[0]));
				children.push({
					type: "fileReference",
					id: ref.id,
					reference: ref,
					comment: comments.get(ref.id),
				});
				used.add(ref.id);
				cursor = range[1];
			}
			appendText(text.slice(cursor, end));
			cursor = end;
		}
	}
	if (used.size !== refs.length) return;
	const doc = readComposerDocument({ version: 1, paragraphs: [{ children }], assets: [] });
	if (!doc || composerDocumentText(doc).text !== text) return;
	return children;
}

export function composerInputFromDocument(composerDocument: ComposerDocument): FileReferenceInput {
	return { ...composerDocumentText(composerDocument), composerDocument };
}

export function composerAssetsFromFiles(images: File[], textFiles: File[]): ComposerAsset[] {
	return [
		...images.map((file) => ({ file, kind: "image" as const })),
		...textFiles.map((file) => ({ file, kind: "textFile" as const })),
	].map(({ file, kind }) => ({
		id: getComposerAssetId(file),
		kind,
		name: file.name,
		size: file.size,
		mimeType: file.type,
	}));
}

/** Merge hydrated/new files without removing missing resources or shifting existing atoms. */
export function withComposerAttachments(
	document: ComposerDocument,
	images: File[],
	textFiles: File[],
): ComposerDocument {
	const files = composerAssetsFromFiles(images, textFiles);
	const known = new Set(document.assets.map((asset) => asset.id));
	const fresh = files.filter((asset) => !known.has(asset.id));
	if (!fresh.length) return document;
	const paragraphs = document.paragraphs.map((paragraph) => ({
		children: [...paragraph.children],
	}));
	if (!paragraphs.length) paragraphs.push({ children: [] });
	const last = paragraphs[paragraphs.length - 1];
	for (const asset of fresh) {
		last.children.push({ type: "attachment", id: composerNodeId(), assetId: asset.id });
	}
	return { ...document, paragraphs, assets: [...document.assets, ...fresh] };
}

export function composerFileResources(images: File[], textFiles: File[]): Map<string, File> {
	return new Map([...images, ...textFiles].map((file) => [getComposerAssetId(file), file]));
}

export function missingComposerAssets(
	document: ComposerDocument,
	resources: ReadonlyMap<string, unknown>,
): boolean {
	return document.assets.some((asset) => !resources.has(asset.id));
}
