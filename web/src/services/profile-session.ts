import { invalidateAuthSessionCache } from "@/services/api/auth";
import { getUserProfile } from "@/services/api/profile";
import { useUserStore } from "@/stores/use-user-store";

export async function loadProfileSession(userId: string | undefined, signal: AbortSignal) {
    const sessionVersion = useUserStore.getState().sessionVersion;
    const profile = await getUserProfile(signal);
    signal.throwIfAborted();
    const current = useUserStore.getState();
    if (current.sessionVersion !== sessionVersion || current.user?.id !== userId) throw new DOMException("用户已切换", "AbortError");
    if (profile.user.id !== userId) throw new Error("账户资料与当前用户不一致");
    invalidateAuthSessionCache();
    current.setUser(profile.user);
    return profile;
}
