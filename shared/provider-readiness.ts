/** Public, credential-free projection of an actual provider registration. */
export interface ProviderReadiness {
	id: string;
	prefix: string;
	name: string;
	ready: boolean;
}
