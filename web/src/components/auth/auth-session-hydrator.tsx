import type { ReactNode } from "react";
import { useEffect, useState } from "react";

import { getAuthSession, invalidateAuthSessionCache } from "@/services/api/auth";
import { FullScreenLoader } from "@/components/ui/aceternity/full-screen-loader";
import { WorkspaceErrorState } from "@/components/layout/workspace-state";
import { appQueryClient } from "@/lib/query-client";
import { assertCurrentUserSession, captureUserSession, isUserSessionInvalidated, subscribeUserSessionInvalidation } from "@/lib/user-scope";
import { preloadWorkspaceRoute } from "@/lib/workspace-route-modules";
import { useUserStore } from "@/stores/use-user-store";
import { recordDiagnosticEvent } from "@/services/diagnostics/client-diagnostics";
import { ACTIVE_USER_SCOPE_KEY, getActiveUserScope } from "@/lib/user-scope";

export function AuthSessionHydrator({ children }: { children: ReactNode }) {
    const hydrated = useUserStore((state) => state.hydrated);
    const [recoveryFailed, setRecoveryFailed] = useState(false);
    const [retry, setRetry] = useState(0);

    useEffect(() => {
        // [小洞免登定制] 在上游会话恢复组件补跨标签换号监听，Cookie 变化后重建整页工作区。
        // 返回项目/画布入口而非保留旧作品详情地址，避免用新账号打开旧账号的编辑状态。
        // 重载前的请求仍由页面账号头拦截；本地延迟保存仍由 user-scope.ts 固定到旧账号。
        const onAccountChanged = (event: StorageEvent) => {
            if (event.storageArea !== window.localStorage || (event.key !== ACTIVE_USER_SCOPE_KEY && event.key !== null)) return;
            if (event.key !== null && event.newValue === getActiveUserScope()) return;
            const target = window.location.pathname.startsWith("/projects") ? "/projects" : "/canvas";
            window.location.replace(target);
        };
        window.addEventListener("storage", onAccountChanged);
        return () => window.removeEventListener("storage", onAccountChanged);
    }, []);

    useEffect(() => {
        let cancelled = false;
        let recovery = 0;
        const restore = async (externalChange: boolean) => {
            const current = ++recovery;
            const isCurrent = () => !cancelled && current === recovery;
            const startedAt = performance.now();
            setRecoveryFailed(false);
            useUserStore.getState().setHydrated(false);
            if (externalChange) {
                appQueryClient.clear();
                invalidateAuthSessionCache();
            }
            recordDiagnosticEvent({ category: "navigation", level: "info", code: "startup.auth_session_started", message: "开始恢复认证会话" });
            const session = captureUserSession(true);
            try {
                if (externalChange) {
                    const { suspendUserSession } = await import("@/lib/user-session");
                    if (!isCurrent()) return;
                    suspendUserSession();
                }
                const payload = await getAuthSession();
                if (!isCurrent()) return;
                assertCurrentUserSession(session, true);
                recordDiagnosticEvent({
                    category: "navigation",
                    level: "info",
                    code: "startup.auth_session_ready",
                    message: payload.user ? "认证会话已恢复" : "匿名会话已确认",
                    durationMs: performance.now() - startedAt,
                });
                // 账号数据、画布和素材持久化只属于已登录工作区，登录页不下载这些模块。
                const { applyUserSession } = await import("@/lib/user-session");
                if (!isCurrent()) return;
                assertCurrentUserSession(session, true);
                await applyUserSession(payload, session);
                if (!isCurrent()) return;
                recordDiagnosticEvent({
                    category: "navigation",
                    level: "info",
                    code: payload.user ? "startup.workspace_ready" : "startup.anonymous_ready",
                    message: payload.user ? "登录工作区已解除启动阻塞" : "匿名页面已解除启动阻塞",
                    durationMs: performance.now() - startedAt,
                });
                if (payload.user) preloadWorkspaceRoute(window.location.pathname);
            } catch (error) {
                if (isCurrent() && !(error instanceof DOMException && error.name === "AbortError")) {
                    setRecoveryFailed(true);
                    recordDiagnosticEvent({
                        category: "navigation",
                        level: "warning",
                        code: "startup.auth_session_failed",
                        message: "认证会话恢复失败，工作区已暂停，等待重试",
                        durationMs: performance.now() - startedAt,
                    });
                }
            }
        };
        const unsubscribe = subscribeUserSessionInvalidation(() => void restore(true));
        void restore(isUserSessionInvalidated());
        return () => {
            cancelled = true;
            unsubscribe();
        };
    }, [retry]);

    if (recoveryFailed) return <WorkspaceErrorState title="登录状态恢复失败" description="工作区已暂停，请检查网络后重试。" onRetry={() => setRetry((value) => value + 1)} />;
    return hydrated ? children : <FullScreenLoader />;
}
