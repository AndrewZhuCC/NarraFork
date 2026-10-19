import { expect, test } from "bun:test";
import {
	CURRENT_RECOVERY_MANIFEST_VERSIONS,
	isLegacyMirrorSourceIdentity,
	parseRecoveryManifestVersions,
	parseUpdateSourceIdentity,
	preparedMatchesRelease,
	sameUpdateSourceIdentity,
	type UpdateSourceIdentity,
} from "./update-identity";

test("recovery capabilities are bounded, unique, canonical and never guessed from future versions", () => {
	expect(CURRENT_RECOVERY_MANIFEST_VERSIONS).toEqual([1, 2, 3]);
	expect(parseRecoveryManifestVersions(undefined)).toEqual([1, 2]);
	expect(parseRecoveryManifestVersions([3, 1, 2])).toEqual([1, 2, 3]);
	expect(parseRecoveryManifestVersions([1, 2, 4])).not.toContain(3);
	const versions = [1, 2, 3];
	const parsed = parseRecoveryManifestVersions(versions);
	versions.pop();
	expect(parsed).toEqual([1, 2, 3]);
	for (const invalid of [
		null,
		"3",
		{},
		[],
		[0],
		[-1],
		[1.5],
		["3"],
		[NaN],
		[Infinity],
		[256],
		[1, 2, 3, 3],
		Array.from({ length: 33 }, (_, index) => index + 1),
		new Array(2),
	]) {
		expect(() => parseRecoveryManifestVersions(invalid)).toThrow("recoveryManifestVersions");
	}
});

const mirror: UpdateSourceIdentity = {
	source: "update-server",
	repository: "fork/repo",
	serverUrl: "https://mirror.example/base",
	product: "narrafork",
	channel: "stable",
	platform: "linux-x64",
};
test("mirror authority includes configured repository, origin, product, channel and platform", () => {
	for (const changed of [
		{ repository: "other/repo" },
		{ serverUrl: "https://other.example" },
		{ product: "other" },
		{ channel: "beta" },
		{ platform: "linux-arm64" },
	]) {
		expect(sameUpdateSourceIdentity(mirror, { ...mirror, ...changed })).toBe(false);
	}
	expect(
		sameUpdateSourceIdentity(mirror, {
			...mirror,
			repository: "FORK/Repo",
			serverUrl: "https://MIRROR.example:443/base/",
		}),
	).toBe(true);
});
test("historical mirror without repository is recognizable history but cannot be a bound identity", () => {
	const { repository: _repository, ...legacy } = mirror;
	expect(isLegacyMirrorSourceIdentity(legacy)).toBe(true);
	expect(parseUpdateSourceIdentity(legacy)).toBeNull();
	expect(sameUpdateSourceIdentity(legacy, mirror)).toBe(false);
	expect(isLegacyMirrorSourceIdentity({ ...legacy, serverUrl: "file:///tmp/mirror" })).toBe(false);
	expect(isLegacyMirrorSourceIdentity({ ...legacy, repository: "invalid" })).toBe(false);
});
test("same-version prepared binary cannot adopt another repository's recommendation", () => {
	const release = {
		version: "1.0.1",
		sha512: "fixture",
		files: [{ size: 64 }],
		sourceIdentity: mirror,
	};
	const prepared = {
		id: "id",
		version: release.version,
		sha512: release.sha512,
		sizeBytes: 64,
		sourceIdentity: mirror,
	};
	expect(preparedMatchesRelease(prepared, release)).toBe(true);
	expect(
		preparedMatchesRelease(prepared, {
			...release,
			sourceIdentity: { ...mirror, repository: "other/repo" },
		}),
	).toBe(false);
	expect(preparedMatchesRelease({ ...prepared, sourceIdentity: null }, release)).toBe(false);
	for (const changed of [
		{ sha512: "changed" },
		{ files: [{ size: 65 }] },
		{ recoveryManifestVersions: [1, 2, 3] },
	]) {
		expect(preparedMatchesRelease(prepared, { ...release, ...changed })).toBe(false);
	}
	expect(preparedMatchesRelease(prepared, { ...release, recoveryManifestVersions: [2, 1] })).toBe(
		true,
	);
	expect(
		preparedMatchesRelease(
			{ ...prepared, recoveryManifestVersions: [1, 2, 3] },
			{ ...release, recoveryManifestVersions: [3, 2, 1] },
		),
	).toBe(true);
	expect(preparedMatchesRelease({ ...prepared, recoveryManifestVersions: [3, 3] }, release)).toBe(
		false,
	);
});
