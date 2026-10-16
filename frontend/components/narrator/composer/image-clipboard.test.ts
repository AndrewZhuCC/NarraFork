import { afterEach, describe, expect, mock, test } from "bun:test";
import { ApiError } from "../../../lib/api";
import {
	copyGeneratedImageToClipboard,
	ImageClipboardUnavailableError,
	isImageClipboardUnavailableError,
} from "./image-clipboard";

describe("copyGeneratedImageToClipboard", () => {
	const g = globalThis as typeof globalThis & {
		ClipboardItem?: unknown;
		navigator?: unknown;
		localStorage?: Storage;
		fetch: typeof fetch;
	};
	const descriptors = new Map(
		["fetch", "navigator", "ClipboardItem", "localStorage", "window", "document", "FileReader"].map(
			(key) => [key, Object.getOwnPropertyDescriptor(g, key)],
		),
	);

	afterEach(() => {
		mock.restore();
		for (const [key, descriptor] of descriptors) {
			if (descriptor) Object.defineProperty(g, key, descriptor);
			else Reflect.deleteProperty(g, key);
		}
	});

	test("constructs promised PNG and writes synchronously before fetching finishes", async () => {
		let finishFetch: ((response: Response) => void) | undefined;
		const pendingFetch = new Promise<Response>((resolve) => {
			finishFetch = resolve;
		});
		const items: Record<string, Blob | Promise<Blob>>[] = [];
		const write = mock(async () => {});
		Object.defineProperty(g, "window", { value: { isSecureContext: true }, configurable: true });
		Object.defineProperty(g, "navigator", { value: { clipboard: { write } }, configurable: true });
		Object.defineProperty(g, "ClipboardItem", {
			configurable: true,
			value: class {
				constructor(data: Record<string, Blob | Promise<Blob>>) {
					items.push(data);
				}
			},
		});
		Object.defineProperty(g, "fetch", { value: mock(() => pendingFetch), configurable: true });
		const copying = copyGeneratedImageToClipboard({ imageSrc: "blob:shown-image" });
		expect(items).toHaveLength(1);
		expect(Object.keys(items[0])).toEqual(["image/png"]);
		expect(items[0]["image/png"]).toBeInstanceOf(Promise);
		expect(write).toHaveBeenCalledTimes(1);
		const blob = new Blob(["png"], { type: "image/png" });
		finishFetch?.(new Response(blob));
		await copying;
		const result = await items[0]["image/png"];
		expect(result.type).toBe("image/png");
		expect(await result.text()).toBe("png");
	});

	test("capability guard still marks unavailability when the selection fallback cannot run", async () => {
		const fetch = mock(async () => new Response());
		Object.defineProperty(g, "fetch", { value: fetch, configurable: true });
		Object.defineProperty(g, "window", { value: { isSecureContext: false }, configurable: true });
		let error: unknown;
		try {
			await copyGeneratedImageToClipboard({ imageSrc: "blob:image" });
		} catch (caught) {
			error = caught;
		}
		expect(error).toBeInstanceOf(ImageClipboardUnavailableError);
		expect(isImageClipboardUnavailableError(error)).toBe(true);
		// Identity marking does not rely on a realm-specific Error prototype.
		Object.setPrototypeOf(error as object, null);
		expect(isImageClipboardUnavailableError(error)).toBe(true);
		expect(isImageClipboardUnavailableError(new Error("Image clipboard is not supported"))).toBe(
			false,
		);
		expect(isImageClipboardUnavailableError(new DOMException("Denied", "NotAllowedError"))).toBe(
			false,
		);
		// 选区接口不具备时不取字节，保持"先判能力、再动网络"的原语义。
		expect(fetch).not.toHaveBeenCalled();
	});

	test("selection fallback puts self-contained pixels in the selection", async () => {
		const execCommand = mock(() => true);
		const write = mock(async () => {});
		let assignedSource = "";
		const image = { src: "", onload: undefined as (() => void) | undefined };
		Object.defineProperty(image, "src", {
			// 让选中的图片同步"加载完成"，回退就不必依赖真实解码。
			set(value: string) {
				assignedSource = value;
				image.onload?.();
			},
			get: () => assignedSource,
		});
		const holder = {
			contentEditable: "",
			style: { cssText: "" },
			appendChild() {},
			remove() {},
		};
		Object.defineProperty(g, "document", {
			configurable: true,
			value: {
				execCommand,
				createElement: (tag: string) => (tag === "img" ? image : holder),
				createRange: () => ({ selectNodeContents() {} }),
				body: { appendChild() {} },
			},
		});
		Object.defineProperty(g, "window", {
			configurable: true,
			value: {
				isSecureContext: false,
				getSelection: () => ({ removeAllRanges() {}, addRange() {} }),
			},
		});
		Object.defineProperty(g, "FileReader", {
			configurable: true,
			value: class {
				result: string | null = null;
				onload: (() => void) | null = null;
				onerror: (() => void) | null = null;
				readAsDataURL(blob: Blob) {
					this.result = `data:${blob.type};base64,cG5n`;
					this.onload?.();
				}
			},
		});
		Object.defineProperty(g, "navigator", { value: { clipboard: { write } }, configurable: true });
		Object.defineProperty(g, "ClipboardItem", { value: class {}, configurable: true });
		Object.defineProperty(g, "fetch", {
			value: mock(async () => new Response(new Blob(["png"], { type: "image/png" }))),
			configurable: true,
		});

		await copyGeneratedImageToClipboard({ imageSrc: "blob:image" });

		expect(execCommand).toHaveBeenCalledWith("copy");
		expect(write).not.toHaveBeenCalled();
		// 选区里必须是自包含像素：粘出需鉴权的地址只会得到一张坏图。
		expect(assignedSource.startsWith("data:image/png;base64,")).toBe(true);
		expect(g.fetch).toHaveBeenCalledTimes(1);
	});

	function installSelectionDom(options: {
		isSecureContext: boolean;
		execCommand?: () => boolean;
		imageEvent?: "load" | "error";
	}) {
		let assignedSource = "";
		const image = {
			src: "",
			onload: undefined as (() => void) | undefined,
			onerror: undefined as (() => void) | undefined,
		};
		Object.defineProperty(image, "src", {
			set(value: string) {
				assignedSource = value;
				if (options.imageEvent === "error") image.onerror?.();
				else image.onload?.();
			},
			get: () => assignedSource,
		});
		const execCommand = mock(options.execCommand ?? (() => true));
		Object.defineProperty(g, "document", {
			configurable: true,
			value: {
				execCommand,
				createElement: (tag: string) =>
					tag === "img"
						? image
						: {
								contentEditable: "",
								style: { cssText: "" },
								appendChild() {},
								remove() {},
							},
				createRange: () => ({ selectNodeContents() {} }),
				body: { appendChild() {} },
			},
		});
		Object.defineProperty(g, "window", {
			configurable: true,
			value: {
				isSecureContext: options.isSecureContext,
				getSelection: () => ({ removeAllRanges() {}, addRange() {} }),
			},
		});
		Object.defineProperty(g, "FileReader", {
			configurable: true,
			value: class {
				result: string | null = null;
				onload: (() => void) | null = null;
				onerror: (() => void) | null = null;
				readAsDataURL(blob: Blob) {
					this.result = `data:${blob.type};base64,cG5n`;
					this.onload?.();
				}
			},
		});
		return { execCommand, readSource: () => assignedSource };
	}

	test("selection fallback does not copy a remote url when pixels cannot be read", async () => {
		const { execCommand } = installSelectionDom({ isSecureContext: false });
		Object.defineProperty(g, "fetch", {
			configurable: true,
			value: mock(async () => {
				throw new TypeError("Failed to fetch");
			}),
		});

		let error: unknown;
		try {
			await copyGeneratedImageToClipboard({ imageSrc: "https://images.example/photo.png" });
		} catch (caught) {
			error = caught;
		}

		expect(isImageClipboardUnavailableError(error)).toBe(true);
		expect(execCommand).not.toHaveBeenCalled();
	});

	test("an already inline data url can be selected when the byte fetch fails", async () => {
		const { execCommand, readSource } = installSelectionDom({ isSecureContext: false });
		Object.defineProperty(g, "fetch", {
			configurable: true,
			value: mock(async () => {
				throw new TypeError("Failed to fetch");
			}),
		});

		await copyGeneratedImageToClipboard({ imageSrc: "data:image/png;base64,cG5n" });

		expect(execCommand).toHaveBeenCalledWith("copy");
		expect(readSource()).toBe("data:image/png;base64,cG5n");
	});

	test("selection image load failure stays unavailable and does not copy", async () => {
		const { execCommand } = installSelectionDom({ isSecureContext: false, imageEvent: "error" });
		Object.defineProperty(g, "fetch", {
			configurable: true,
			value: mock(async () => new Response(new Blob(["png"], { type: "image/png" }))),
		});

		let error: unknown;
		try {
			await copyGeneratedImageToClipboard({ imageSrc: "blob:image" });
		} catch (caught) {
			error = caught;
		}

		expect(isImageClipboardUnavailableError(error)).toBe(true);
		expect(execCommand).not.toHaveBeenCalled();
	});

	test("preserves the actual clipboard rejection, not a capability error", async () => {
		const denied = new DOMException("Denied", "NotAllowedError");
		Object.defineProperty(g, "window", { value: { isSecureContext: true }, configurable: true });
		Object.defineProperty(g, "navigator", {
			value: {
				clipboard: {
					write: async () => {
						throw denied;
					},
				},
			},
			configurable: true,
		});
		Object.defineProperty(g, "ClipboardItem", { value: class {}, configurable: true });
		Object.defineProperty(g, "fetch", {
			value: async () => new Response(new Blob(["png"], { type: "image/png" })),
			configurable: true,
		});
		await expect(copyGeneratedImageToClipboard({ imageSrc: "blob:image" })).rejects.toBe(denied);
		expect(isImageClipboardUnavailableError(denied)).toBe(false);
	});

	test("a failed selection fallback still preserves the clipboard rejection", async () => {
		const denied = new DOMException("Denied", "NotAllowedError");
		const { execCommand } = installSelectionDom({
			isSecureContext: true,
			execCommand: () => false,
		});
		Object.defineProperty(g, "navigator", {
			configurable: true,
			value: {
				clipboard: {
					write: async () => {
						throw denied;
					},
				},
			},
		});
		Object.defineProperty(g, "ClipboardItem", { configurable: true, value: class {} });
		Object.defineProperty(g, "fetch", {
			configurable: true,
			value: async () => new Response(new Blob(["png"], { type: "image/png" })),
		});

		await expect(copyGeneratedImageToClipboard({ imageSrc: "blob:image" })).rejects.toBe(denied);
		expect(isImageClipboardUnavailableError(denied)).toBe(false);
		expect(execCommand).toHaveBeenCalledWith("copy");
	});

	test("a data-url selection copy can recover a clipboard rejection", async () => {
		const denied = new DOMException("Denied", "NotAllowedError");
		const { execCommand, readSource } = installSelectionDom({ isSecureContext: true });
		Object.defineProperty(g, "navigator", {
			configurable: true,
			value: {
				clipboard: {
					write: async () => {
						throw denied;
					},
				},
			},
		});
		Object.defineProperty(g, "ClipboardItem", { configurable: true, value: class {} });
		Object.defineProperty(g, "fetch", {
			configurable: true,
			value: async () => new Response(new Blob(["png"], { type: "image/png" })),
		});

		await copyGeneratedImageToClipboard({ imageSrc: "blob:image" });

		expect(execCommand).toHaveBeenCalledWith("copy");
		expect(readSource().startsWith("data:image/png;base64,")).toBe(true);
	});

	test("surfaces structured fs preview errors", async () => {
		Object.defineProperty(g, "navigator", {
			value: {
				clipboard: {
					write: async () => {},
				},
			},
			configurable: true,
		});
		Object.defineProperty(g, "localStorage", {
			value: {
				getItem: () => null,
				setItem: () => {},
				removeItem: () => {},
			},
			configurable: true,
		});
		Object.defineProperty(g, "ClipboardItem", {
			value: class ClipboardItem {},
			configurable: true,
		});
		Object.defineProperty(g, "fetch", {
			value: async () =>
				new Response(
					JSON.stringify({
						code: "FS_PREVIEW_TOO_LARGE",
						reason: "File too large to preview",
					}),
					{
						status: 413,
						statusText: "Payload Too Large",
						headers: { "content-type": "application/json" },
					},
				),
			configurable: true,
		});

		try {
			await copyGeneratedImageToClipboard({ savedPath: "/tmp/large.png" });
			throw new Error("expected clipboard image fetch to fail");
		} catch (err) {
			expect(err).toBeInstanceOf(ApiError);
			expect((err as ApiError).status).toBe(413);
			expect((err as Error).message).toBe("File too large to preview");
			expect((err as ApiError).data?.code).toBe("FS_PREVIEW_TOO_LARGE");
		}
	});
});
