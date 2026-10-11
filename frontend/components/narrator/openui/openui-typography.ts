import { defaultLightTheme, type Theme } from "@openuidev/react-ui";
import {
	FONT_WEIGHT,
	headingMetrics,
	MONO_FAMILY,
	SANS_FAMILY,
	typographyMetrics,
} from "@shared/pretext-layout/pretext-fonts";
import { letterSpacingPxFor } from "@shared/pretext-layout/typography";

/** Anchor the SDK's 16px body to the same metrics that NF measures and paints. */
export function openUITypographyTheme(metrics = typographyMetrics()): Theme {
	const bodyRatio = metrics.line.body / metrics.size.sm;
	const codeRatio = metrics.line.code / metrics.codeSize;
	const heading = headingMetrics(4);
	const largeHeading = headingMetrics(1);
	const spacing = `${metrics.letterSpacing.body / metrics.size.sm}em`;
	const theme: Record<string, string> = {
		fontBody: SANS_FAMILY,
		fontHeading: SANS_FAMILY,
		fontLabel: SANS_FAMILY,
		fontNumbers: SANS_FAMILY,
		fontCode: MONO_FAMILY,
		fontWeightRegular: String(FONT_WEIGHT.regular),
		fontWeightMedium: String(FONT_WEIGHT.medium),
		fontWeightBold: String(FONT_WEIGHT.bold),
		fontWeightHeavy: String(FONT_WEIGHT.bold),
		lineHeightBody: String(bodyRatio),
		lineHeightLabel: String(bodyRatio),
		lineHeightCode: String(codeRatio),
		lineHeightHeading: String(heading.lineHeight / heading.size),
		lineHeightHeadingLarge: String(largeHeading.lineHeight / largeHeading.size),
		letterSpacingNormal: spacing,
		letterSpacingTight: spacing,
		letterSpacingTighter: spacing,
	};
	const headingLevels = { Xs: 5, Sm: 4, Md: 3, Lg: 2, Xl: 1 } as const;
	for (const [key, value] of Object.entries(defaultLightTheme)) {
		if (typeof value !== "string") continue;
		if (key.startsWith("fontSize")) {
			theme[key] = `${(Number.parseFloat(value) * metrics.size.sm) / 16}px`;
			continue;
		}
		// Components consume compound font tokens directly, not just fontSizeMd.
		if (!/^text(?:Body|Label|Numbers|Heading|Code)/.test(key)) continue;
		const font = /^(\d+) (\d+(?:\.\d+)?)px\/[\d.]+ .+$/.exec(value);
		if (!font) continue;
		const code = key.startsWith("textCode");
		let size = (Number(font[2]) * (code ? metrics.codeSize : metrics.size.sm)) / (code ? 14 : 16);
		let line = Math.round(size * (code ? codeRatio : bodyRatio));
		let weight = Number(font[1]) >= 700 ? 700 : Number(font[1]) >= 500 ? 600 : 400;
		const level = /Heading(Xs|Sm|Md|Lg|Xl)$/.exec(key)?.[1];
		if (level) {
			const h = headingMetrics(headingLevels[level as keyof typeof headingLevels]);
			size = h.size;
			line = h.lineHeight;
			weight = FONT_WEIGHT.bold;
		}
		theme[key] = `${weight} ${size}px/${line}px ${code ? MONO_FAMILY : SANS_FAMILY}`;
		theme[`${key}LetterSpacing`] = `${letterSpacingPxFor(size)}px`;
	}
	return theme;
}
