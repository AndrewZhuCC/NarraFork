import type {
	KnowledgeEntryLink,
	KnowledgeEntryViewerCaps,
	KnowledgeLinkType,
} from "../../../lib/api";

/** Link types a user can create, in display order. */
export const LINK_TYPES: KnowledgeLinkType[] = [
	"related",
	"expands",
	"supersedes",
	"depends_on",
	"parent",
	"custom",
];

/** Group links by type, in the {@link LINK_TYPES} order (other types last, by name). */
export function groupLinksByType(
	links: KnowledgeEntryLink[],
): { type: KnowledgeLinkType; links: KnowledgeEntryLink[] }[] {
	const byType = new Map<KnowledgeLinkType, KnowledgeEntryLink[]>();
	for (const l of links) {
		const list = byType.get(l.linkType) ?? [];
		list.push(l);
		byType.set(l.linkType, list);
	}
	const rank = (type: KnowledgeLinkType) => {
		const i = LINK_TYPES.indexOf(type);
		return i === -1 ? LINK_TYPES.length : i;
	};
	return [...byType.entries()]
		.sort(([a], [b]) => rank(a) - rank(b) || a.localeCompare(b))
		.map(([type, list]) => ({ type, links: list }));
}

/**
 * Which actions the read page offers (§5.2), purely from the server's capability bits:
 *  - exactly one primary action — "Edit" (direct write of main) for `canWriteMain`, otherwise
 *    the personal-version action ("Propose a change" / "My personal version");
 *  - the personal-version action stays available as a secondary one for direct writers;
 *  - "Settings" only when at least one settings section is editable (the page itself still
 *    renders read-only for anyone who opens it).
 */
export interface EntryReadActions {
	primary: "edit" | "personal";
	secondaryPersonal: boolean;
	settings: boolean;
}

export function entryReadActions(caps: KnowledgeEntryViewerCaps): EntryReadActions {
	return {
		primary: caps.canWriteMain ? "edit" : "personal",
		secondaryPersonal: caps.canWriteMain,
		settings: hasAnySettingsCap(caps),
	};
}

export function hasAnySettingsCap(caps: KnowledgeEntryViewerCaps): boolean {
	return caps.canEditMeta || caps.canManageAcl || caps.canTransferOwner || caps.canDelete;
}
