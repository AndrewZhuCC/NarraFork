import type { LocalImportSource } from "@shared/local-provider-import";
import { createContext, useContext } from "react";
import type { AddProviderDraft } from "./provider-add-draft";

// The providers parent route keeps its reducer mounted while the add route is open.
// Resolves with the new provider's id when the page should open it afterwards.
export const ProviderAddContext = createContext<
	((draft: AddProviderDraft) => Promise<string | undefined>) | null
>(null);

export interface ProviderImportContextValue {
	/** Saves one provider imported from another tool's local config as a new provider. */
	importProvider: (draft: AddProviderDraft) => Promise<{ refreshFailed: boolean }>;
	/** Which tools have config directories on the server (existence only); undefined until known. */
	detectedSources?: readonly { source: LocalImportSource; found: boolean }[];
	/** Detection is still running, so the page should not settle its default selection yet. */
	detecting: boolean;
}

/** Null when the viewer cannot import (not an admin, or outside the providers route). */
export const ProviderImportContext = createContext<ProviderImportContextValue | null>(null);
export function useProviderImport() {
	return useContext(ProviderImportContext);
}

export const TokenDanceAddContext = createContext<{
	login: (addPage?: Record<string, unknown>) => Promise<void>;
	restoredAddPage?: Record<string, unknown>;
	consumeRestoredAddPage?: () => void;
} | null>(null);
export function useTokenDanceAdd() {
	return useContext(TokenDanceAddContext);
}

export function useAddProvider() {
	const add = useContext(ProviderAddContext);
	if (!add) throw new Error("Provider add route requires the providers parent context");
	return add;
}
