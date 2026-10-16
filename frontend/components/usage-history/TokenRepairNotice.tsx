import { useCurrentUser } from "@frontend/hooks/useAuth";
import { usageTokenRepairApi } from "@frontend/lib/usage-token-repair-api";
import { Alert, Button } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

export function TokenRepairNotice() {
	const { t } = useTranslation("common");
	const { data: user } = useCurrentUser();
	const isAdmin = user?.role === "admin";
	const { data } = useQuery({
		queryKey: ["usage-token-repair", "probe"],
		queryFn: ({ signal }) => usageTokenRepairApi.probe(signal),
		enabled: isAdmin,
		staleTime: 60_000,
	});
	if (!isAdmin) return null;
	return (
		<Alert title={t("tokenRepair.title")}>
			{t("tokenRepair.notice")}
			<Button component={Link} to="/settings/usage-repair" variant="light" mt="xs">
				{t(data?.job ? "tokenRepair.continue" : "tokenRepair.open")}
			</Button>
		</Alert>
	);
}
