/**
 * svg-sanitize.ts — Make an untrusted workspace `.svg` safe to render on the app origin.
 *
 * ## Why this is needed
 *
 * `/api/fs/preview` serves arbitrary workspace files, and the viewer renders an `.svg`
 * either directly (navigating to the preview URL) or through a SAME-ORIGIN blob URL the
 * frontend builds from the bytes. An SVG is an active document: `<script>`, `on*` event
 * handlers, `javascript:` URLs and `<foreignObject>` (which embeds arbitrary XHTML) all
 * execute when the SVG is loaded as a document on NarraFork's own origin — i.e. with the
 * user's session. A response-level CSP cannot help the blob case (a blob URL does not
 * inherit the response's headers), so the only reliable defense is to sanitize the BYTES
 * before they ever reach the browser.
 *
 * ## Approach: allow-list, not deny-list
 *
 * `sanitize-html` (already a dependency; see `share-preview-worker.ts`) parses the markup
 * and keeps ONLY the elements and attributes named below. Everything else is dropped, so
 * `<script>`, `<foreignObject>`, `<iframe>`, and every `on*`/unknown attribute disappear
 * without having to be enumerated — a new SVG-borne vector cannot slip through a hole in a
 * deny-list because there is no deny-list. URL attributes (`href`/`xlink:href`/`src`) are
 * additionally restricted to safe schemes, which strips `javascript:` and `data:`.
 *
 * ⚠️ `lowerCaseTags`/`lowerCaseAttributeNames` MUST stay false: SVG is case-sensitive, and
 * lower-casing would corrupt `viewBox`, `preserveAspectRatio`, `gradientUnits`,
 * `<linearGradient>`, `<feGaussianBlur>` and the like — turning sanitization into a silent
 * rendering break. The default HTML lower-casing is exactly wrong here.
 *
 * ## What is deliberately NOT included
 *
 * Animation elements (`<animate>`, `<set>`, …) are omitted on purpose: `<set
 * attributeName="xlink:href" to="javascript:…">` is a known SVG XSS vector, and animations
 * are not part of the "paths, shapes, styles" a preview needs. `<style>` IS kept (class-based
 * styling is common in exported SVGs) despite sanitize-html's standing warning about it: CSS
 * in a standalone SVG cannot execute script in any current browser, and every element that
 * COULD run code is already removed, so the residual risk is confined to CSS-level resource
 * loading. `allowVulnerableTags` only silences the warning for that considered decision.
 */

import sanitizeHtml from "sanitize-html";

/** Static SVG elements a preview may render. No `script`/`foreignObject`/animation. */
const SVG_TAGS: readonly string[] = [
	"svg",
	"g",
	"defs",
	"symbol",
	"use",
	"switch",
	"title",
	"desc",
	"metadata",
	"style",
	"view",
	// Shapes / paths / text
	"path",
	"rect",
	"circle",
	"ellipse",
	"line",
	"polyline",
	"polygon",
	"text",
	"tspan",
	"textPath",
	"tref",
	// Paint servers / clipping / markers
	"linearGradient",
	"radialGradient",
	"stop",
	"pattern",
	"image",
	"clipPath",
	"mask",
	"marker",
	"a",
	// Filters
	"filter",
	"feBlend",
	"feColorMatrix",
	"feComponentTransfer",
	"feComposite",
	"feConvolveMatrix",
	"feDiffuseLighting",
	"feDisplacementMap",
	"feDistantLight",
	"feDropShadow",
	"feFlood",
	"feFuncA",
	"feFuncB",
	"feFuncG",
	"feFuncR",
	"feGaussianBlur",
	"feImage",
	"feMerge",
	"feMergeNode",
	"feMorphology",
	"feOffset",
	"fePointLight",
	"feSpecularLighting",
	"feSpotLight",
	"feTile",
	"feTurbulence",
];

/**
 * Attributes any SVG element may carry. Geometry, presentation, gradient/filter plumbing
 * and the two URL attributes (scheme-restricted below). `on*` handlers are absent, so they
 * are dropped. `style` (inline) is allowed; its value is not deep-parsed here, but inline
 * CSS cannot execute script in a standalone SVG.
 */
