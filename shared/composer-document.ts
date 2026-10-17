import type { FileReference, FileSelection } from "./file-reference";

export const MAX_COMPOSER_DOCUMENT_BYTES = 2 * 1024 * 1024;
export const MAX_COMPOSER_DOCUMENT_NODES = 4096;
export const MAX_COMPOSER_DOCUMENT_TEXT = 200_000;
export const MAX_COMPOSER_QUOTES = 8;
export const MAX_COMPOSER_QUOTE_CHARS = 8000;

export interface ComposerAsset {
	id: string;
	kind: "image" | "textFile";
	name: string;
	size: number;
	mimeType: string;
}
export type ComposerInlineNode =
	| { type: "text"; text: string }
	| { type: "break" }
	| { type: "quote"; id: string; label: string; text: string; anchor?: string; comment?: string }
	| { type: "fileReference"; id: string; reference: FileReference; comment?: string }
	| { type: "attachment"; id: string; assetId: string };
export interface ComposerDocument {
	version: 1;
	paragraphs: { children: ComposerInlineNode[] }[];
	assets: ComposerAsset[];
}

function object(value: unknown, keys: string[]): Record<string, unknown> {
	if (!value || typeof value !== "object" || Array.isArray(value))
		throw new Error("Invalid composer object");
	const item = value as Record<string, unknown>;
	if (Object.keys(item).some((key) => !keys.includes(key)))
		throw new Error("Unknown composer field");
	return item;
}
function string(value: unknown, max: number, empty = false): string {
	if (typeof value !== "string" || value.length > max || (!empty && !value.length))
		throw new Error("Invalid composer string");
	return value;
}
function hasControl(value: string, spaces = false): boolean {
	for (let index = 0; index < value.length; index++) {
		const code = value.charCodeAt(index);
		if (code < (spaces ? 33 : 32) || code === 127) return true;
	}
	return false;
}
function identifier(value: unknown): string {
	const id = string(value, 4096);
	if (hasControl(id, true)) throw new Error("Invalid composer identity");
	return id;
}
function reference(value: unknown): FileReference {
	const item = object(value, [
		"id",
		"deviceId",
		"path",
		"label",
		"selection",
		"inputRange",
		"expectedHash",
	]);
	const result: FileReference = {
		id: identifier(item.id),
		deviceId: identifier(item.deviceId),
		path: string(item.path, 4096),
		label: string(item.label, 4096),
	};
	if (hasControl(result.path + result.label)) throw new Error("Invalid file reference");
	if (item.selection !== undefined) {
		const keys = ["startLineNumber", "startColumn", "endLineNumber", "endColumn"];
		const selection = object(item.selection, keys);
		if (
			keys.some(
				(key) =>
					!Number.isSafeInteger(selection[key]) ||
					Number(selection[key]) < 1 ||
					Number(selection[key]) > 1_000_000,
			)
		)
			throw new Error("Invalid selection");
		const s = selection as unknown as FileSelection;
		if (
			s.endLineNumber < s.startLineNumber ||
			(s.endLineNumber === s.startLineNumber && s.endColumn < s.startColumn)
		)
			throw new Error("Invalid selection");
		result.selection = { ...s };
	}
	if (item.expectedHash !== undefined) result.expectedHash = string(item.expectedHash, 128);
	if (item.inputRange !== undefined) {
		const range = item.inputRange;
		if (
			!Array.isArray(range) ||
			range.length !== 2 ||
			range.some((n) => !Number.isSafeInteger(n) || n < 0 || n > MAX_COMPOSER_DOCUMENT_TEXT) ||
			range[1] <= range[0]
		)
			throw new Error("Invalid input range");
		result.inputRange = [range[0], range[1]];
	}
	return result;
}

