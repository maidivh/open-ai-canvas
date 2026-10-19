import { describe, expect, test } from "bun:test";

import { shouldShowSidebarCheckin } from "../src/lib/sidebar-checkin";
import { getWalletAccess, normalizeWalletSettings, validateWalletSettings } from "../src/lib/wallet-mode";
import type { WalletSettings, WalletSummary } from "../src/services/api/wallet";

const internal = {
    walletMode: "internal",
    account: { userId: "user-1", availableMicrocredits: 0, reservedMicrocredits: 0 },
} as WalletSummary;
const external = { ...internal, walletMode: "external", rechargeUrl: "https://wallet.example/ai/recharge", account: { ...internal.account, availableMicrocredits: 12_000_000 } } as WalletSummary;
const settings: WalletSettings = { mode: "internal", baseUrl: "", clientId: "", issuer: "", siteId: 0, rechargeUrl: "", secretConfigured: false, version: 1 };

describe("wallet mode boundaries", () => {
    test("internal zero balance remains a real balance and retains local funding", () => {
        expect(getWalletAccess(internal, {}, false)).toMatchObject({ walletMode: "internal", availableMicrocredits: 0, localActionsAllowed: true });
    });
    test("external wallet never enables local funding, even when the old credits flag is off", () => {
        expect(getWalletAccess(external, { walletMode: "external" }, false)).toMatchObject({ walletMode: "external", availableMicrocredits: 12_000_000, localActionsAllowed: false, rechargeUrl: external.rechargeUrl });
    });
    test("a failed refresh invalidates cached balance and disables local funding", () => {
        expect(getWalletAccess(internal, {}, true)).toMatchObject({ availableMicrocredits: null, localActionsAllowed: false });
        expect(getWalletAccess(external, {}, true).availableMicrocredits).toBeNull();
    });
    test("first wallet failure retains public external mode and recharge target without a zero fallback", () => {
        expect(getWalletAccess(null, { walletMode: "external", rechargeUrl: external.rechargeUrl }, true)).toMatchObject({ walletMode: "external", rechargeUrl: external.rechargeUrl, availableMicrocredits: null, localActionsAllowed: false });
    });
    test("recharge links reject script URLs and embedded credentials", () => {
        for (const rechargeUrl of ["javascript:alert(1)", "data:text/html,x", "https://user:pass@wallet.example/"]) {
            expect(getWalletAccess({ ...external, rechargeUrl }, {}, false).rechargeUrl).toBe("");
        }
    });
    test("external mode hides signup-era checkin policies even if the server sends a bonus", () => {
        expect(shouldShowSidebarCheckin({ creditsEnabled: true, walletMode: "external", checkinBonusMicrocredits: 10_000_000, checkedInToday: false })).toBe(false);
        expect(shouldShowSidebarCheckin({ creditsEnabled: true, walletMode: "internal", checkinBonusMicrocredits: 10_000_000, checkedInToday: false })).toBe(true);
    });
});

describe("wallet settings form", () => {
    const draft = { mode: "external" as const, baseUrl: " https://wallet.example/ ", clientId: " canvas ", clientSecret: " test-secret ", siteId: 2, issuer: " cloooud ", rechargeUrl: " https://wallet.example/ai/recharge ", version: 3 };
    test("normalizes identity and origin fields while preserving secret bytes", () => {
        expect(normalizeWalletSettings(draft)).toEqual({ ...draft, baseUrl: "https://wallet.example", clientId: "canvas", issuer: "cloooud", rechargeUrl: "https://wallet.example/ai/recharge" });
    });
    test("requires an external credential but allows leaving an existing secret untouched", () => {
        const noSecret = { ...normalizeWalletSettings(draft), clientSecret: "" };
        expect(validateWalletSettings(noSecret, false)).toContain("密钥");
        expect(validateWalletSettings(noSecret, true)).toBe("");
        expect(validateWalletSettings({ ...settings, mode: "internal" }, false)).toBe("");
    });
    test("rejects prefixed origins and incomplete external identity configuration", () => {
        expect(validateWalletSettings({ ...draft, baseUrl: "https://wallet.example/api" }, false)).toContain("域名");
        expect(validateWalletSettings({ ...draft, siteId: 0 }, false)).toContain("站点");
        expect(validateWalletSettings({ ...draft, issuer: "" }, false)).toContain("身份");
    });
});
