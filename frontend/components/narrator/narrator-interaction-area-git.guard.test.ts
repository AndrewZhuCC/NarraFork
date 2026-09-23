import { expect, test } from "bun:test";

test("chapter interactions bind Git polling to the narrator opt-in", async () => {
	const source = await Bun.file(new URL("./NarratorInteractionArea.tsx", import.meta.url)).text();
	const chapterBar = source.match(/<ChapterBar\s+[\s\S]*?\/>/)?.[0] ?? "";

	expect(chapterBar).toContain("chapterId={props.chapterId}");
	expect(chapterBar).toContain("narratorId={props.common.narratorId}");
});
