import { useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { useEffect } from "react";

import { getWallet, type WalletSummary } from "@/services/api/wallet";
import { refreshFeatureAvailability } from "@/services/feature-availability";
import { useUserStore } from "@/stores/use-user-store";

// 多处余额共用按用户隔离的 Query。功能关闭时仍在进入/唤醒/钱包事件后检查功能
// 状态，才能发现服务端切换到外部钱包；不为常驻布局增加轮询。
const WALLET_STALE_TIME_MS = 5 * 60_000;
const walletBalanceQueryKey = (userId: string) => ["wallet-balance", userId] as const;
const walletRefreshSubscriptions = new WeakMap<QueryClient, Map<string, { consumers: number; dispose: () => void }>>();

function subscribeWalletRefresh(queryClient: QueryClient, userId: string) {
    let subscriptions = walletRefreshSubscriptions.get(queryClient);
    if (!subscriptions) {
        subscriptions = new Map();
        walletRefreshSubscriptions.set(queryClient, subscriptions);
    }
    let subscription = subscriptions.get(userId);
    if (!subscription) {
        let active = true;
        let sequence = 0;
        const queryKey = walletBalanceQueryKey(userId);
        const handleUpdated = () => {
            const requestedSequence = ++sequence;
            const user = useUserStore.getState().user;
            // 同一用户只装一组监听。新事件取消旧快照后重读；显式取消也覆盖尚无
            // 缓存数据的首次请求，不能用 cancelRefetch:false 吞掉后续独立事件。
            void queryClient.cancelQueries({ queryKey, exact: true }).then(() => {
                if (!active || requestedSequence !== sequence || useUserStore.getState().user !== user) return;
                return queryClient.invalidateQueries({ queryKey, exact: true }, { cancelRefetch: false });
            });
        };
        const handleVisible = () => { if (document.visibilityState !== "hidden") handleUpdated(); };
        window.addEventListener("wallet:updated", handleUpdated);
        window.addEventListener("focus", handleVisible);
        document.addEventListener("visibilitychange", handleVisible);
        subscription = {
            consumers: 0,
            dispose: () => {
                active = false;
                window.removeEventListener("wallet:updated", handleUpdated);
                window.removeEventListener("focus", handleVisible);
                document.removeEventListener("visibilitychange", handleVisible);
            },
        };
        subscriptions.set(userId, subscription);
        // 公共设置可能在此页没有余额消费者时更新；不能沿用仍 fresh 的旧模式缓存。
        const cached = queryClient.getQueryData<WalletSnapshot>(queryKey);
        const features = useUserStore.getState().features;
        if (cached && (cached.walletMode !== (features.walletMode || "internal") || (cached.policy !== null) !== features.creditsEnabled)) handleUpdated();
    }
    const registered = subscription;
    registered.consumers += 1;
    return () => {
        registered.consumers -= 1;
        if (registered.consumers) return;
        registered.dispose();
        subscriptions.delete(userId);
        if (!subscriptions.size) walletRefreshSubscriptions.delete(queryClient);
    };
}

export type WalletSnapshot = {
    availableMicrocredits: number | null;
    policy: WalletSummary["policy"] | null;
    walletMode: "internal" | "external";
    rechargeUrl?: string;
};

export function useWalletBalance(userId?: string, enabled = true) {
    const publicMode = useUserStore((state) => state.features.walletMode);
    const publicRechargeUrl = useUserStore((state) => state.features.rechargeUrl);
    const sessionUserId = useUserStore((state) => state.user?.id);
    const activeUserId = userId === sessionUserId ? userId || "" : "";
    const queryClient = useQueryClient();
    const query = useQuery({
        queryKey: walletBalanceQueryKey(activeUserId),
        enabled: Boolean(activeUserId),
        queryFn: async ({ signal }): Promise<WalletSnapshot> => {
            const user = useUserStore.getState().user;
            // 本请求会继续读取余额，不广播通知来取消自身；其他公共刷新会通知共享入口。
            const features = await refreshFeatureAvailability(signal, { notifyWallet: false });
            signal.throwIfAborted();
            if (useUserStore.getState().user !== user) throw new DOMException("用户已切换", "AbortError");
            if (!features.creditsEnabled) return { availableMicrocredits: null, policy: null, walletMode: features.walletMode || "internal", rechargeUrl: features.rechargeUrl };
            const wallet = await getWallet(1, 1, "all", signal);
            signal.throwIfAborted();
            if (wallet.account.userId !== activeUserId) throw new Error("积分账户与当前用户不一致");
            const current = useUserStore.getState();
            // 公共刷新会替换 features 对象；只有钱包配置变化才使余额快照失效。
            if (current.user !== user || current.features.walletMode !== features.walletMode || current.features.creditsEnabled !== features.creditsEnabled || current.features.rechargeUrl !== features.rechargeUrl) throw new DOMException("钱包状态已更新", "AbortError");
            const walletMode = wallet.walletMode || features.walletMode || "internal";
            const rechargeUrl = wallet.rechargeUrl ?? features.rechargeUrl;
            if (features.walletMode !== walletMode || features.rechargeUrl !== rechargeUrl) {
                current.setFeatures({ ...current.features, walletMode, rechargeUrl, creditsEnabled: walletMode === "external" || features.creditsEnabled });
            }
            return { availableMicrocredits: wallet.account.availableMicrocredits, policy: wallet.policy, walletMode, rechargeUrl };
        },
        staleTime: WALLET_STALE_TIME_MS,
        refetchOnWindowFocus: false,
    });

    useEffect(() => {
        if (!activeUserId) return;
        return subscribeWalletRefresh(queryClient, activeUserId);
    }, [activeUserId, queryClient]);

    useEffect(() => {
        if (query.error) console.warn("积分余额刷新失败", query.error);
    }, [query.error]);

    const walletMode = publicMode ?? query.data?.walletMode ?? "internal";
    const available = enabled && activeUserId && !query.isError && query.data?.walletMode === walletMode;
    return {
        availableMicrocredits: available ? query.data?.availableMicrocredits ?? null : null,
        policy: available ? query.data?.policy ?? null : null,
        walletMode,
        rechargeUrl: publicRechargeUrl ?? query.data?.rechargeUrl,
        error: query.error,
        refreshing: query.isFetching,
        refresh: query.refetch,
    };
}
