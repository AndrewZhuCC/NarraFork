import { Select } from "@mantine/core";
import { LOCALE_OPTIONS } from "@shared/i18n-locales";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useUpdateUserPreferences } from "../hooks/useUserPreferences";
import { changeAppLanguage, getNamespacesForPath, normalizeLanguage } from "../lib/i18n";

/**
 * `persistToAccount` saves the choice to the signed-in user's preferences. The login
 * page has no session, so it only switches the UI (the detector caches it locally,
 * and registration sends it along as the new account's language).
 */
export function LanguageSwitcher({ persistToAccount = true }: { persistToAccount?: boolean }) {
	const { i18n } = useTranslation();
	const updatePrefs = useUpdateUserPreferences();
	const [isChangingLanguage, setIsChangingLanguage] = useState(false);
	const currentLanguage = normalizeLanguage(i18n.resolvedLanguage ?? i18n.language);

	const handleChange = (value: string | null) => {
		if (!value) return;
		const locale = normalizeLanguage(value);
		if (locale === currentLanguage) return;

		setIsChangingLanguage(true);
		void (async () => {
			try {
				await changeAppLanguage(locale, getNamespacesForPath(window.location.pathname));
				if (persistToAccount) updatePrefs.mutate({ language: locale });
			} finally {
				setIsChangingLanguage(false);
			}
		})();
	};

	return (
		<Select
			data={LOCALE_OPTIONS}
			value={currentLanguage}
			onChange={handleChange}
			size="sm"
			w={120}
			allowDeselect={false}
			disabled={isChangingLanguage}
		/>
	);
}
