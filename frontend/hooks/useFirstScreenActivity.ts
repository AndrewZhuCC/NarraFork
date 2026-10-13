import { useEffect, useMemo, useState } from "react";

export const FIRST_SCREEN_ACTIVITY_FALLBACK_MS = 1_500;

/** Only optional companion queries: never gate WS, bootstrap, permissions or execution targets. */
export function useFirstScreenActivity(
	narratorId: string,
	tailAdopted: boolean,
	fallbackMs = FIRST_SCREEN_ACTIVITY_FALLBACK_MS,
): boolean {
	const screen = useMemo(() => ({ narratorId }), [narratorId]);
	const [releasedScreen, setReleasedScreen] = useState<typeof screen | null>(null);
	useEffect(() => {
		if (!narratorId) return;
		if (tailAdopted) {
			setReleasedScreen(screen);
			return;
		}
		const timer = setTimeout(
			() => setReleasedScreen(screen),
			Math.min(5_000, Math.max(0, fallbackMs)),
		);
		return () => clearTimeout(timer);
	}, [screen, narratorId, tailAdopted, fallbackMs]);
	return !!narratorId && (tailAdopted || releasedScreen === screen);
}
