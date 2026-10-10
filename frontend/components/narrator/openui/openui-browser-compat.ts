type UUIDCrypto = Pick<Crypto, "getRandomValues"> & Partial<Pick<Crypto, "randomUUID">>;

/** OpenUI uses UUIDs for citation DOM IDs even on plain-HTTP LAN pages. */
export function installOpenUIUUIDFallback(target: UUIDCrypto | undefined): void {
	if (!target || typeof target.randomUUID === "function" || !target.getRandomValues) return;
	// ponytail: keep this vendor shim local to OpenUI loading; remove it when the
	// SDK uses a DOM ID helper instead. Never substitute Math.random for Web Crypto.
	Object.defineProperty(target, "randomUUID", {
		configurable: true,
		writable: true,
		value: () => {
			const bytes = target.getRandomValues(new Uint8Array(16));
			bytes[6] = (bytes[6] & 0x0f) | 0x40;
			bytes[8] = (bytes[8] & 0x3f) | 0x80;
			const hex = Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0")).join("");
			return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
		},
	});
}

if (typeof window !== "undefined") installOpenUIUUIDFallback(globalThis.crypto);
