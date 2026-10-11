import { afterEach, expect, test } from "bun:test";
import {
	headingMetrics,
	MONO_FAMILY,
	SANS_FAMILY,
	typographyMetrics,
} from "@shared/pretext-layout/pretext-fonts";
import {
	DEFAULT_TYPOGRAPHY,
	getTypography,
	setTypography,
} from "@shared/pretext-layout/typography";
import { openUITypographyTheme } from "./openui-typography";

const original = getTypography();
afterEach(() => setTypography(original));

test.each([70, 100, 130, 180])("OpenUI body matches NF at %i percent", (fontScalePercent) => {
	setTypography({ ...DEFAULT_TYPOGRAPHY, fontScalePercent });
	const metrics = typographyMetrics();
	const theme = openUITypographyTheme();
	expect(theme.textBodyDefault).toBe(
		`400 ${metrics.size.sm}px/${metrics.line.body}px ${SANS_FAMILY}`,
	);
	expect(theme.fontSizeMd).toBe(`${metrics.size.sm}px`);
	expect(theme.fontBody).toBe(SANS_FAMILY);
	expect(theme.textLabelDefault).toBe(theme.textBodyDefault);
	expect(theme.textNumbersDefault).toBe(theme.textBodyDefault);
});

test("heading, code, line height and letter spacing follow NF typography", () => {
	setTypography({
		...DEFAULT_TYPOGRAPHY,
		fontScalePercent: 125,
		lineHeightScalePercent: 140,
		letterSpacingPercent: 3,
	});
	const metrics = typographyMetrics();
	const heading = headingMetrics(3);
	const theme = openUITypographyTheme();
	expect(theme.textBodyDefault).toBe(`400 17.5px/${metrics.line.body}px ${SANS_FAMILY}`);
	expect(theme.textBodyDefaultLetterSpacing).toBe(`${metrics.letterSpacing.body}px`);
	expect(theme.textHeadingMd).toBe(`700 ${heading.size}px/${heading.lineHeight}px ${SANS_FAMILY}`);
	expect(theme.textHeadingMdLetterSpacing).toBe(`${heading.letterSpacing}px`);
	expect(theme.textCodeDefault).toBe(
		`400 ${metrics.codeSize}px/${metrics.line.code}px ${MONO_FAMILY}`,
	);
	expect(theme.textCodeDefaultLetterSpacing).toBe(`${metrics.letterSpacing.code}px`);
});