/** Parse only the semantic allowlist, never editor JSON, HTML or browser resource URLs. */
export function parseComposerDocument(value: unknown): ComposerDocument {
	const root = object(value, ["version", "paragraphs", "assets"]);
	if (
		root.version !== 1 ||
		!Array.isArray(root.paragraphs) ||
		!Array.isArray(root.assets) ||
		root.assets.length > 4096 ||
		root.paragraphs.length > MAX_COMPOSER_DOCUMENT_NODES
	)
		throw new Error("Invalid composer document");
	const assetIds = new Set<string>();
	const assets = root.assets.map((value): ComposerAsset => {
		const item = object(value, ["id", "kind", "name", "size", "mimeType"]);
		const id = identifier(item.id);
		if (
			assetIds.has(id) ||
			(item.kind !== "image" && item.kind !== "textFile") ||
			!Number.isSafeInteger(item.size) ||
			Number(item.size) < 0
		)
			throw new Error("Invalid composer asset");
		assetIds.add(id);
		const name = string(item.name, 4096);
		const mimeType = string(item.mimeType, 255, true);
		if (hasControl(name) || name.includes("/") || name.includes("\\") || hasControl(mimeType, true))
			throw new Error("Invalid asset metadata");
		return { id, kind: item.kind, name, size: Number(item.size), mimeType };
	});
	const ids = new Set<string>();
	const refIds = new Set<string>();
	const usedAssets = new Set<string>();
	let nodes = root.paragraphs.length;
	let textChars = 0;
	let quotes = 0;
	const paragraphs = root.paragraphs.map((value) => {
		const paragraph = object(value, ["children"]);
		if (!Array.isArray(paragraph.children)) throw new Error("Invalid composer paragraph");
		nodes += paragraph.children.length;
		if (nodes > MAX_COMPOSER_DOCUMENT_NODES) throw new Error("Too many composer nodes");
		return {
			children: paragraph.children.map((value): ComposerInlineNode => {
				const node = object(value, [
					"type",
					"text",
					"id",
					"label",
					"anchor",
					"comment",
					"reference",
					"assetId",
				]);
				if (node.type === "text") {
					object(value, ["type", "text"]);
					const text = string(node.text, MAX_COMPOSER_DOCUMENT_TEXT, true);
					textChars += text.length;
					return { type: "text", text };
				}
				if (node.type === "break") {
					object(value, ["type"]);
					return { type: "break" };
				}
				const id = identifier(node.id);
				if (ids.has(id)) throw new Error("Duplicate composer node identity");
				ids.add(id);
				if (node.type === "quote") {
					object(value, ["type", "id", "label", "text", "anchor", "comment"]);
					if (++quotes > MAX_COMPOSER_QUOTES) throw new Error("Too many composer quotes");
					const text = string(node.text, MAX_COMPOSER_QUOTE_CHARS);
					const label = string(node.label, 80);
					if (/\s/u.test(label[0]) || /[\r\n]/u.test(label)) throw new Error("Invalid quote label");
					textChars += text.length;
					const result: ComposerInlineNode = { type: "quote", id, label, text };
					if (node.comment !== undefined) {
						result.comment = string(node.comment, MAX_COMPOSER_DOCUMENT_TEXT, true);
						textChars += result.comment.length;
					}
					if (node.anchor !== undefined) {
						const anchor = string(node.anchor, 256);
						if (!/^[mt]:[A-Za-z0-9_-]{4,}$/u.test(anchor)) throw new Error("Invalid quote anchor");
						result.anchor = anchor;
					}
					return result;
				}
				if (node.type === "fileReference") {
					object(value, ["type", "id", "reference", "comment"]);
					const ref = reference(node.reference);
					if (refIds.has(ref.id) || refIds.size >= 16)
						throw new Error("Invalid composer references");
					refIds.add(ref.id);
					const result: ComposerInlineNode = { type: "fileReference", id, reference: ref };
					if (node.comment !== undefined) {
						result.comment = string(node.comment, MAX_COMPOSER_DOCUMENT_TEXT, true);
						textChars += result.comment.length;
					}
					return result;
				}
				if (node.type === "attachment") {
					object(value, ["type", "id", "assetId"]);
					const assetId = identifier(node.assetId);
					if (!assetIds.has(assetId)) throw new Error("Dangling composer asset");
					usedAssets.add(assetId);
					return { type: "attachment", id, assetId };
				}
				throw new Error("Unknown composer node type");
			}),
		};
	});
	if (
		nodes > MAX_COMPOSER_DOCUMENT_NODES ||
		textChars > MAX_COMPOSER_DOCUMENT_TEXT ||
		usedAssets.size !== assetIds.size
	)
		throw new Error("Invalid composer budget or unused assets");
	const doc: ComposerDocument = { version: 1, paragraphs, assets };
	const encoded = JSON.stringify(doc);
	if (
		encoded.length > MAX_COMPOSER_DOCUMENT_BYTES ||
		new TextEncoder().encode(encoded).byteLength > MAX_COMPOSER_DOCUMENT_BYTES
	)
		throw new Error("Composer document too large");
	if (composerDocumentText(doc).text.length > MAX_COMPOSER_DOCUMENT_TEXT)
		throw new Error("Composer projection too long");
	return doc;
}
export function readComposerDocument(value: unknown): ComposerDocument | undefined {
	try {
		return parseComposerDocument(value);
	} catch {
		return undefined;
	}
}
export function composerFileReferenceToken(ref: FileReference): string {
	const s = ref.selection;
	if (!s) return `#file:${ref.label}`;
	const end =
		s.endColumn === 1 && s.endLineNumber > s.startLineNumber
			? s.endLineNumber - 1
			: s.endLineNumber;
	return `#file:${ref.label}:${s.startLineNumber}-${end}`;
}
/** Compatibility projection; positions always measure the final UTF-16 string. */
export function composerDocumentText(doc: ComposerDocument): {
	text: string;
	fileReferences: FileReference[];
} {
	let prefix = "";
	for (const p of doc.paragraphs)
		for (const n of p.children) {
			let quote: { label: string; text: string; anchor?: string; comment?: string };
			if (n.type === "quote") quote = n;
			else if (n.type === "fileReference" && n.comment) {
				const token = composerFileReferenceToken(n.reference);
				const label = token.slice("#file:".length).replace(/\s+/gu, " ").trim();
				quote = {
					label: label.length > 80 ? `${label.slice(0, 79)}…` : label,
					text: token,
					anchor: `f:${n.reference.id}`,
					comment: n.comment,
				};
			} else continue;
			const body = quote.text
				.split("\n")
				.map((line) => {
					const escaped = /^\\*\[quote\]/u.test(line) ? `\\${line}` : line;
					return escaped ? `> ${escaped}` : ">";
				})
				.join("\n");
			const comment = quote.comment
				? `\n| [comment]\n${quote.comment
						.split("\n")
						.map((line) => `| ${line}`)
						.join("\n")}`
				: "";
			prefix += `> [quote] ${quote.label}${quote.anchor ? ` @@${quote.anchor}` : ""}\n${body}${comment}\n\n`;
		}
	let text = prefix;
	const fileReferences: FileReference[] = [];
	doc.paragraphs.forEach((paragraph, index) => {
		if (index) text += "\n";
		for (const node of paragraph.children) {
			if (node.type === "text") text += node.text;
			if (node.type === "break") text += "\n";
			if (node.type === "fileReference") {
				const start = text.length;
				text += composerFileReferenceToken(node.reference);
				fileReferences.push({ ...node.reference, inputRange: [start, text.length] });
			}
		}
	});
	return { text, fileReferences };
}
