import type { EditorRawEncoding } from "../shared/editor-document";
import { isCompiledRuntime } from "./lib/runtime-target";
import { EditorDocumentStreams } from "./services/editor-document-streams";

if (!isCompiledRuntime()) throw new Error("This fixture must run as a compiled binary");
const path = process.argv[2];
if (!path) throw new Error("Fixture source required");
const streams = new EditorDocumentStreams();
try {
	const metadata = await streams.metadata(
		"smoke",
		path,
		process.argv[3] as EditorRawEncoding | undefined,
	);
	const source = await streams.open("smoke", "document", path, undefined, metadata);
	let bytes = 0;
	for (;;) {
		const chunk = await streams.chunk("document", source.readId, bytes, 65536);
		bytes += chunk.bytes?.length ?? 0;
		if (chunk.eof) break;
	}
	if (bytes !== metadata.sourceBytes) throw new Error("Compiled stream byte mismatch");
	await streams.close("document", source.readId);
	const counters = await streams.workers.counters();
	if (counters.some((value) => value?.fds)) throw new Error("Compiled source FD leak");
	console.info(
		JSON.stringify({
			compiled: true,
			bytes,
			hashKind: metadata.hashKind,
			encoding: metadata.encoding,
			counters,
			admission: streams.diagnostics,
		}),
	);
} finally {
	await streams.dispose();
}
