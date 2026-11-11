import { http } from "@/services/api/request";

/** 海外版网关返回的素材状态；只有 Active 会带 asset_uri。 */
export type WantraceAssetStatus = "Processing" | "Active" | "Failed" | "Uncertain" | string;
export type WantraceVerificationStatus = "pending" | "verified" | "expired" | "failed" | string;

export type WantraceAsset = {
    id: string;
    groupId: string;
    resourceId: string;
    name: string;
    assetType: "Image" | string;
    status: WantraceAssetStatus;
    assetUri: string;
    error?: string;
    libraryAssetId?: string;
    createdAt: string;
    updatedAt: string;
};

export type WantraceGroup = {
    id: string;
    kind: "real" | string;
    name: string;
    description: string;
    channelId: number;
    remoteId: string;
    upstreamId: string;
    state: string;
    expiresAt?: string;
    expired: boolean;
    createdAt: string;
    assets: WantraceAsset[];
};

export type WantraceVerification = {
    id: string;
    name: string;
    status: WantraceVerificationStatus;
    groupId?: string;
    /** 仅在发起认证时返回；不要写入本地存储。 */
    h5Link?: string;
    error?: string;
    expiresAt: string;
    createdAt: string;
};

export type WantraceAssetConfig = {
    channelId: number;
    realGroupLimit: number;
    realAuthorizationDays: number;
};

export type WantraceOverview = {
    enabled: boolean;
    realEnabled: boolean;
    config: WantraceAssetConfig;
    groups: WantraceGroup[];
    verifications: WantraceVerification[];
    usage: number;
};

export const wantraceOverviewQueryKey = ["wantrace-assets"] as const;

export function getWantraceOverview(signal?: AbortSignal) {
    return http.get<WantraceOverview>("/wantrace-assets", { signal });
}

export function createWantraceVerification(input: { name: string }) {
    return http.post<{ verification: WantraceVerification }>("/wantrace-assets/verifications", input);
}

export function checkWantraceVerification(id: string) {
    return http.post<{ verification: WantraceVerification }>(`/wantrace-assets/verifications/${encodeURIComponent(id)}/check`);
}

export function createWantraceAsset(groupId: string, input: { resourceId: string; name?: string }) {
    return http.post<{ asset: WantraceAsset }>(`/wantrace-assets/groups/${encodeURIComponent(groupId)}/assets`, input);
}

export function refreshWantraceAsset(id: string) {
    return http.post<{ asset: WantraceAsset }>(`/wantrace-assets/assets/${encodeURIComponent(id)}/refresh`);
}

/** 把已通过的海外版图片加入素材库「方舟认证」分类，生成时按渠道使用 asset_uri。 */
export function addWantraceAssetToLibrary(id: string) {
    return http.post<{ assetId: string }>(`/wantrace-assets/assets/${encodeURIComponent(id)}/library`);
}

export type AdminWantraceAssetSetting = {
    enabled: boolean;
    realEnabled: boolean;
    baseUrl: string;
    channelId: number;
    channelName: string;
    hasApiKey: boolean;
    realGroupLimit: number;
    realAuthorizationDays: number;
    updatedBy: string;
    updatedAt: string;
};

export type WantraceChannel = { id: number; name: string; group: string };

export type UpdateWantraceAssetSettingInput = {
    enabled: boolean;
    realEnabled: boolean;
    apiKey?: string;
    channelId: number;
    realGroupLimit: number;
    realAuthorizationDays: number;
};

export function getAdminWantraceAssetSetting(signal?: AbortSignal) {
    return http.get<{ setting: AdminWantraceAssetSetting }>("/admin/settings/wantrace-assets", { signal });
}

export function updateAdminWantraceAssetSetting(input: UpdateWantraceAssetSettingInput) {
    return http.patch<{ setting: AdminWantraceAssetSetting }>("/admin/settings/wantrace-assets", input);
}

/** 读取 Key 可访问的渠道；Key 放在请求体，避免出现在 URL 和访问日志。 */
export function listAdminWantraceChannels(apiKey: string) {
    return http.post<{ channels: WantraceChannel[] }>("/admin/settings/wantrace-assets/channels", { apiKey });
}

export function testAdminWantraceAssetSetting() {
    return http.post<{ ok: boolean; channelId: number; channelName: string; channels: WantraceChannel[] }>("/admin/settings/wantrace-assets/test");
}
