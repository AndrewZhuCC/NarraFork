import { afterEach, describe, expect, it } from "bun:test";
import { eventBus, type NarraForkEvent } from "../../../server/lib/event-bus";

describe("eventBus", () => {
	const handlers: Array<() => void> = [];

	afterEach(() => {
		for (const cleanup of handlers) cleanup();
		handlers.length = 0;
	});

	it("delivers typed events to subscribers", () => {
		const received: NarraForkEvent[] = [];
		const handler = (e: NarraForkEvent) => received.push(e);
		// biome-ignore lint/suspicious/noExplicitAny: test utility cast
		eventBus.on("terminal:created", handler as any);
		// biome-ignore lint/suspicious/noExplicitAny: test utility cast
		handlers.push(() => eventBus.off("terminal:created", handler as any));

		eventBus.emit({ type: "terminal:created", terminalId: "t1", narratorId: "n1" });
		expect(received).toHaveLength(1);
		expect(received[0].type).toBe("terminal:created");
	});

	it("does not deliver events of other types", () => {
		const received: NarraForkEvent[] = [];
		const handler = (e: NarraForkEvent) => received.push(e);
		// biome-ignore lint/suspicious/noExplicitAny: test utility cast
		eventBus.on("terminal:exited", handler as any);
		// biome-ignore lint/suspicious/noExplicitAny: test utility cast
		handlers.push(() => eventBus.off("terminal:exited", handler as any));

		eventBus.emit({ type: "terminal:created", terminalId: "t1", narratorId: "n1" });
		expect(received).toHaveLength(0);
	});

	it("onAny receives all events", () => {
		const received: NarraForkEvent[] = [];
		const handler = (e: NarraForkEvent) => received.push(e);
		eventBus.onAny(handler);
		handlers.push(() => eventBus.offAny(handler));

		eventBus.emit({ type: "terminal:created", terminalId: "t1", narratorId: "n1" });
		eventBus.emit({ type: "narrator:status_changed", narratorId: "n1", status: "idle" });
		expect(received).toHaveLength(2);
	});
});
