import { create } from "zustand";

import { DEFAULT_DRAWING_ENGINE, type CanvasDrawingEngineSetting } from "@/lib/canvas/canvas-drawing-engine";

export type LocalUser = {
    id: string;
    username: string;
    email?: string;
    phone?: string;
    emailVerifiedAt?: string;
    phoneVerifiedAt?: string;
    displayName: string;
    avatarUrl?: string;
    avatarMode?: "custom" | "default" | "";
    avatarPreset?: string;
    identityProvider?: string;
    identityId?: string;
    identityUsername?: string;
    role: "admin" | "user";
    status: "active" | "disabled";
    lastLoginAt?: string;
    createdAt?: string;
    updatedAt?: string;
};

export type RuntimeLimits = {
    activeTaskLimit: number;
    resourceUploadMB: number;
    recycleBinRetentionDays?: number;
};

export type FeatureAvailability = {
    welcomeEnabled: boolean;
    shortDramaEnabled: boolean;
    taskCenterEnabled: boolean;
    creditsEnabled: boolean;
    walletMode?: "internal" | "external";
    rechargeUrl?: string;
    customChannelsEnabled: boolean;
    frontendModelsEnabled: boolean;
    pluginCenterEnabled: boolean;
    systemPluginsVisibleToUsers: boolean;
    inspirationSourcesVisible: boolean;
    imageWorkbenchEnabled: boolean;
    videoWorkbenchEnabled: boolean;
    configured?: boolean;
    updatedBy?: string;
    updatedAt?: string;
};

export const defaultFeatureAvailability: FeatureAvailability = {
    welcomeEnabled: true,
    shortDramaEnabled: true,
    taskCenterEnabled: true,
    creditsEnabled: true,
    customChannelsEnabled: true,
    frontendModelsEnabled: false,
    pluginCenterEnabled: true,
    systemPluginsVisibleToUsers: true,
    inspirationSourcesVisible: true,
    imageWorkbenchEnabled: false,
    videoWorkbenchEnabled: false,
};

type UserStore = {
    hydrated: boolean;
    user: LocalUser | null;
    sessionVersion: number;
    runtimeLimits: RuntimeLimits;
    drawingEngine: CanvasDrawingEngineSetting;
    features: FeatureAvailability;
    referralAvailable: boolean;
    setUser: (user: LocalUser | null, options?: { newSession?: boolean }) => void;
    setRuntimeLimits: (limits?: RuntimeLimits) => void;
    setDrawingEngine: (setting?: CanvasDrawingEngineSetting) => void;
    setFeatures: (features?: FeatureAvailability) => void;
    setReferralAvailable: (available?: boolean) => void;
    setHydrated: (hydrated: boolean) => void;
    clearSession: () => void;
};

export const useUserStore = create<UserStore>()((set) => ({
    hydrated: false,
    user: null,
    sessionVersion: 0,
    runtimeLimits: { activeTaskLimit: 5, resourceUploadMB: 50, recycleBinRetentionDays: 30 },
    drawingEngine: { defaultEngine: DEFAULT_DRAWING_ENGINE },
    features: defaultFeatureAvailability,
    referralAvailable: false,
    // Profile updates keep the session; login/logout and account switches fence
    // pending requests even when the next session returns to the same user ID.
    setUser: (user, options) => set((state) => ({ user, sessionVersion: state.sessionVersion + (options?.newSession || state.user?.id !== user?.id ? 1 : 0) })),
    setRuntimeLimits: (runtimeLimits) => set({ runtimeLimits: runtimeLimits || { activeTaskLimit: 5, resourceUploadMB: 50, recycleBinRetentionDays: 30 } }),
    setDrawingEngine: (drawingEngine) => set({ drawingEngine: drawingEngine || { defaultEngine: DEFAULT_DRAWING_ENGINE } }),
    setFeatures: (features) => set({ features: features ? { ...defaultFeatureAvailability, ...features } : defaultFeatureAvailability }),
    setReferralAvailable: (available) => set({ referralAvailable: Boolean(available) }),
    setHydrated: (hydrated) => set({ hydrated }),
    clearSession: () => set((state) => ({ user: null, sessionVersion: state.sessionVersion + 1, runtimeLimits: { activeTaskLimit: 5, resourceUploadMB: 50, recycleBinRetentionDays: 30 }, drawingEngine: { defaultEngine: DEFAULT_DRAWING_ENGINE }, features: defaultFeatureAvailability, referralAvailable: false })),
}));