const SVG_ATTRS: readonly string[] = [
	"id",
	"class",
	"style",
	"lang",
	"transform",
	"transform-origin",
	"x",
	"y",
	"x1",
	"y1",
	"x2",
	"y2",
	"cx",
	"cy",
	"r",
	"rx",
	"ry",
	"width",
	"height",
	"d",
	"points",
	"dx",
	"dy",
	"rotate",
	"viewBox",
	"preserveAspectRatio",
	"xmlns",
	"xmlns:xlink",
	"version",
	"baseProfile",
	"href",
	"xlink:href",
	"xlink:title",
	"xml:space",
	"fill",
	"fill-opacity",
	"fill-rule",
	"stroke",
	"stroke-width",
	"stroke-linecap",
	"stroke-linejoin",
	"stroke-dasharray",
	"stroke-dashoffset",
	"stroke-opacity",
	"stroke-miterlimit",
	"opacity",
	"color",
	"stop-color",
	"stop-opacity",
	"offset",
	"gradientUnits",
	"gradientTransform",
	"spreadMethod",
	"patternUnits",
	"patternContentUnits",
	"patternTransform",
	"clip-path",
	"clip-rule",
	"clipPathUnits",
	"mask",
	"maskUnits",
	"maskContentUnits",
	"marker-start",
	"marker-mid",
	"marker-end",
	"markerWidth",
	"markerHeight",
	"markerUnits",
	"orient",
	"refX",
	"refY",
	"font-family",
	"font-size",
	"font-weight",
	"font-style",
	"text-anchor",
	"dominant-baseline",
	"alignment-baseline",
	"letter-spacing",
	"word-spacing",
	"filter",
	"filterUnits",
	"primitiveUnits",
	"result",
	"in",
	"in2",
	"stdDeviation",
	"flood-color",
	"flood-opacity",
	"lighting-color",
	"values",
	"type",
	"mode",
	"operator",
	"scale",
	"k1",
	"k2",
	"k3",
	"k4",
	"radius",
	"baseFrequency",
	"numOctaves",
	"seed",
	"stitchTiles",
	"startOffset",
	"vector-effect",
	"shape-rendering",
	"color-interpolation-filters",
	"overflow",
	"display",
	"visibility",
];

/**
 * Remove any `<!DOCTYPE …>` before parsing, internal subset (`[ … ]`) included.
 *
 * SVG needs no DOCTYPE, and the internal subset is where `<!ENTITY … SYSTEM "file://…">`
 * (external-entity / billion-laughs) declarations live. sanitize-html drops the directive
 * itself but mis-terminates at the first `>` inside the subset, leaving a stray `]>` as
 * visible text — stripping it here removes both the risk and that artifact. The `<?xml?>`
 * prolog is left to the parser, which discards it cleanly.
 */
function stripDoctype(svg: string): string {
	return svg.replace(/<!DOCTYPE[^>[]*(?:\[[\s\S]*?\])?[^>]*>/gi, "");
}

/**
 * Return a sanitized copy of `svg` safe to serve as `image/svg+xml`.
 *
 * Synchronous and bounded: callers must cap the input size (sanitize-html runs on the
 * server's single JS thread — see the main-thread rules in CLAUDE.md).
 */
export function sanitizeSvg(svg: string): string {
	return sanitizeHtml(stripDoctype(svg), {
		allowedTags: SVG_TAGS as string[],
		allowedAttributes: { "*": SVG_ATTRS as string[] },
		// Scheme allow-list applied to the URL-bearing attributes, so `javascript:` and
		// `data:` (which can smuggle an executable document) are stripped from them.
		allowedSchemes: ["http", "https", "mailto", "tel"],
		allowedSchemesAppliedToAttributes: ["href", "xlink:href", "src"],
		allowProtocolRelative: false,
		// `script` stays in nonTextTags so its body is discarded, not re-emitted as text;
		// `style` is intentionally NOT here, so legitimate CSS survives.
		nonTextTags: ["script", "textarea", "option", "noscript"],
		// SVG is case-sensitive — see the module header. This is load-bearing.
		parser: { lowerCaseTags: false, lowerCaseAttributeNames: false },
		// `<style>` is kept by deliberate decision (see header); this acknowledges the
		// standing warning rather than dropping class-based styling from every SVG.
		allowVulnerableTags: true,
	});
}
