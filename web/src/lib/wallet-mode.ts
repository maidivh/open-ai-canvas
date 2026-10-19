import type { WalletSettingsInput, WalletSummary } from "@/services/api/wallet";

type WalletPublicSettings = Pick<WalletSummary, "walletMode" | "rechargeUrl">;

function safeRechargeUrl(value?: string) {
    try {
        const url = new URL(value || "");
        return ["https:", "http:"].includes(url.protocol) && !url.username && !url.password ? url.href : "";
    } catch {
        return "";
    }
}

/** A cached balance must not look current after an upstream failure. */
export function getWalletAccess(wallet: WalletSummary | null | undefined, settings: WalletPublicSettings, failed: boolean) {
    const walletMode = wallet?.walletMode ?? settings.walletMode ?? "internal";
    return {
        walletMode,
        rechargeUrl: safeRechargeUrl(wallet?.rechargeUrl ?? settings.rechargeUrl),
        availableMicrocredits: !failed && wallet ? wallet.account.availableMicrocredits : null,
        localActionsAllowed: !failed && Boolean(wallet) && walletMode === "internal",
    };
}

export function normalizeWalletSettings(input: WalletSettingsInput): WalletSettingsInput {
    return {
        mode: input.mode,
        baseUrl: (input.baseUrl || "").trim().replace(/\/+$/, ""),
        clientId: (input.clientId || "").trim(),
        clientSecret: input.clientSecret || "",
        siteId: input.siteId,
        issuer: (input.issuer || "").trim(),
        rechargeUrl: (input.rechargeUrl || "").trim(),
        version: input.version,
    };
}

export function validateWalletSettings(input: WalletSettingsInput, secretConfigured: boolean) {
    if (input.mode === "internal") return "";
    const value = normalizeWalletSettings(input);
    try {
        const url = new URL(value.baseUrl);
        if (!["http:", "https:"].includes(url.protocol) || url.username || url.password || url.pathname !== "/" || url.search || url.hash) throw new Error();
    } catch {
        return "请填写小洞AI站点域名，不包含接口路径、查询参数或账号密码";
    }
    if (!value.clientId) return "请填写计费客户端标识";
    if (!value.clientSecret?.trim() && !secretConfigured) return "请填写计费客户端密钥";
    if (!Number.isSafeInteger(value.siteId) || value.siteId <= 0) return "请填写有效的站点 ID";
    if (!value.issuer) return "请填写与 SSO 绑定一致的身份签发方";
    if (value.rechargeUrl && !safeRechargeUrl(value.rechargeUrl)) return "充值地址须为有效的 HTTP 或 HTTPS 链接";
    return "";
}
