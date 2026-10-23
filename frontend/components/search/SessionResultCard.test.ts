import { describe, expect, test } from "bun:test";
import { hitLinkTarget } from "./SessionResultCard";

describe("hitLinkTarget", () => {
	test("lands on the tool call's row for a hit inside one", () => {
		expect(hitLinkTarget({ id: "m1", snippet: "", toolUseId: "call-1" }, "giraffe")).toEqual({
			hash: "msg-call-1",
			search: { find: "giraffe" },
		});
	});

	test("lands on the message otherwise, with the longest term as the row needle", () => {
		expect(hitLinkTarget({ id: "m1", snippet: "" }, "ab refresh")).toEqual({
			hash: "msg-m1",
			search: { find: "refresh" },
		});
		expect(hitLinkTarget({ id: "m1", snippet: "" }, "  ")).toEqual({ hash: "msg-m1", search: {} });
	});
});
