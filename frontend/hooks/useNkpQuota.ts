import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";

export interface NkpQuotaData {
	quotaBalance: number;
	quotaTotalGranted: number;
}

export function useNkpQuota(providerId: string | undefined) {
	return useQuery<NkpQuotaData>({
		queryKey: ["nkp", "quota", providerId],
		queryFn: () => api.getNkpProviderQuota(providerId!),
		enabled: !!providerId,
		staleTime: 60_000,
		refetchOnWindowFocus: false,
	});
}
