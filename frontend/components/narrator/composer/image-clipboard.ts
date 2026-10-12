import { ApiError, authorizedFetch, readFetchError } from "../../../lib/api";
import { apiUrl, isApiUrl, resolveServerUrl } from "../../../lib/base-path";

export const MAX_INLINE_IMAGE_SOURCE_CHARS = 16 * 1024 * 1024;
export const MAX_IMAGE_CLIPBOARD_BLOB_BYTES = 25 * 1024 * 1024;
const MAX_IMAGE_CLIPBOARD_PIXELS = 32_000_000;

export interface CopyGeneratedImageOptions {
	imageSrc?: string | null;
	savedPath?: string | null;
}

function assertInlineImageSourceSafe(source: string): void {
	if (source.length > MAX_INLINE_IMAGE_SOURCE_CHARS) {
		throw new Error("Image data too large");
	}
	if (source.startsWith("data:") && !source.startsWith("data:image/")) {
		throw new Error("Unsupported image data URL");
	}
}

function isRelativeImageUrl(source: string): boolean {
	return isApiUrl(source) || /\.(?:png|jpe?g|gif|webp|avif|svg)(?:[?#].*)?$/i.test(source);
}

function normalizeImageSrc(imageSrc?: string | null): string | null {
	const source = imageSrc?.trim();
	if (!source) return null;
	if (
		source.startsWith("data:") ||
		source.startsWith("blob:") ||
		source.startsWith("http://") ||
		source.startsWith("https://") ||
		isRelativeImageUrl(source)
	) {
		if (source.startsWith("data:")) assertInlineImageSourceSafe(source);
		// A rooted `/api/…` here is a server-minted, PERSISTED value (a screenshot's
		// `previewUrl`), so it must be re-pointed at the mount prefix before it is fetched.
		// Unlike the `<img>` path there is no fallback chain to rescue it: copy and download
		// simply fail, and under a prefix the proxy answers with HTML, so the failure reads
		// as a corrupt image rather than a wrong URL.
		return resolveServerUrl(source);
	}
	assertInlineImageSourceSafe(source);
	return `data:image/png;base64,${source}`;
}

function getSavedPathPreviewSource(savedPath?: string | null): string | null {
	const path = savedPath?.trim();
	if (!path) return null;
	return `${apiUrl("/fs/preview")}?path=${encodeURIComponent(path)}`;
}

const capabilityErrors = new WeakSet<object>();

export class ImageClipboardUnavailableError extends Error {
	constructor() {
		super("Image clipboard is not supported");
		this.name = "ImageClipboardUnavailableError";
		capabilityErrors.add(this);
	}
}

/** Match only our capability guard, not permission errors or cross-realm Error prototypes. */
export function isImageClipboardUnavailableError(
	error: unknown,
): error is ImageClipboardUnavailableError {
	return typeof error === "object" && error !== null && capabilityErrors.has(error);
}

function assertBlobSafe(blob: Blob): void {
	if (blob.size > MAX_IMAGE_CLIPBOARD_BLOB_BYTES) {
		throw new Error("Image blob too large");
	}
}

export async function fetchImageBlob({
	imageSrc,
	savedPath,
}: CopyGeneratedImageOptions): Promise<Blob> {
	const savedPathSource = getSavedPathPreviewSource(savedPath);
	const source = savedPathSource ?? normalizeImageSrc(imageSrc);
	if (!source) throw new Error("No image source");
	const needsAuth = savedPathSource != null || isApiUrl(source);

	const response = needsAuth ? await authorizedFetch(source) : await fetch(source);
	if (!response.ok) {
		const error = await readFetchError(response, `HTTP ${response.status}`);
		throw new ApiError(error.message, response.status, error.data);
	}
	const blob = await response.blob();
	assertBlobSafe(blob);
	if (blob.type.startsWith("image/")) return blob;
	return new Blob([blob], { type: "image/png" });
}

async function imageBlobToPng(blob: Blob): Promise<Blob> {
	const objectUrl = URL.createObjectURL(blob);
	try {
		const image = await new Promise<HTMLImageElement>((resolve, reject) => {
			const img = new Image();
			img.onload = () => resolve(img);
			img.onerror = () => reject(new Error("Failed to load image"));
			img.src = objectUrl;
		});
		const pixels = image.naturalWidth * image.naturalHeight;
		if (!Number.isFinite(pixels) || pixels <= 0 || pixels > MAX_IMAGE_CLIPBOARD_PIXELS) {
			throw new Error("Image dimensions too large");
		}
		const canvas = document.createElement("canvas");
		canvas.width = image.naturalWidth;
		canvas.height = image.naturalHeight;
		const ctx = canvas.getContext("2d");
		if (!ctx) throw new Error("Canvas is not supported");
		ctx.drawImage(image, 0, 0);
		const pngBlob = await new Promise<Blob>((resolve, reject) => {
			canvas.toBlob((blob) => {
				if (blob) resolve(blob);
				else reject(new Error("Failed to encode image"));
			}, "image/png");
		});
		assertBlobSafe(pngBlob);
		return pngBlob;
	} finally {
		URL.revokeObjectURL(objectUrl);
	}
}

/** Binary image copy needs ClipboardItem; plain HTTP and old browsers cannot fall back to execCommand. */
export function canUseImageClipboard(): boolean {
	return (
		typeof navigator !== "undefined" &&
		typeof navigator.clipboard?.write === "function" &&
		typeof ClipboardItem !== "undefined" &&
		(typeof window === "undefined" || window.isSecureContext !== false)
	);
}

/** 选区回退所需的老接口：execCommand、Selection 与 Range 缺一不可。 */
function canUseSelectionClipboard(): boolean {
	return (
		typeof document !== "undefined" &&
		typeof document.execCommand === "function" &&
		typeof window?.getSelection === "function" &&
		typeof document.createRange === "function"
	);
}

/** 选区复制最多等图片加载多久：超时按失败上报，不能静默卡住。 */
const SELECTION_IMAGE_LOAD_TIMEOUT_MS = 5000;

/**
 * 选区复制回退：异步剪贴板缺失或被拒时仍可能成功复制图片。
 *
 * `execCommand("copy")` 走的是"复制当前选区"这条老路径，只要求文档持有选区，
 * 因此非安全上下文（局域网 HTTP）、权限被拒、浏览器不支持图片写入这三种情况
 * 都能覆盖；代价是需要把图片渲染到视口外并等它加载完。
 */
async function copyViaSelection(imageUrl: string): Promise<void> {
	const holder = document.createElement("div");
	holder.contentEditable = "true";
	// 必须真实渲染才能被选中，所以移出视口而不是隐藏。
	holder.style.cssText = "position:fixed;left:-10000px;top:0;";
	const image = document.createElement("img");
	const loaded = new Promise<void>((resolve, reject) => {
		const timer = setTimeout(
			() => reject(new Error("Timed out loading image")),
			SELECTION_IMAGE_LOAD_TIMEOUT_MS,
		);
		image.onload = () => {
			clearTimeout(timer);
			resolve();
		};
		image.onerror = () => {
			clearTimeout(timer);
			reject(new Error("Failed to load image"));
		};
	});
	image.src = imageUrl;
	holder.appendChild(image);
	document.body.appendChild(holder);

	const selection = window.getSelection();
	try {
		await loaded;
		const range = document.createRange();
		range.selectNodeContents(holder);
		selection?.removeAllRanges();
		selection?.addRange(range);
		if (!document.execCommand("copy")) throw new ImageClipboardUnavailableError();
	} finally {
		selection?.removeAllRanges();
		holder.remove();
	}
}

/** 把像素读成 data: URL；环境不给读就返回 null，由调用方退回原地址。 */
function readDataUrl(blob: Blob): Promise<string | null> {
	if (typeof FileReader !== "function") return Promise.resolve(null);
	return new Promise((resolve) => {
		const reader = new FileReader();
		reader.onload = () => resolve(typeof reader.result === "string" ? reader.result : null);
		reader.onerror = () => resolve(null);
		reader.readAsDataURL(blob);
	});
}

/**
 * 选区里的图片地址必须自包含：粘贴目标取不到需要鉴权的地址，只会粘出一张坏图。
 * 所以优先取字节转 data:（能转 PNG 就转，转不动就用原始字节），失败才退回显示地址。
 */
async function buildSelectionImageSource(
	options: CopyGeneratedImageOptions,
): Promise<string | null> {
	try {
		const blob = await fetchImageBlob(options);
		const png = blob.type === "image/png" ? blob : await imageBlobToPng(blob).catch(() => blob);
		const dataUrl = await readDataUrl(png);
		if (dataUrl) return dataUrl;
	} catch {
		// 取字节失败时退回页面已在显示的地址。
	}
	return normalizeImageSrc(options.imageSrc);
}

async function copyWithSelection(options: CopyGeneratedImageOptions): Promise<void> {
	// 平台连选区接口都没有时先报不可用，避免白白取一次字节。
	if (!canUseSelectionClipboard()) throw new ImageClipboardUnavailableError();
	const source = await buildSelectionImageSource(options);
	if (!source) throw new ImageClipboardUnavailableError();
	await copyViaSelection(source);
}

export async function copyGeneratedImageToClipboard(
	options: CopyGeneratedImageOptions,
): Promise<void> {
	if (!canUseImageClipboard()) {
		// 非安全上下文（局域网 HTTP）下浏览器不提供异步剪贴板，只能走选区复制；
		// 平台连选区接口都不具备时它按不可用上报，调用方保留降级为下载的原语义。
		await copyWithSelection(options);
		return;
	}

	const imageBlob = fetchImageBlob(options);
	// 下面的分支可能先返回，先接住 rejection 以免留下未处理的 promise。
	void imageBlob.catch(() => {});

	const clipboardBlob = imageBlob.then(async (blob) => {
		const png = blob.type === "image/png" ? blob : await imageBlobToPng(blob);
		assertBlobSafe(png);
		return png;
	});
	// write/ClipboardItem must run before any await to preserve Safari's user activation.
	// The browser consumes the original promise; also handle its rejection if write
	// or the constructor fails before consuming it (e.g. permission was denied).
	void clipboardBlob.catch(() => {});
	try {
		await Promise.all([
			navigator.clipboard.write([new ClipboardItem({ "image/png": clipboardBlob })]),
			clipboardBlob,
		]);
	} catch (error) {
		// 权限被拒、激活过期或该浏览器不支持图片写入时，选区回退仍可能成功；
		// 回退也失败（含平台没有选区接口）则抛出原错误，交由调用方按原语义反馈。
		try {
			await copyWithSelection(options);
		} catch {
			throw error;
		}
	}
}
