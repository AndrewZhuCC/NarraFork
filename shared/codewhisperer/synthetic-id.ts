/**
 * ID generation for synthetic protocol artifacts.
 *
 * When an upstream model leaks a tool call as XML inside the assistant text
 * stream instead of using native tool-use fields, the protocol layer has to mint
 * a `toolUseId` for it. That ID only has to be unique, so this wraps `nanoid`
 * directly rather than importing the host's `server/lib/id` — which 143 files
 * depend on and which must stay out of bundled plugin code.
 *
 * Kept byte-compatible with `generateId()` from the host: same generator, same
 * default alphabet.
 */

import { nanoid } from "nanoid";

/** Generate a unique ID of `size` characters (nanoid alphabet). */
export function generateSyntheticId(size = 21): string {
	return nanoid(size);
}
