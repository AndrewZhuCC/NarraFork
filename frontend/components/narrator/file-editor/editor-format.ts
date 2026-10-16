export type EditorEolTag = 1 | 2 | 3;
/** Load-only format root. No fabricated mixed-EOL metadata or editable v2 drafts. */
export type EditorFormat =
	| { version: 1; kind: "uniform"; tag: EditorEolTag; count: number }
	| {
			version: 1;
			kind: "mixed";
			prefixTag: EditorEolTag;
			prefixCount: number;
			chunks: readonly Uint8Array[];
			count: number;
	  };
export class EditorFormatBuilder {
	private root: EditorFormat = { version: 1, kind: "uniform", tag: 1, count: 0 };
	push(tags: Uint8Array) {
		if (!tags.length) return;
		for (const tag of tags) if (tag < 1 || tag > 3) throw new Error("EDITOR_FORMAT_INVALID");
		if (this.root.kind === "uniform") {
			const tag = tags[0] as EditorEolTag;
			if ((!this.root.count || this.root.tag === tag) && tags.every((value) => value === tag)) {
				this.root = { ...this.root, tag, count: this.root.count + tags.length };
				return;
			}
			this.root = {
				version: 1,
				kind: "mixed",
				prefixTag: this.root.tag,
				prefixCount: this.root.count,
				count: this.root.count,
				chunks: [],
			};
		}
		this.root = {
			...this.root,
			chunks: [...this.root.chunks, tags],
			count: this.root.count + tags.length,
		};
		if (this.root.count > 1_000_000) throw new Error("EDITOR_FORMAT_LIMIT");
	}
	finish(newlines: number) {
		if (newlines !== this.root.count) throw new Error("EDITOR_FORMAT_MISMATCH");
		return this.root;
	}
}
