import { describe, expect, it } from "bun:test";
import { sanitizeSvg } from "../svg-sanitize";

describe("sanitizeSvg — removes executable content", () => {
	it("strips <script>, event handlers and javascript:/data: URLs", () => {
		const out = sanitizeSvg(
			`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)">
				<script>alert('xss')</script>
				<rect onclick="evil()" width="10" height="10"/>
				<a xlink:href="javascript:alert(2)"><text>hi</text></a>
				<image href="data:text/html,<script>1</script>" width="5" height="5"/>
			</svg>`,
		);
		expect(out).not.toContain("<script");
		expect(out).not.toMatch(/onload/i);
		expect(out).not.toMatch(/onclick/i);
		expect(out).not.toContain("javascript:");
		expect(out).not.toContain("alert");
		expect(out).not.toContain("data:text");
	});

	it("removes <foreignObject> and its embedded HTML even after a <style> element", () => {
		// A <style> before <foreignObject> was where a streaming rewriter silently gave up;
		// an allow-list parser does not, so this stays covered.
		const out = sanitizeSvg(
			`<svg xmlns="http://www.w3.org/2000/svg">
				<style>.a{fill:red}</style>
				<foreignObject><body><iframe src="x"></iframe><script>bad()</script></body></foreignObject>
			</svg>`,
		);
		expect(out.toLowerCase()).not.toContain("foreignobject");
		expect(out).not.toContain("<iframe");
		expect(out).not.toContain("<script");
	});

	it("drops a DOCTYPE with an external-entity declaration, leaving no stray text", () => {
		const out = sanitizeSvg(
			`<?xml version="1.0"?>
			<!DOCTYPE svg [ <!ENTITY xxe SYSTEM "file:///etc/passwd"> ]>
			<svg xmlns="http://www.w3.org/2000/svg"><rect width="1" height="1"/></svg>`,
		);
		expect(out).not.toContain("ENTITY");
		expect(out).not.toContain("DOCTYPE");
		expect(out).not.toContain("]>");
	});

	it("omits animation elements (the <set>/<animate> href vector)", () => {
		const out = sanitizeSvg(
			`<svg xmlns="http://www.w3.org/2000/svg"><a><set attributeName="xlink:href" to="javascript:alert(1)"/></a></svg>`,
		);
		expect(out.toLowerCase()).not.toContain("<set");
		expect(out).not.toContain("javascript:");
	});
});

describe("sanitizeSvg — preserves legitimate rendering", () => {
	it("keeps case-sensitive geometry, gradients, filters, styles and internal refs", () => {
		const out = sanitizeSvg(
			`<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" viewBox="0 0 200 100" preserveAspectRatio="xMidYMid meet">
				<style>.title{font-weight:700}</style>
				<defs>
					<linearGradient id="g" gradientUnits="userSpaceOnUse" x1="0" y1="0" x2="200" y2="0">
						<stop offset="0%" stop-color="red"/><stop offset="100%" stop-color="blue"/>
					</linearGradient>
					<filter id="blur"><feGaussianBlur stdDeviation="2"/></filter>
				</defs>
				<rect width="200" height="100" fill="url(#g)" filter="url(#blur)"/>
				<path d="M10 10 L90 90 Z" stroke="black" stroke-width="2" fill="none"/>
				<circle cx="50" cy="50" r="20"/>
				<text class="title" x="10" y="20" font-family="sans-serif">Hello</text>
				<use xlink:href="#g"/>
				<a href="https://ok.example">ok</a>
			</svg>`,
		);
		// Case preserved — lower-casing these would break SVG rendering.
		expect(out).toContain('viewBox="0 0 200 100"');
		expect(out).toContain('preserveAspectRatio="xMidYMid meet"');
		expect(out).toContain("<linearGradient");
		expect(out).toContain('gradientUnits="userSpaceOnUse"');
		expect(out).toContain("<feGaussianBlur");
		expect(out).toContain('stdDeviation="2"');
		// Paint / paths / styles / internal references intact.
		expect(out).toContain("url(#g)");
		expect(out).toContain("url(#blur)");
		expect(out).toContain('d="M10 10 L90 90 Z"');
		expect(out).toContain(".title{font-weight:700}");
		expect(out).toContain('xlink:href="#g"');
		expect(out).toContain('href="https://ok.example"');
	});
});
